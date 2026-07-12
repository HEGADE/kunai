package session

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"

	"github.com/hegade/kunai/internal/claude"
)

// Manager owns all live sessions. It is safe for concurrent use.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

func NewManager() *Manager {
	return &Manager{sessions: make(map[string]*Session)}
}

// CreateOptions configure a new session.
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

	drvOpts := claude.Options{Cwd: opts.Cwd, Model: opts.Model, Effort: opts.Effort}
	if opts.Resume != "" {
		drvOpts.Resume = opts.Resume
	} else {
		drvOpts.SessionID = id
	}
	drv := claude.NewSession(drvOpts)

	s := newSession(id, opts.Cwd, opts.Title, drv)
	s.model = opts.Model
	s.effort = opts.Effort
	if len(opts.Seed) > 0 {
		s.Seed(opts.Seed)
	}

	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()

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
	m.mu.Lock()
	all := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		all = append(all, s)
	}
	m.mu.Unlock()
	for _, s := range all {
		s.Close()
	}
}

func (m *Manager) remove(id string) {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
}

// removeIf deletes id only if it still maps to this exact session, so a stale
// reap goroutine cannot evict a freshly re-created session with the same id.
func (m *Manager) removeIf(id string, s *Session) {
	m.mu.Lock()
	if m.sessions[id] == s {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
}

// RestartWithEffort relaunches a live session at a new reasoning effort by
// closing it and re-creating it with --resume (effort is a spawn-time CLI flag,
// so it cannot change on the running process). The conversation is preserved via
// the transcript: seedFn loads it back into the replay buffer. The new session
// keeps the same id (resume forces id == claude session id).
func (m *Manager) RestartWithEffort(ctx context.Context, id, effort string, seedFn func(cid string) []SeedTurn) (*Session, error) {
	m.mu.Lock()
	old, ok := m.sessions[id]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("session %s not live", id)
	}
	cid := old.ClaudeSessionID()
	meta := old.Meta()

	old.Close()
	<-old.Done()
	m.removeIf(id, old) // synchronous, so the recreate below won't collide

	// Effort is a spawn-time flag, so the process must be respawned either way.
	// Three cases, distinguished by whether the CLI has assigned a session id
	// (only after the first turn) and whether a transcript exists on disk:
	//   - prompted this run: resume by the live CLI session id.
	//   - resumed from history but not yet prompted: no live id yet, but a
	//     transcript exists under the handle id, so resume that (a fresh
	//     --session-id would collide with the existing transcript and the CLI
	//     refuses to start).
	//   - brand-new session, no turns and no transcript: respawn fresh under the
	//     same handle id.
	opts := CreateOptions{Cwd: meta.Cwd, Title: meta.Title, Model: meta.Model, Effort: effort}
	if cid != "" {
		opts.Resume, opts.Seed = cid, seedFn(cid)
	} else if seed := seedFn(id); len(seed) > 0 {
		opts.Resume, opts.Seed = id, seed
	} else {
		opts.SessionID = id
	}
	return m.Create(ctx, opts)
}

// newUUID returns a random RFC 4122 v4 UUID (required by claude --session-id).
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
