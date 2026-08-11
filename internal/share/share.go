// Package share is the domain of a shared session: one link, worth exactly one
// conversation and nothing else.
//
// The problem it exists for is that the tailnet is kunai's entire auth perimeter
// (see internal/server/cors.go), so the only way to show somebody a session was to
// put them on the tailnet, which hands them every machine, every session and every
// API on it. There was no smaller unit of access to give away, so nobody gave any.
//
// A share is that smaller unit. It names one session, an expiry, how much of the
// conversation may be seen, and what the person holding it may do. Because the link
// travels over the public internet, holding it is deliberately not enough to drive
// the agent: that takes a pairing step the owner approves, and only one guest per
// share can ever hold it.
//
// This package is pure. It imports nothing from net/http or internal/session, so
// the rules can be tested on their own, and so the decisions live in one place
// rather than being spread through handlers.
package share

import "time"

// Tier is what the person holding the link may do. Deliberately ordered by how
// much they can reach, so a comparison is meaningful.
type Tier string

const (
	// TierView watches the conversation and sends nothing at all.
	TierView Tier = "view"
	// TierAsk prompts the agent, which may read and search but not change anything.
	TierAsk Tier = "ask"
	// TierWork prompts the agent, which may also edit files inside the share's roots.
	TierWork Tier = "work"
)

// Valid reports whether t is a tier this package knows. Anything else is refused
// rather than defaulted, because defaulting an unknown tier means guessing how
// much access to grant.
func (t Tier) Valid() bool {
	return t == TierView || t == TierAsk || t == TierWork
}

// CanPrompt reports whether this tier may send anything to the agent.
func (t Tier) CanPrompt() bool { return t == TierAsk || t == TierWork }

// ToolsOwner is what a share stamps on a session whose tools it withheld, so the
// reconciler that gives them back can tell its own restrictions from anyone
// else's. It exists because a second feature (the pull request reviewer) also
// withholds tools, and the reconciler used to read the restriction itself as
// proof of a share: it respawned a review a minute after it began and killed the
// turn that was running.
const ToolsOwner = "share"

// DisallowedTools is the tool denylist a session runs with while shared at this
// tier, passed to the CLI as --disallowedTools. Verified against claude 2.1.220
// that a denied tool is absent from the session's tool set rather than merely
// gated behind a prompt.
//
// Bash is denied at EVERY tier that can prompt, and that is the load-bearing
// choice. The path guard works by reading file paths out of a tool call, and a
// shell command has no path to read: `cat $(echo ~)/.ssh/id_rsa` is opaque to any
// inspection short of running it. Allowing Bash would leave the confinement
// decorative. Task is denied for the same reason one step removed, since a
// subagent is a way to run tools kunai never sees the request for.
func (t Tier) DisallowedTools() []string {
	switch t {
	case TierAsk:
		// Reading and searching only: no way to change the repository at all.
		return []string{"Bash", "Task", "NotebookEdit", "Edit", "Write", "MultiEdit"}
	case TierWork:
		return []string{"Bash", "Task", "NotebookEdit"}
	default:
		return nil // view sends nothing, so the session runs unchanged
	}
}

// Policy decides how much of a tool call's detail may cross to the guest.
//
// The same reasoning as internal/telegram's policy, and for the same reason: the
// risk being guarded is not really the source, it is the incidental spill. A
// config file the agent read, a token a test echoed, an env dump in a debug
// command. None of that is anything anyone would choose to publish, and a shared
// link is more exposed than a chat, not less.
type Policy struct {
	// ToolInputs sends the arguments a tool was called with, which for an Edit is
	// the contents of your file.
	ToolInputs bool
	// ToolOutputs sends what a tool returned: whole files, command output.
	ToolOutputs bool
}

// StrictPolicy is the default: the conversation, and tool calls by name and shape.
func StrictPolicy() Policy { return Policy{} }

// FullPolicy renders diffs and file contents, so a guest sees what the agent
// actually did rather than that it did something.
func FullPolicy() Policy { return Policy{ToolInputs: true, ToolOutputs: true} }

// Guest is the one person allowed to drive a shared session. One per share: the
// link is public, so everybody else holding it stays a viewer forever, and that
// is the property that makes a public link survivable at all.
type Guest struct {
	// Device is the opaque key the guest's browser generated and holds. It is the
	// credential that proves "the one who was approved", separately from the token,
	// which anybody with the link has.
	Device   string `json:"device"`
	Name     string `json:"name,omitempty"`
	PairedAt int64  `json:"paired_at"`
}

// Pending is an unapproved request to drive the session, with the short code the
// owner reads off the guest's screen and approves.
type Pending struct {
	Code    string `json:"code"`
	Device  string `json:"device"`
	Name    string `json:"name,omitempty"`
	AskedAt int64  `json:"asked_at"`
}

// Share is one shared session.
type Share struct {
	Token     string `json:"token"` // the link; a bearer credential on the open internet
	SessionID string `json:"session_id"`
	Title     string `json:"title,omitempty"` // what the session was called when shared
	Tier      Tier   `json:"tier"`
	// Mode is the standing permission mode applied to tool calls the path guard
	// has already cleared, so a guest working while the owner is asleep is not
	// stuck on the first write. Anything the guard cannot clear still stops for
	// the owner regardless of this.
	Mode   string `json:"mode,omitempty"`
	Detail Policy `json:"detail"`
	// FromSeq is where the guest's view of the conversation begins: 0 shares the
	// whole thing, anything else shares only what happens from the moment the link
	// was made. Enforced against this record and never against a number the guest
	// sends, or the floor is a suggestion.
	FromSeq uint64 `json:"from_seq,omitempty"`
	// Roots are the folders a guest-prompted tool call may touch, FROZEN when the
	// share was made. Never recomputed from the live session: an owner adding a
	// project later would otherwise widen a live share without deciding to.
	Roots     []string `json:"roots,omitempty"`
	ExpiresAt int64    `json:"expires_at"`
	Guest     *Guest   `json:"guest,omitempty"`
	Pending   *Pending `json:"pending,omitempty"`
	Turns     int      `json:"turns"`               // guest turns spent
	MaxTurns  int      `json:"max_turns,omitempty"` // 0 = uncapped
	CreatedAt int64    `json:"created_at"`
}

// Expiry bounds. Five minutes is short enough to hand someone a link for one
// look; five days is long enough to leave a review open over a weekend and short
// enough that a forgotten share dies on its own.
const (
	MinTTL = 5 * time.Minute
	MaxTTL = 5 * 24 * time.Hour
	// PairTTL is how long an unapproved pairing code is good for. Short, because an
	// unapproved code sitting around is a standing invitation to whoever holds it.
	PairTTL = time.Hour
)

// ClampTTL brings a requested lifetime inside the allowed range rather than
// rejecting it. A share is created by someone in the middle of handing a link to
// a person, and a silent tightening beats a validation error they will not read.
func ClampTTL(d time.Duration) time.Duration {
	switch {
	case d < MinTTL:
		return MinTTL
	case d > MaxTTL:
		return MaxTTL
	default:
		return d
	}
}

// Expired reports whether the share is past its lifetime. Checked on every request
// and every websocket frame, not once at connect: a guest holding an open socket
// when the clock runs out has to be disconnected, or the expiry only bounds how
// long they can take to arrive.
func (s *Share) Expired(now time.Time) bool {
	return s.ExpiresAt > 0 && now.Unix() >= s.ExpiresAt
}

// Paired reports whether device is the one guest approved to drive this session.
// A share with no guest is a share nobody may prompt through, whatever its tier.
func (s *Share) Paired(device string) bool {
	return device != "" && s.Guest != nil && s.Guest.Device == device
}

// MayPrompt reports whether this device may send to the agent right now: the tier
// allows it, the device is the paired one, the share is live, and the turn cap is
// not spent.
func (s *Share) MayPrompt(device string, now time.Time) bool {
	if !s.Tier.CanPrompt() || !s.Paired(device) || s.Expired(now) {
		return false
	}
	return s.MaxTurns == 0 || s.Turns < s.MaxTurns
}

// clone returns a copy safe to hand outside the store's lock. A plain struct copy
// is not enough: Roots, Guest and Pending are a slice and two pointers, so the
// copy would still share them and a caller could rewrite a live share's boundary
// or its guest by touching what looks like its own value.
func (s *Share) clone() *Share {
	if s == nil {
		return nil
	}
	cp := *s
	cp.Roots = append([]string(nil), s.Roots...)
	if s.Guest != nil {
		g := *s.Guest
		cp.Guest = &g
	}
	if s.Pending != nil {
		p := *s.Pending
		cp.Pending = &p
	}
	return &cp
}

// TurnsLeft reports how many guest turns remain, and whether there is a cap at
// all. Shown to the guest, so a run that is about to stop says so beforehand.
func (s *Share) TurnsLeft() (left int, capped bool) {
	if s.MaxTurns == 0 {
		return 0, false
	}
	if s.Turns >= s.MaxTurns {
		return 0, true
	}
	return s.MaxTurns - s.Turns, true
}
