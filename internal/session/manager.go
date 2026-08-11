package session

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/hegade/kunai/internal/claude"
)

// closeGrace bounds how long a respawn waits for the old driver to finish closing
// before going ahead without it. Generous, because the normal case takes
// milliseconds and this is only a backstop against a pump that cannot exit.
const closeGrace = 10 * time.Second

// Manager owns all live sessions. It is safe for concurrent use.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
	// onChange fires whenever the session LIST would look different: one was
	// created, one went away, or one changed state. The server uses it to push
	// the list to connected clients rather than having them ask for it on a
	// timer, which is what made a status board up to eight seconds stale.
	onChange func()
}

func NewManager() *Manager {
	return &Manager{sessions: make(map[string]*Session)}
}

// SetOnChange registers the list-changed callback. Called once at wiring time,
// before any session exists.
func (m *Manager) SetOnChange(fn func()) {
	m.mu.Lock()
	m.onChange = fn
	m.mu.Unlock()
}

// changed fires the callback outside the manager's lock, since a listener fans
// out to sockets and must never be able to stall session bookkeeping.
func (m *Manager) changed() {
	m.mu.Lock()
	fn := m.onChange
	m.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// CreateOptions configure a new session.
// envKV flattens an env map into the KEY=VALUE slice the driver appends to the
// process environment.
func envKV(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

type CreateOptions struct {
	Cwd    string
	Title  string
	Model  string
	Effort string // reasoning effort: low|medium|high|xhigh|max (spawn-time only)
	// Resume, when set, reattaches to an existing CLI session id (loading its
	// transcript) rather than starting a fresh conversation.
	Resume string
	// SessionID, when set (and Resume is empty), forces the new session's id
	// instead of generating one. Used to respawn a session under the same id
	// without a transcript (e.g. an effort change before the first turn, where
	// there is nothing to resume).
	SessionID string
	// Seed pre-populates the replay buffer with past turns (used with Resume so
	// the client sees the prior conversation).
	Seed []SeedTurn
	// ContextTokens seeds the context-usage meter for a resumed session, so it
	// shows the real fill immediately instead of waiting for the next turn.
	ContextTokens int64
	// Overhead seeds the resident context overhead (system prompt, tools, memory,
	// skills) measured from the transcript, so the meter stays right the moment a
	// resumed session next compacts: a compaction's postTokens omits this, and it
	// cannot be recovered from the compaction frame alone. See loadTranscriptContextTokens.
	Overhead int64
	// HistBefore is the transcript byte offset where the seeded tail began. Older
	// history lives in [0, HistBefore); the client pages it in on reverse scroll.
	// 0 means the whole transcript was seeded (nothing older to fetch).
	HistBefore int64
	// Mode is the permission mode to spawn in; empty means DefaultPermissionMode.
	Mode string
	// CLI names which Claude CLI (account) this session runs on. CLIName is for
	// display, Bin is the binary to exec (empty = "claude"), Env is extra process
	// environment. Set by the server from the chosen CLI profile.
	CLIName string
	Bin     string
	Env     map[string]string
	// AppendSystemPrompt is extra system prompt baked in at spawn, used to tell
	// the model which git worktree it is working in. Spawn-time only, so restart
	// carries it over the same way it carries the account and the effort.
	AppendSystemPrompt string
	// DisallowedTools names tools this session must not offer the model, set when
	// the session is shared with someone who is not its owner, and when a pull
	// request review runs unattended. Spawn-time, so it is carried by spawnSpec
	// across every respawn: a tool restriction silently dropped by a later effort
	// change would hand a guest the toolset the share was created to withhold.
	DisallowedTools []string
	// ToolsOwner names what withheld them, so a feature that reconciles its own
	// restrictions cannot lift somebody else's. See Session.toolsOwner.
	ToolsOwner string
}

// Create registers a new claude session and returns immediately; the CLI boots
// in the background so opening a session is instant. Boot failures surface to
// attached clients as an error event. Our handle id doubles as the CLI
// --session-id so --resume works with the same id after a restart.
func (m *Manager) Create(ctx context.Context, opts CreateOptions) (*Session, error) {
	if opts.Cwd == "" {
		return nil, errors.New("cwd required")
	}
	if fi, err := os.Stat(opts.Cwd); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("not a directory: %s", opts.Cwd)
	}
	id := opts.Resume
	if id == "" {
		id = opts.SessionID
	}
	if id == "" {
		id = newUUID()
	}

	m.mu.Lock()
	if _, exists := m.sessions[id]; exists {
		m.mu.Unlock()
		return nil, fmt.Errorf("session %s already live", id)
	}
	m.mu.Unlock()

	// Resolve the permission mode here, once, so the spawned process and the mode
	// we report to clients can never disagree.
	mode := opts.Mode
	if mode == "" {
		mode = DefaultPermissionMode
	}

	drvOpts := claude.Options{
		Cwd: opts.Cwd, Model: opts.Model, Effort: opts.Effort, PermissionMode: CLIModeFor(mode),
		Bin: opts.Bin, Env: envKV(opts.Env), AppendSystemPrompt: opts.AppendSystemPrompt,
		DisallowedTools: opts.DisallowedTools,
	}
	if opts.Resume != "" {
		drvOpts.Resume = opts.Resume
	} else {
		drvOpts.SessionID = id
	}
	drv := claude.NewSession(drvOpts)

	s := newSession(id, opts.Cwd, opts.Title, drv)
	s.model = opts.Model
	s.effort = opts.Effort
	s.mode = mode
	s.cliName = opts.CLIName
	s.cliBin = opts.Bin
	s.cliEnv = opts.Env
	s.appendPrompt = opts.AppendSystemPrompt
	s.disallowedTools = opts.DisallowedTools
	s.toolsOwner = opts.ToolsOwner
	s.contextTokens = opts.ContextTokens
	s.overhead = opts.Overhead
	s.histBefore = opts.HistBefore
	if len(opts.Seed) > 0 {
		s.Seed(opts.Seed)
	}

	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()
	// Every later state change on this session is a change to the list too, so it
	// reports through the same callback the manager fires for create and remove.
	s.SetOnListChange(m.changed)
	m.changed()

	// Reap from the registry when the session ends. Instance-checked so a restart
	// that re-creates the same id is not reaped by the old session's goroutine.
	go func() {
		<-s.Done()
		m.removeIf(id, s)
	}()

	// Boot the CLI off the request path. Prompts sent meanwhile queue in the
	// driver and flush once the process is up.
	go func() {
		if err := drv.Start(context.Background()); err != nil {
			s.FailStart(err.Error())
		}
	}()
	return s, nil
}

func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	return s, ok
}

func (m *Manager) List() []Meta {
	m.mu.Lock()
	metas := make([]Meta, 0, len(m.sessions))
	for _, s := range m.sessions {
		metas = append(metas, s.Meta())
	}
	m.mu.Unlock()
	sort.Slice(metas, func(i, j int) bool { return metas[i].CreatedAt.After(metas[j].CreatedAt) })
	return metas
}

func (m *Manager) Close(id string) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	m.mu.Unlock()
	if ok {
		s.Close()
	}
}

func (m *Manager) CloseAll() {
	for _, s := range m.snapshot() {
		s.Close()
	}
}

// StopForThermal interrupts every live session: it settles any loop and aborts
// the running turn, but leaves the claude processes alive so the sessions stay
// resumable. This is the guardian's soft trip: the heat comes from the turns, so
// stopping them is what cools the machine, and killing the processes outright
// would throw away recoverable work for no extra cooling. Returns the count.
func (m *Manager) StopForThermal() int {
	sessions := m.snapshot()
	for _, s := range sessions {
		_ = s.StopForThermal()
	}
	return len(sessions)
}

// snapshot copies the live sessions under the lock, then releases it so a slow
// per-session call cannot block the registry (the CloseAll pattern).
func (m *Manager) snapshot() []*Session {
	m.mu.Lock()
	all := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		all = append(all, s)
	}
	m.mu.Unlock()
	return all
}

func (m *Manager) remove(id string) {
	m.mu.Lock()
	_, existed := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if existed {
		m.changed()
	}
}

// removeIf deletes id only if it still maps to this exact session, so a stale
// reap goroutine cannot evict a freshly re-created session with the same id.
func (m *Manager) removeIf(id string, s *Session) {
	m.mu.Lock()
	hit := m.sessions[id] == s
	if hit {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	if hit {
		m.changed()
	}
}

// acctOverride swaps the account (name/bin/env) a session runs on across a
// respawn. The caller is responsible for making the transcript reachable under
// the new account's config dir first (see the account-switch handler).
type acctOverride struct {
	name  string
	bin   string
	env   map[string]string
	model string // "" carries the old model over; set to reset it (e.g. provider -> Claude)
}

// restartOverride is what a respawn is changing. Everything left zero carries
// over from the running session, which is the default that matters: the failure
// mode here is not a wrong value, it is a value nobody remembered to mention
// silently reverting to whatever a fresh session would get.
//
// tools is a pointer because "leave the restrictions alone" and "remove them" are
// different instructions and both have to be expressible.
type restartOverride struct {
	effort string
	acct   *acctOverride
	tools  *[]string
	// toolsOwner rides with tools rather than standing alone: who withheld them is
	// part of the same instruction, and setting one without the other would leave a
	// restriction nobody claims or an owner for tools that are gone.
	toolsOwner string
	mode       string
}

// RestartWithEffort relaunches a live session at a new reasoning effort by
// closing it and re-creating it with --resume (effort is a spawn-time CLI flag,
// so it cannot change on the running process). The conversation is preserved via
// the transcript: seedFn loads it back into the replay buffer. The new session
// keeps the same id (resume forces id == claude session id).
func (m *Manager) RestartWithEffort(ctx context.Context, id, effort string, seedFn func(configDir, cid string) []SeedTurn) (*Session, error) {
	return m.restart(ctx, id, restartOverride{effort: effort}, seedFn)
}

// RestartWithTools relaunches a session with a different set of withheld tools,
// used when a session is shared with somebody who is not its owner and when that
// share ends. The tool list is a spawn-time CLI flag, so like effort it cannot be
// changed on a running process. mode, when set, becomes the session's standing
// permission mode. owner records what is withholding them, and is what stops one
// feature's reconciler from lifting another's restriction.
func (m *Manager) RestartWithTools(ctx context.Context, id string, denied []string, owner, mode string, seedFn func(configDir, cid string) []SeedTurn) (*Session, error) {
	return m.restart(ctx, id, restartOverride{tools: &denied, toolsOwner: owner, mode: mode}, seedFn)
}

// RestartWithAccount relaunches a live session on a different Claude account,
// keeping its conversation. Effort and everything else carry over; only the
// account (name/bin/env) changes, so the resumed process authenticates and bills
// as the new account. The transcript must already be present under the new
// account's config dir (the handler copies it before calling this).
func (m *Manager) RestartWithAccount(ctx context.Context, id, name, bin string, env map[string]string, seedFn func(configDir, cid string) []SeedTurn) (*Session, error) {
	return m.restart(ctx, id, restartOverride{acct: &acctOverride{name: name, bin: bin, env: env}}, seedFn)
}

// RestartWithAccountModel is RestartWithAccount plus a model reset, for switching to
// an account where the current model is meaningless (e.g. provider -> Claude, where
// a carried-over "grok-4.5" is not a Claude tier and leaves the picker blank). An
// empty model carries the old one over, as before.
func (m *Manager) RestartWithAccountModel(ctx context.Context, id, name, bin string, env map[string]string, model string, seedFn func(configDir, cid string) []SeedTurn) (*Session, error) {
	return m.restart(ctx, id, restartOverride{acct: &acctOverride{name: name, bin: bin, env: env, model: model}}, seedFn)
}

// restart is the shared respawn: close the live process and re-create it with
// --resume so the conversation is preserved via the transcript. effort != ""
// changes the reasoning effort; acct != nil changes the account. Anything not
// overridden carries over from the old session.
func (m *Manager) restart(ctx context.Context, id string, ov restartOverride, seedFn func(configDir, cid string) []SeedTurn) (*Session, error) {
	m.mu.Lock()
	old, ok := m.sessions[id]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("session %s not live", id)
	}
	cid := old.ClaudeSessionID()
	meta := old.Meta()
	// Everything spawn-time carries over unless this restart is explicitly
	// changing it; see spawnSpec for why that decision lives in one place.
	spec := specOf(old).withOverrides(ov)
	// A loop is carried across the respawn rather than killed by it: the run is
	// meant to survive a process change, exactly as it survives a kunai restart.
	// The mode it borrowed must NOT be, or the new process is built permissive
	// with no loop left to hand the mode back. See Session.LoopHandoff.
	loopRec, preLoopMode, hadLoop := old.LoopHandoff()
	if hadLoop && preLoopMode != "" {
		spec.mode = preLoopMode
	}
	dir := spec.configDir() // where the resumed process reads its transcript

	old.Close()
	// Bounded, because this wait is the last thing standing between a wedged
	// session and a person who cannot get their work off a walled account. Done()
	// closes when the driver's event pump exits, so anything that blocks that pump
	// blocks this forever, and an unbounded wait here turns one stuck session into
	// an account switch that never answers (the HTTP handler's own timeout cannot
	// help: a channel receive does not watch ctx). Close() has already cancelled
	// the process and killed it, so the old CLI is gone either way and respawning
	// is safe; waiting is only about letting the pump finish tidily first. On
	// timeout, say so and carry on rather than leaving the session unrecoverable.
	select {
	case <-old.Done():
	case <-time.After(closeGrace):
		log.Printf("session %s: the driver did not finish closing within %s; respawning anyway", id, closeGrace)
	}
	m.removeIf(id, old) // synchronous, so the recreate below won't collide

	// The process must be respawned. Three cases, distinguished by whether the CLI
	// has assigned a session id (only after the first turn) and whether a
	// transcript exists on disk:
	//   - prompted this run: resume by the live CLI session id.
	//   - resumed from history but not yet prompted: no live id yet, but a
	//     transcript exists under the handle id, so resume that (a fresh
	//     --session-id would collide with the existing transcript and the CLI
	//     refuses to start).
	//   - brand-new session, no turns and no transcript: respawn fresh under the
	//     same handle id.
	opts := CreateOptions{Cwd: meta.Cwd, Title: meta.Title}
	spec.apply(&opts)
	if cid != "" {
		opts.Resume, opts.Seed = cid, seedFn(dir, cid)
	} else if seed := seedFn(dir, id); len(seed) > 0 {
		opts.Resume, opts.Seed = id, seed
	} else {
		opts.SessionID = id
	}
	sess, err := m.Create(ctx, opts)
	if err != nil {
		// The old session is already gone. The loop's record is still on disk
		// saying "running", which is the right outcome: nothing is driving it now,
		// and the next boot will pick it up rather than lose the run.
		return nil, err
	}
	if hadLoop {
		// Re-arms the loop on the new process, which re-borrows acceptEdits and
		// records the real pre-loop mode to hand back when it ends. Failure is
		// reported by ResumeLoop to the session itself (it stops the loop with a
		// reason), so the respawn still succeeded and the caller gets its session.
		if err := sess.ResumeLoop(loopRec); err != nil {
			log.Printf("session %s: respawned but the loop could not continue: %v", id, err)
		}
	}
	return sess, nil
}

// newUUID returns a random RFC 4122 v4 UUID (required by claude --session-id).
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
