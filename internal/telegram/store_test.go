package telegram

import (
	"testing"
	"time"
)

// The enrolment story: a stranger messages the bot, gets a code, and the owner
// approves it in the app. Nobody hunts for a numeric user id.
func TestPairingApprovesAPerson(t *testing.T) {
	s := LoadStore(t.TempDir(), "", nil)

	if s.Allows(555) {
		t.Fatal("a stranger must not be allowed before approval")
	}
	code := s.Ask(555, "Shorya", "shorya")
	if code == "" {
		t.Fatal("asking should produce a code to read out")
	}
	_, _, waiting, _ := s.Snapshot()
	if len(waiting) != 1 || waiting[0].Name != "Shorya" {
		t.Fatalf("the request should name who asked, got %+v", waiting)
	}

	person, ok := s.Approve(code)
	if !ok || person.ID != 555 {
		t.Fatalf("approve = %+v, %v", person, ok)
	}
	if !s.Allows(555) {
		t.Error("an approved person should be allowed")
	}
	if _, _, waiting, _ = s.Snapshot(); len(waiting) != 0 {
		t.Error("an approved request should leave the queue")
	}
}

// Messaging twice while waiting must not fill the owner's screen with codes for
// one impatient person.
func TestAskingTwiceKeepsOneCode(t *testing.T) {
	s := LoadStore(t.TempDir(), "", nil)
	first := s.Ask(7, "A", "")
	second := s.Ask(7, "A", "")
	if first != second {
		t.Errorf("codes %q and %q, want the same one", first, second)
	}
	if _, _, waiting, _ := s.Snapshot(); len(waiting) != 1 {
		t.Errorf("want one pending request, got %d", len(waiting))
	}
}

func TestDenyDropsTheRequestWithoutAccess(t *testing.T) {
	s := LoadStore(t.TempDir(), "", nil)
	code := s.Ask(9, "B", "")
	if !s.Deny(code) {
		t.Fatal("deny should find the request")
	}
	if s.Allows(9) {
		t.Error("a denied person must not be allowed")
	}
	if _, _, waiting, _ := s.Snapshot(); len(waiting) != 0 {
		t.Error("a denied request should leave the queue")
	}
}

// An unapproved code is a standing invitation, so it has to go stale.
func TestPairingRequestsExpire(t *testing.T) {
	s := LoadStore(t.TempDir(), "", nil)
	code := s.Ask(11, "C", "")

	s.mu.Lock()
	s.Waiting[0].AskedAt = time.Now().Add(-pairTTL - time.Minute).Unix()
	s.mu.Unlock()

	if _, _, waiting, _ := s.Snapshot(); len(waiting) != 0 {
		t.Error("an expired request should not be shown")
	}
	if _, ok := s.Approve(code); ok {
		t.Error("an expired code must not be approvable")
	}
	if s.Allows(11) {
		t.Error("expiry must not grant access")
	}
}

func TestApproveRejectsAnUnknownCode(t *testing.T) {
	s := LoadStore(t.TempDir(), "", nil)
	if _, ok := s.Approve("NOPE12"); ok {
		t.Error("an unknown code must not be approved")
	}
}

func TestRevokeRemovesAccess(t *testing.T) {
	s := LoadStore(t.TempDir(), "", []int64{42})
	if !s.Allows(42) {
		t.Fatal("the seeded id should be allowed")
	}
	if !s.Revoke(42) || s.Allows(42) {
		t.Error("revoke should remove access")
	}
}

// The token is set in the app, and the state has to survive a restart or you
// would paste it again every boot.
func TestStoreSurvivesAReload(t *testing.T) {
	dir := t.TempDir()

	s := LoadStore(dir, "", nil)
	s.SetToken("123:ABC")
	s.SetDetail(true)
	code := s.Ask(77, "D", "d")
	s.Approve(code)
	s.bind(-100123, "sess-1")
	s.setOffset(55)

	again := LoadStore(dir, "", nil)
	tok, people, _, detail := again.Snapshot()
	if tok != "123:ABC" {
		t.Errorf("token lost: %q", tok)
	}
	if !detail {
		t.Error("detail setting lost")
	}
	if len(people) != 1 || people[0].ID != 77 {
		t.Errorf("people lost: %+v", people)
	}
	if again.boundTo(-100123) != "sess-1" {
		t.Error("chat binding lost")
	}
	if again.offset() != 55 {
		t.Error("update offset lost")
	}
}

// A new token is a new bot identity, so the old update cursor is meaningless and
// keeping it would skip the new bot's first messages.
func TestChangingTheTokenResetsTheOffset(t *testing.T) {
	s := LoadStore(t.TempDir(), "", nil)
	s.SetToken("one")
	s.setOffset(900)
	if !s.SetToken("two") {
		t.Fatal("a different token should report a change")
	}
	if s.offset() != 0 {
		t.Errorf("offset = %d, want it reset for the new bot", s.offset())
	}
	if s.SetToken("two") {
		t.Error("the same token should not report a change")
	}
}

// Flags seed the store the first time so an existing command line keeps working,
// but must not fight what the app saved afterwards.
func TestFlagsSeedOnlyWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	LoadStore(dir, "flag-token", []int64{1})

	s := LoadStore(dir, "flag-token", []int64{1})
	s.SetToken("set-in-the-app")

	again := LoadStore(dir, "flag-token", []int64{1})
	if tok, _, _, _ := again.Snapshot(); tok != "set-in-the-app" {
		t.Errorf("token = %q, want the app's value to win", tok)
	}
}

// The code is read off one screen and typed nowhere, so it avoids the shapes
// that get misread.
func TestPairCodeAvoidsAmbiguousCharacters(t *testing.T) {
	for i := 0; i < 200; i++ {
		for _, c := range pairCode() {
			switch c {
			case '0', 'O', 'I', '1', 'L':
				t.Fatalf("pair code contains an easily misread character: %q", c)
			}
		}
	}
}
