package share

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A share link sits on the open internet, so the token is the only thing between
// a stranger and one of your conversations. Everything here is a rule that would
// be a security bug if it quietly stopped holding.

func TestTokenIsLongAndURLSafe(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		tok := NewToken()
		if seen[tok] {
			t.Fatalf("NewToken repeated a value after %d draws: %q", i, tok)
		}
		seen[tok] = true
		// 32 bytes in unpadded base64url.
		if len(tok) != 43 {
			t.Fatalf("token length %d, want 43 (32 bytes): %q", len(tok), tok)
		}
		if strings.ContainsAny(tok, "+/=") {
			t.Fatalf("token is not URL-safe, so it would need escaping in a path: %q", tok)
		}
		// It goes in a URL path segment, so it must survive one untouched.
		if filepath.Base("/s/"+tok) != tok {
			t.Fatalf("token does not survive a path segment: %q", tok)
		}
	}
}

// The owner reads this code off the guest's screen or hears it on a call, so the
// glyphs people misread must not appear in it.
func TestPairCodeIsUnambiguous(t *testing.T) {
	for i := 0; i < 500; i++ {
		code := NewPairCode()
		if len(code) != pairLen {
			t.Fatalf("pair code length %d, want %d: %q", len(code), pairLen, code)
		}
		if strings.ContainsAny(code, "01OIL") {
			t.Fatalf("pair code contains an easily misread glyph: %q", code)
		}
	}
}

// Bash has to be denied at every tier that can prompt. The path guard reads file
// paths out of a tool call, and a shell command has none to read, so allowing it
// would make the confinement decorative rather than real.
func TestNoPromptingTierAllowsBash(t *testing.T) {
	// Exact membership, not substring: "NotebookEdit" contains "Edit", and a
	// substring check here would report the work tier as denying edits when it
	// does not.
	denies := func(tier Tier, tool string) bool {
		for _, d := range tier.DisallowedTools() {
			if d == tool {
				return true
			}
		}
		return false
	}

	for _, tier := range []Tier{TierAsk, TierWork} {
		for _, must := range []string{"Bash", "Task"} {
			if !denies(tier, must) {
				t.Errorf("tier %q does not deny %s (denies %v)", tier, must, tier.DisallowedTools())
			}
		}
	}
	// Ask cannot change anything at all.
	for _, must := range []string{"Edit", "Write", "MultiEdit"} {
		if !denies(TierAsk, must) {
			t.Errorf("the ask tier does not deny %s, so it could change the repository", must)
		}
	}
	// Work is meant to be able to edit, or it is not work.
	if denies(TierWork, "Edit") {
		t.Error("the work tier denies Edit, which leaves a guest unable to do anything")
	}
	// A view share does not restrict the session at all: nothing is sent through it.
	if len(TierView.DisallowedTools()) != 0 {
		t.Error("a view share should not change how the session runs")
	}
}

func TestUnknownTierIsRefusedNotDefaulted(t *testing.T) {
	if Tier("admin").Valid() || Tier("").Valid() {
		t.Fatal("an unknown tier must not validate; defaulting it means guessing how much access to grant")
	}
	if Tier("view").CanPrompt() {
		t.Fatal("a view share must not be able to prompt")
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(filepath.Join(t.TempDir(), "shares.json"))
}

func liveShare(t *testing.T, s *Store, tier Tier) *Share {
	t.Helper()
	sh, err := s.Create(Share{SessionID: "sess-1", Tier: tier, Roots: []string{"/repo"}}, time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return sh
}

// Holding the link is not enough to drive the agent. That is the whole reason a
// public link is survivable: everybody who gets forwarded it stays a viewer.
func TestOnlyThePairedDeviceMayPrompt(t *testing.T) {
	s := newTestStore(t)
	sh := liveShare(t, s, TierWork)
	now := time.Now()

	if sh.MayPrompt("some-device", now) {
		t.Fatal("an unpaired device could prompt using only the link")
	}

	code, err := s.Ask(sh.Token, "device-a", "Sam")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	// Still nothing until the owner approves.
	got, _ := s.Get(sh.Token)
	if got.MayPrompt("device-a", now) {
		t.Fatal("a device could prompt before the owner approved it")
	}
	if _, err := s.Approve(sh.Token, code); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	got, _ = s.Get(sh.Token)
	if !got.MayPrompt("device-a", now) {
		t.Fatal("the approved device still cannot prompt")
	}
	// And nobody else, ever.
	if got.MayPrompt("device-b", now) {
		t.Fatal("a second device could prompt on a share that already has a guest")
	}
	if got.MayPrompt("", now) {
		t.Fatal("an empty device key was accepted as the guest")
	}
}

// A second person following the same link must be told it is spent, not left
// waiting for an approval that would displace the first.
func TestASecondDeviceCannotQueueBehindTheGuest(t *testing.T) {
	s := newTestStore(t)
	sh := liveShare(t, s, TierWork)
	code, _ := s.Ask(sh.Token, "device-a", "")
	if _, err := s.Approve(sh.Token, code); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ask(sh.Token, "device-b", ""); err == nil {
		t.Fatal("a second device was allowed to request pairing on a taken share")
	}
}

func TestViewSharesRefusePairing(t *testing.T) {
	s := newTestStore(t)
	sh := liveShare(t, s, TierView)
	if _, err := s.Ask(sh.Token, "device-a", ""); err == nil {
		t.Fatal("a view-only share accepted a pairing request")
	}
}

// Expiry is a wall, not a hint, and it is checked against the record rather than
// against anything the guest sends.
func TestExpiryEndsEverything(t *testing.T) {
	s := newTestStore(t)
	sh := liveShare(t, s, TierWork)
	code, _ := s.Ask(sh.Token, "device-a", "")
	if _, err := s.Approve(sh.Token, code); err != nil {
		t.Fatal(err)
	}

	// Wind the clock past the share's end.
	s.SetClock(func() time.Time { return time.Unix(sh.ExpiresAt+1, 0) })

	if _, err := s.Get(sh.Token); err != ErrNotFound {
		t.Fatalf("an expired share still resolved: %v", err)
	}
	if err := s.SpendTurn(sh.Token, "device-a"); err == nil {
		t.Fatal("an expired share still let the guest send")
	}
	got := &Share{ExpiresAt: sh.ExpiresAt, Tier: TierWork, Guest: &Guest{Device: "device-a"}}
	if got.MayPrompt("device-a", time.Unix(sh.ExpiresAt+1, 0)) {
		t.Fatal("MayPrompt ignored the expiry")
	}
}

// A missing, expired and revoked share are all one error, so probing cannot tell
// a real token from a wrong one.
func TestNotFoundDoesNotDistinguishWhy(t *testing.T) {
	s := newTestStore(t)
	sh := liveShare(t, s, TierView)
	if _, err := s.Get("never-existed"); err != ErrNotFound {
		t.Fatalf("unknown token gave %v, want ErrNotFound", err)
	}
	if err := s.Revoke(sh.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(sh.Token); err != ErrNotFound {
		t.Fatalf("revoked token gave %v, want the same ErrNotFound", err)
	}
}

// The turn cap is what bounds how much of the owner's quota a guest can spend, so
// the check and the increment have to be one operation.
func TestTurnCapIsSpentExactlyOnce(t *testing.T) {
	s := newTestStore(t)
	sh, err := s.Create(Share{SessionID: "s", Tier: TierWork, MaxTurns: 2}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := s.Ask(sh.Token, "d", "")
	if _, err := s.Approve(sh.Token, code); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := s.SpendTurn(sh.Token, "d"); err != nil {
			t.Fatalf("turn %d refused: %v", i+1, err)
		}
	}
	if err := s.SpendTurn(sh.Token, "d"); err == nil {
		t.Fatal("the guest sent a third turn against a cap of two")
	}
	got, _ := s.Get(sh.Token)
	if left, capped := got.TurnsLeft(); !capped || left != 0 {
		t.Errorf("TurnsLeft = (%d, %v), want (0, true)", left, capped)
	}
}

// Roots are frozen when the share is made. Recomputing them from the live session
// would let an owner widen a guest's boundary by adding a project, without ever
// deciding to.
func TestRootsAreFrozenAtCreation(t *testing.T) {
	s := newTestStore(t)
	roots := []string{"/repo"}
	sh, err := s.Create(Share{SessionID: "s", Tier: TierWork, Roots: roots}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// Mutating the slice the caller passed must not reach into the stored share.
	roots[0] = "/etc"
	got, _ := s.Get(sh.Token)
	if len(got.Roots) != 1 || got.Roots[0] != "/repo" {
		t.Fatalf("the stored roots followed the caller's slice: %v", got.Roots)
	}
}

func TestTTLIsClampedRatherThanRejected(t *testing.T) {
	if got := ClampTTL(time.Second); got != MinTTL {
		t.Errorf("ClampTTL(1s) = %v, want %v", got, MinTTL)
	}
	if got := ClampTTL(365 * 24 * time.Hour); got != MaxTTL {
		t.Errorf("ClampTTL(a year) = %v, want %v", got, MaxTTL)
	}
	if got := ClampTTL(2 * time.Hour); got != 2*time.Hour {
		t.Errorf("ClampTTL mangled a legal value: %v", got)
	}
}

// One share per session, so "is this shared, and with whom" has a single answer.
func TestOneSharePerSession(t *testing.T) {
	s := newTestStore(t)
	liveShare(t, s, TierView)
	if _, err := s.Create(Share{SessionID: "sess-1", Tier: TierWork}, time.Hour); err != ErrNoRoom {
		t.Fatalf("a second share for the same session gave %v, want ErrNoRoom", err)
	}
}

// Ending a session must take its share with it: a link pointing at a dead session
// is at best confusing and at worst points at an id that gets reused.
func TestRevokeSessionDropsTheLink(t *testing.T) {
	s := newTestStore(t)
	sh := liveShare(t, s, TierView)
	tok, had := s.RevokeSession("sess-1")
	if !had || tok != sh.Token {
		t.Fatalf("RevokeSession returned (%q, %v), want the live token", tok, had)
	}
	if _, err := s.Get(sh.Token); err != ErrNotFound {
		t.Fatal("the link still worked after its session was revoked")
	}
	if _, had := s.RevokeSession("sess-1"); had {
		t.Error("revoking twice reported a second share")
	}
}

// Shares outlive a kunai restart, and expired ones do not come back with it.
func TestStoreReloadsLiveSharesAndDropsDeadOnes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shares.json")
	s := NewStore(path)
	live, _ := s.Create(Share{SessionID: "a", Tier: TierView}, time.Hour)
	dead, _ := s.Create(Share{SessionID: "b", Tier: TierView}, time.Hour)
	// Age one of them out by hand, then persist.
	s.mu.Lock()
	s.byToken[dead.Token].ExpiresAt = time.Now().Add(-time.Minute).Unix()
	s.saveLocked()
	s.mu.Unlock()

	again := NewStore(path)
	if _, err := again.Get(live.Token); err != nil {
		t.Errorf("a live share did not survive the restart: %v", err)
	}
	if _, err := again.Get(dead.Token); err != ErrNotFound {
		t.Error("an expired share came back after the restart")
	}
}

// A corrupt file must not stop kunai booting.
func TestCorruptStoreStartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shares.json")
	if err := writeFile(path, "{not json"); err != nil {
		t.Fatal(err)
	}
	if s := NewStore(path); !s.Empty() {
		t.Fatal("a corrupt store came back non-empty")
	}
}

func writeFile(path, body string) error { return os.WriteFile(path, []byte(body), 0o600) }

// A second person opening the link must NOT displace the first person's pending
// request.
//
// The failure this prevents is quiet and bad: Sam reads their code to the owner,
// Eve opens the link a moment later, and with a single overwritable slot the
// owner's approval either fails (if they type Sam's code) or lets EVE in (if
// they tap a button bound to whoever is currently waiting), while believing they
// approved Sam.
func TestASecondAskerCannotDisplaceTheFirst(t *testing.T) {
	s := newTestStore(t)
	sh := liveShare(t, s, TierWork)

	sams, err := s.Ask(sh.Token, "device-sam", "Sam")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ask(sh.Token, "device-eve", "Eve"); err == nil {
		t.Fatal("a second device took over the pending slot")
	}

	// Sam's code still works, and lets in Sam.
	got, err := s.Approve(sh.Token, sams)
	if err != nil {
		t.Fatalf("the first asker's code stopped working: %v", err)
	}
	if got.Guest == nil || got.Guest.Device != "device-sam" {
		t.Fatalf("approved the wrong person: %+v", got.Guest)
	}

	// Asking twice from the same browser is idempotent, not a second request.
	s2 := newTestStore(t)
	sh2 := liveShare(t, s2, TierWork)
	a, _ := s2.Ask(sh2.Token, "device-sam", "Sam")
	b, _ := s2.Ask(sh2.Token, "device-sam", "Sam")
	if a != b {
		t.Errorf("the same device got two different codes: %q then %q", a, b)
	}

	// And denying clears the way for the next person.
	s3 := newTestStore(t)
	sh3 := liveShare(t, s3, TierWork)
	if _, err := s3.Ask(sh3.Token, "device-sam", "Sam"); err != nil {
		t.Fatal(err)
	}
	if err := s3.Deny(sh3.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := s3.Ask(sh3.Token, "device-eve", "Eve"); err != nil {
		t.Fatalf("denying did not free the slot: %v", err)
	}
}

// The fact the server's reconcile loop depends on: once a share is past its
// time, the session it belonged to has no live share, so the tools it withheld
// can be given back.
//
// Nothing else was watching for this. Revoking clears a session's restrictions,
// but a link that simply runs out is swept by whatever next reads the store, and
// the session went on running without Bash and with its guard installed for as
// long as it lived. Silently, because from the outside that is indistinguishable
// from a session nobody ever shared.
func TestAnExpiredShareStopsBelongingToItsSession(t *testing.T) {
	s := newTestStore(t)
	sh := liveShare(t, s, TierWork)

	if _, live := s.BySession("sess-1"); !live {
		t.Fatal("a live share was not found for its session")
	}

	s.SetClock(func() time.Time { return time.Unix(sh.ExpiresAt+1, 0) })

	if _, live := s.BySession("sess-1"); live {
		t.Fatal("an expired share still claims its session, so the session would " +
			"stay restricted forever")
	}
	// And revoking is not the only path: the sweep already dropped it.
	if _, err := s.Get(sh.Token); err != ErrNotFound {
		t.Errorf("the expired share is still resolvable: %v", err)
	}
}

// An expired share must leave the file, not just the map.
//
// sweepLocked deleted from memory on every read path and never wrote, so
// shares.json kept links the running process had already stopped honouring. The
// file is meant to be the record of what this machine is currently exposing, and
// anything reading it to answer that question got a stale yes.
func TestSweepPersistsWhatItDropped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shares.json")
	now := time.Now()

	s := NewStore(path)
	s.SetClock(func() time.Time { return now })
	if _, err := s.Create(Share{SessionID: "a", Tier: TierView}, MinTTL); err != nil {
		t.Fatal(err)
	}

	// Past its expiry, and something reads the store.
	s.SetClock(func() time.Time { return now.Add(MinTTL + time.Second) })
	if !s.Empty() {
		t.Fatal("the expired share still answers")
	}

	// A second store reading the same file must not find it either. Before the
	// fix this reloaded the dead share, and only its own load-time expiry check
	// hid the problem.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var list []*Share
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("shares.json still lists %d expired share(s): %s", len(list), raw)
	}
}
