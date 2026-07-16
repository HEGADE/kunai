package session

// A loop is a self-prompting run: the same task fed back every time a turn ends,
// so the agent keeps working with nobody at the keyboard. The technique is
// Ralph's (ghuntley.com/ralph). The prompt never changes; progress lives in the
// files, the tests, and the git history rather than in the conversation, so each
// iteration starts by reading what the last one actually did.
//
// Claude Code implements this with a Stop hook that blocks exit and re-feeds the
// prompt, because a hook is the only seam it has. Kunai drives the CLI itself and
// already knows exactly when a turn ends, so the loop lives here instead: in the
// session, like the prompt queue, and for the same reason. The whole point is
// that nobody is attached. The phone can be asleep.
//
// The hard part is not the looping. It is the stopping: an unattended loop spends
// real money on someone else's schedule. Every exit below is therefore a limit
// the loop cannot talk itself past, and the two the user sets (iterations and
// spend) are both hard. Whichever comes first wins.

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hegade/kunai/internal/claude"
)

// Loop states, as a client sees them.
const (
	LoopRunning   = "running"
	LoopDone      = "done"      // the model reported the task complete
	LoopStopped   = "stopped"   // you stopped it, or the usage window did
	LoopExhausted = "exhausted" // a limit ran out
	LoopFailed    = "failed"    // the turn itself broke

	// LoopSeam marks an iteration recovered from a transcript rather than one
	// happening now. It renders as the same seam in the log but must never light
	// up the live bar: the loop it belonged to ended before the process restarted.
	LoopSeam = "seam"
)

// Bounds. A loop that cannot exhaust itself is a bug, not a feature: it runs
// while the person who started it is asleep. The defaults are deliberately timid
// (a handful of iterations, a couple of dollars) because the failure mode of too
// small is "it stopped early and told you why", and the failure mode of too big
// is a drained account discovered in the morning.
const (
	loopHardIters    = 200
	loopHardUSD      = 50.0
	loopDefaultIters = 10
	loopDefaultUSD   = 2.0
)

// loopCooldown is the pause between iterations. Never hot-spin: if a turn ends
// instantly (a CLI that errors on contact), this is what keeps the loop from
// becoming a fork bomb with a credit card. A var only so tests need not sleep.
var loopCooldown = 2 * time.Second

// LoopConfig starts a loop.
type LoopConfig struct {
	Prompt   string  `json:"prompt"`
	Promise  string  `json:"promise,omitempty"`
	MaxIters int     `json:"max_iters"`
	MaxUSD   float64 `json:"max_usd"`
}

// LoopStatus is the whole loop as a client sees it: enough to render the card,
// the live bar, and the reason it ended, with no client-side bookkeeping.
type LoopStatus struct {
	State     string  `json:"state"`
	Prompt    string  `json:"prompt"`
	Promise   string  `json:"promise,omitempty"`
	Iteration int     `json:"iteration"`
	MaxIters  int     `json:"max_iters"`
	SpentUSD  float64 `json:"spent_usd"`
	MaxUSD    float64 `json:"max_usd"`
	Reason    string  `json:"reason,omitempty"`
}

// LoopPermissionMode is what a loop runs in, whatever the session was using.
// Auto still stops to ask about a risky action, and with nobody watching that is
// not caution, it is a hang: the first iteration sits at awaiting_permission
// until morning and the loop does nothing all night. The scheduler reached the
// same conclusion for the same reason (see fireJob), and this is the same case.
// The session's own mode is handed back when the loop ends.
const LoopPermissionMode = "acceptEdits"

// loopRun is the live state behind a LoopStatus.
type loopRun struct {
	cfg       LoopConfig
	iteration int
	startCost float64 // session cost total when the loop began; spend is the delta
	prevMode  string  // the session's mode before the loop borrowed it
	state     string
	reason    string
	resumes   int // times this loop has been resumed after a restart (crash-loop guard)
}

// LoopPersist is a running loop written to disk so it survives a restart (an
// auto-update, a crash, an OOM, the service manager bouncing us). It carries
// everything needed to recreate the session with --resume and pick the loop up
// where it left off. It exists on disk ONLY while the loop is running: any ending,
// including a thermal trip, deletes it, so a machine that stopped a loop for a
// reason never silently restarts it. See internal/server/looppersist.go.
type LoopPersist struct {
	SessionID string     `json:"session_id"` // the --resume handle (== Session.ID) and file key
	Cwd       string     `json:"cwd"`
	Model     string     `json:"model"`
	Effort    string     `json:"effort"`
	Config    LoopConfig `json:"config"`
	Iteration int        `json:"iteration"`
	SpentUSD  float64    `json:"spent_usd"`
	Resumes   int        `json:"resumes"`
	State     string     `json:"state"` // the server writes while "running", deletes otherwise
}

// StartLoop begins a self-prompting run. The config is clamped rather than
// rejected: a loop is started by someone about to walk away, and a silent
// tightening beats a validation error they never read.
func (s *Session) StartLoop(cfg LoopConfig) error {
	cfg.Prompt = strings.TrimSpace(cfg.Prompt)
	if cfg.Prompt == "" {
		return errors.New("loop: a task is required")
	}
	cfg.Promise = strings.TrimSpace(cfg.Promise)
	cfg.MaxIters = clampIters(cfg.MaxIters)
	cfg.MaxUSD = clampUSD(cfg.MaxUSD)

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("loop: session is closed")
	}
	if s.loop != nil && s.loop.state == LoopRunning {
		s.mu.Unlock()
		return errors.New("loop: one is already running")
	}
	// Spend is measured against the session total as it stands now, so a loop
	// started in a long conversation is billed for its own turns and not the ones
	// that came before it.
	s.loop = &loopRun{cfg: cfg, startCost: s.lastCostUSD, state: LoopRunning, prevMode: s.mode}
	s.emitLocked(s.sequenceLocked(s.loopEventLocked()))
	s.persistLoopLocked()
	s.mu.Unlock()

	// Borrow the autonomous mode for the duration, or the first file edit ends the
	// run in all but name.
	if err := s.SetPermissionMode(LoopPermissionMode); err != nil {
		s.mu.Lock()
		s.stopLoopLocked(LoopFailed, "the permission mode could not be set: "+err.Error())
		s.mu.Unlock()
		return err
	}
	s.advanceLoop()
	return nil
}

// ResumeLoop picks a loop back up on a freshly recreated session after a restart.
// It is StartLoop's twin: same borrowed mode, same pump, but it continues from the
// saved iteration and spend instead of starting over.
//
// The budget baseline is the subtle part. A resumed CLI process starts its cost
// count from zero (--resume loads context, not the prior bill, verified against a
// real CLI), so setting startCost to the negative of what was already spent makes
// the running spend pick up exactly where it left off: spend = lastCostUSD -
// startCost = thisProcessCost + priorSpent. The iteration cap carries over exactly
// because it is a plain integer, so even if the money math ever drifted the loop
// still cannot outrun its hard bound.
func (s *Session) ResumeLoop(rec LoopPersist) error {
	cfg := rec.Config
	cfg.MaxIters = clampIters(cfg.MaxIters)
	cfg.MaxUSD = clampUSD(cfg.MaxUSD)

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("loop: session is closed")
	}
	if s.loop != nil && s.loop.state == LoopRunning {
		s.mu.Unlock()
		return errors.New("loop: one is already running")
	}
	s.loop = &loopRun{
		cfg:       cfg,
		iteration: rec.Iteration,
		startCost: -rec.SpentUSD,
		state:     LoopRunning,
		prevMode:  s.mode,
		resumes:   rec.Resumes + 1,
	}
	s.emitLocked(s.sequenceLocked(s.loopEventLocked()))
	s.persistLoopLocked() // rewrite with the bumped resume count
	s.mu.Unlock()

	if err := s.SetPermissionMode(LoopPermissionMode); err != nil {
		s.mu.Lock()
		s.stopLoopLocked(LoopFailed, "the permission mode could not be set: "+err.Error())
		s.mu.Unlock()
		return err
	}
	s.advanceLoop()
	return nil
}

// StopLoop ends a loop by hand. Safe to call when none is running.
func (s *Session) StopLoop(reason string) {
	s.mu.Lock()
	s.stopLoopLocked(LoopStopped, reason)
	s.mu.Unlock()
}

// stopLoopLocked settles a running loop and tells everyone why.
func (s *Session) stopLoopLocked(state, reason string) {
	if s.loop == nil || s.loop.state != LoopRunning {
		return
	}
	s.loop.state = state
	s.loop.reason = reason
	s.emitLocked(s.sequenceLocked(s.loopEventLocked()))
	// The state is now terminal, so this deletes the durable record: a loop that
	// ended for any reason, thermal included, must never be resumed on the next
	// boot. This runs before the guardian's poweroff, so the delete wins the race.
	s.persistLoopLocked()

	// Hand the session's mode back, unless you changed it yourself while the loop
	// ran, in which case your choice stands. The driver call needs the lock this
	// is holding, so it goes out on its own goroutine.
	if prev := s.loop.prevMode; prev != "" && prev != LoopPermissionMode && s.mode == LoopPermissionMode {
		go func() { _ = s.SetPermissionMode(prev) }()
	}
}

// loopStatusLocked snapshots the loop for a client (nil when there never was one).
func (s *Session) loopStatusLocked() *LoopStatus {
	l := s.loop
	if l == nil {
		return nil
	}
	return &LoopStatus{
		State:     l.state,
		Prompt:    l.cfg.Prompt,
		Promise:   l.cfg.Promise,
		Iteration: l.iteration,
		MaxIters:  l.cfg.MaxIters,
		SpentUSD:  s.lastCostUSD - l.startCost,
		MaxUSD:    l.cfg.MaxUSD,
		Reason:    l.reason,
	}
}

func (s *Session) loopEventLocked() AppEvent {
	return AppEvent{T: EvLoop, Loop: s.loopStatusLocked()}
}

// persistLoopLocked hands the loop's durable state to the persister (set by the
// server). The record carries its own state, so the server writes it while the
// loop runs and deletes it once it ends. A no-op when no persister is registered
// (every unit test, and any build without a data dir).
func (s *Session) persistLoopLocked() {
	if s.loopPersist == nil || s.loop == nil {
		return
	}
	l := s.loop
	s.loopPersist(LoopPersist{
		SessionID: s.ID,
		Cwd:       s.Cwd,
		Model:     s.model,
		Effort:    s.effort,
		Config:    l.cfg,
		Iteration: l.iteration,
		SpentUSD:  s.lastCostUSD - l.startCost,
		Resumes:   l.resumes,
		State:     l.state,
	})
}

// SetLoopPersister registers where a running loop is saved so it can survive a
// restart. Called on every session by the server.
func (s *Session) SetLoopPersister(fn func(LoopPersist)) {
	s.mu.Lock()
	s.loopPersist = fn
	s.mu.Unlock()
}

// advanceLoop runs the next iteration, if the loop is still entitled to one.
func (s *Session) advanceLoop() {
	s.mu.Lock()
	l := s.loop
	if s.closed || l == nil || l.state != LoopRunning {
		s.mu.Unlock()
		return
	}
	// Anything a person actually typed outranks the loop; it picks up when that
	// turn ends. Waiting on a permission ask counts as busy, which is what keeps
	// an unattended loop from stampeding past a question it should have stopped
	// at. These are exactly the states prompt() queues behind, and deliberately
	// so: a session that is still booting is fine to start on, because the driver
	// buffers the turn until the CLI is up. Requiring idle here instead stalled a
	// loop started on a fresh session forever, since nothing else would wake it.
	if s.state == StateRunning || s.state == StateAwaiting || len(s.queue) > 0 {
		s.mu.Unlock()
		return
	}

	l.iteration++
	q := &queuedPrompt{
		Text:   LoopPrompt(l.cfg, l.iteration),
		silent: true, // the loop card is the conversation's record of this
		label:  fmt.Sprintf("Loop #%d", l.iteration),
	}
	s.emitLocked(s.sequenceLocked(s.loopEventLocked()))
	s.persistLoopLocked() // save the new iteration so a crash resumes from here
	s.startTurnLocked(q)
	s.mu.Unlock()

	if err := s.deliver(q.Text, nil); err != nil {
		s.mu.Lock()
		s.stopLoopLocked(LoopFailed, "the iteration could not be sent: "+err.Error())
		s.mu.Unlock()
		s.setState(StateIdle)
	}
}

// afterTurn decides, once a turn has ended, whether the loop gets another one.
// Called with the turn's cost already folded into lastCostUSD.
func (s *Session) afterTurn(turnFailed bool) {
	s.mu.Lock()
	l := s.loop
	if s.closed || l == nil || l.state != LoopRunning {
		s.mu.Unlock()
		return
	}
	spent := s.lastCostUSD - l.startCost

	// Order matters. The model reporting completion is a success even if this
	// turn also happened to exhaust something, so it is asked first.
	var state, reason string
	switch {
	case l.cfg.Promise != "" && saidPromise(s.lastText, l.cfg.Promise):
		state, reason = LoopDone, "Claude reported the task complete"
	case turnFailed:
		state, reason = LoopFailed, "the turn ended in an error"
	case s.rateLimited:
		state, reason = LoopStopped, "the usage limit was reached"
	case l.cfg.MaxUSD > 0 && spent >= l.cfg.MaxUSD:
		state, reason = LoopExhausted, fmt.Sprintf("the $%.2f budget ran out", l.cfg.MaxUSD)
	case l.iteration >= l.cfg.MaxIters:
		state, reason = LoopExhausted, fmt.Sprintf("all %d iterations ran", l.cfg.MaxIters)
	}
	if state != "" {
		s.stopLoopLocked(state, reason)
		s.mu.Unlock()
		s.notifyAttention("loop", reason)
		return
	}
	s.mu.Unlock()

	// Breathe, then go again. AfterFunc rather than a sleep so nothing holds the
	// pump: advanceLoop re-checks every condition when it fires.
	time.AfterFunc(loopCooldown, s.advanceLoop)
}

// LoopPrompt is what the model actually reads each time round.
//
// It says which iteration this is, because otherwise a model that sees the same
// text twice concludes it failed and starts over. It points at the files rather
// than the conversation, because the files are where the last iteration's work
// survives. And it spells out that claiming completion falsely is the one way to
// break the loop, because that is exactly the shortcut a model reaches for when
// the task is hard.
//
// The whole thing is wrapped in a tag because a loop iteration is a harness
// wrapper, not something a person typed, and it has to be readable as one from
// the other end: the CLI writes every turn we send into the transcript, and
// resuming reads that file back. Without the wrapper, reopening a fifty-iteration
// loop replayed fifty copies of these instructions as user messages and buried
// the work they produced. ParseLoopIteration is the other half of this contract.
func LoopPrompt(cfg LoopConfig, n int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<loop-iteration n=%q of=%q>\n", strconv.Itoa(n), strconv.Itoa(cfg.MaxIters))
	b.WriteString(cfg.Prompt)
	b.WriteString("\n\n---\n")
	b.WriteString("You are running in a loop with nobody watching. You are reading this again because the last turn ended, not because anything went wrong. ")
	b.WriteString("Before doing anything, check what earlier iterations already did: read the files, run the tests, look at the git history. Continue that work rather than starting over.")
	if cfg.Promise != "" {
		fmt.Fprintf(&b, " When the task is genuinely and verifiably finished, make <promise>%s</promise> the last thing in your reply. That ends the loop, so only say it when it is true: do not say it to get out early.", cfg.Promise)
	}
	b.WriteString("\n</loop-iteration>")
	return b.String()
}

// loopIterRe reads back the opening tag LoopPrompt writes.
var loopIterRe = regexp.MustCompile(`^<loop-iteration n="(\d+)" of="(\d+)"`)

// ParseLoopIteration recognises a loop's own turn in a transcript and says which
// iteration it was. This is how a resumed session shows the seams between
// iterations instead of either replaying the instructions or, worse, showing the
// work with nothing to explain why it happened.
func ParseLoopIteration(text string) (n, of int, ok bool) {
	m := loopIterRe.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return 0, 0, false
	}
	n, _ = strconv.Atoi(m[1])
	of, _ = strconv.Atoi(m[2])
	return n, of, true
}

// assistantText joins a message's text blocks, ignoring tool calls and thinking.
// It is what the model said out loud, which is where a promise has to appear.
func assistantText(m *claude.AssistantMessage) string {
	if m == nil {
		return ""
	}
	var b strings.Builder
	for _, blk := range m.Content {
		if blk.Type == "text" && blk.Text != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}

// promiseRe pulls the contents of the first <promise> tag, across newlines.
var promiseRe = regexp.MustCompile(`(?is)<promise>(.*?)</promise>`)

// saidPromise reports whether the model closed with the agreed phrase. Matching
// is lenient (case and inner whitespace) on purpose: a false positive stops the
// loop early and says so, while a miss keeps spending money.
func saidPromise(text, promise string) bool {
	m := promiseRe.FindStringSubmatch(text)
	if m == nil {
		return false
	}
	return normalizePromise(m[1]) == normalizePromise(promise)
}

func normalizePromise(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

func clampIters(n int) int {
	if n <= 0 {
		return loopDefaultIters
	}
	if n > loopHardIters {
		return loopHardIters
	}
	return n
}

func clampUSD(v float64) float64 {
	if v <= 0 {
		return loopDefaultUSD
	}
	if v > loopHardUSD {
		return loopHardUSD
	}
	return v
}
