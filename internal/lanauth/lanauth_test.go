package lanauth

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestValidatePINRefusesTheGuessesTriedFirst(t *testing.T) {
	for _, bad := range []string{"12345", "1234567890123", "12345a", "abcdef", ""} {
		if err := ValidatePIN(bad); err == nil {
			t.Errorf("ValidatePIN(%q) accepted a malformed PIN", bad)
		}
	}
	// Six digits is a million combinations only if the choice is spread over them.
	// These are not, so they are refused at the point of SETTING one.
	for _, weak := range []string{"000000", "123456", "654321", "111111", "121212", "1234567", "987654"} {
		if err := ValidatePIN(weak); !errors.Is(err, ErrPINWeak) {
			t.Errorf("ValidatePIN(%q) = %v, want it refused as weak", weak, err)
		}
	}
	for _, ok := range []string{"481920", "739104", "6183940275"} {
		if err := ValidatePIN(ok); err != nil {
			t.Errorf("ValidatePIN(%q) = %v, want accepted", ok, err)
		}
	}
}

func TestHashPINNeverStoresThePIN(t *testing.T) {
	h, err := HashPIN("481920")
	if err != nil {
		t.Fatal(err)
	}
	if !h.Verify("481920") {
		t.Error("the right PIN did not verify")
	}
	if h.Verify("481921") {
		t.Error("a wrong PIN verified")
	}
	if string(h.Key) == "481920" || string(h.Salt) == "481920" {
		t.Error("the PIN is recoverable from what was stored")
	}
	// A fresh salt each time, so two people with the same PIN do not share a hash
	// and one cracked hash does not reveal another.
	h2, _ := HashPIN("481920")
	if string(h.Salt) == string(h2.Salt) || string(h.Key) == string(h2.Key) {
		t.Error("two hashes of the same PIN are identical; the salt is not random")
	}
	// An unset hash must never accept anything, including the empty string.
	var zero Hashed
	if zero.Set() || zero.Verify("") || zero.Verify("481920") {
		t.Error("a zero-value hash accepted a PIN")
	}
}

// The load-bearing test. Everything else here is hygiene; this is the property
// that makes a six-digit PIN defensible at all.
func TestGuessingIsBoundedEvenWhenTheSourceChanges(t *testing.T) {
	var th Throttle
	base := time.Now()

	// An attacker rotating source addresses defeats per-source limits entirely, so
	// the global counter has to be the one that stops them. Give every guess a
	// brand new address and count how far they get.
	guesses := 0
	for i := 0; i < 500; i++ {
		src := "10.0.0." + itoa(i%250) + ":" + itoa(i)
		if th.RetryAfter(base, src) > 0 {
			continue // locked out despite the fresh address
		}
		guesses++
		th.Fail(base, src)
	}
	if guesses > DefaultGlobal.FreeAttempts+1 {
		t.Fatalf("an attacker changing address every time got %d guesses; the global limit is not holding", guesses)
	}

	// And the lock is not permanent: it lifts, so an owner is not locked out for
	// good by somebody else's attempt.
	later := base.Add(DefaultGlobal.MaxLock + time.Second)
	if wait := th.RetryAfter(later, "10.0.0.1:1"); wait != 0 {
		t.Errorf("still locked %v after the maximum lock expired", wait)
	}
}

func TestThrottleForgivesAnOwnerAndPunishesAnAttacker(t *testing.T) {
	var th Throttle
	now := time.Now()
	const src = "192.168.0.9:5000"

	// Mistyping your own PIN a few times must not lock anything: the allowance is
	// spent by FreeAttempts failures, and the NEXT one is what locks.
	for i := 0; i < DefaultPerSource.FreeAttempts; i++ {
		if wait := th.RetryAfter(now, src); wait != 0 {
			t.Fatalf("locked after %d failures, before the free allowance was used", i)
		}
		th.Fail(now, src)
	}
	if wait := th.RetryAfter(now, src); wait != 0 {
		t.Fatalf("locked after exactly the free allowance (%d), which should still be forgiven", DefaultPerSource.FreeAttempts)
	}
	th.Fail(now, src)
	if wait := th.RetryAfter(now, src); wait == 0 {
		t.Fatal("no lock once the free attempts were exceeded")
	}

	// Each further failure lengthens it.
	first := th.RetryAfter(now, src)
	th.Fail(now, src)
	if second := th.RetryAfter(now, src); second <= first {
		t.Errorf("lock did not escalate: %v then %v", first, second)
	}

	// A quiet spell forgives, so tomorrow's first attempt is not still punished.
	fresh := now.Add(DefaultPerSource.Decay + DefaultPerSource.MaxLock + time.Minute)
	if wait := th.RetryAfter(fresh, src); wait != 0 {
		t.Errorf("still locked %v after a long quiet period", wait)
	}
}

// A map keyed by something the attacker picks is a way to exhaust memory rather
// than to guess, so it has to be bounded.
func TestThrottleTableCannotBeGrownWithoutLimit(t *testing.T) {
	var th Throttle
	now := time.Now()
	for i := 0; i < maxSources*4; i++ {
		th.Fail(now, "10.1."+itoa(i/250)+"."+itoa(i%250))
	}
	if len(th.Sources) > maxSources {
		t.Errorf("per-source table grew to %d, past the %d cap", len(th.Sources), maxSources)
	}
}

// A restart must not hand back a fresh guessing budget, and must not sign out the
// devices already admitted.
func TestLocksAndSessionsSurviveARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lanauth.json")
	now := time.Now()

	s := Open(path)
	s.SetClock(func() time.Time { return now })
	if err := s.SetPIN("481920"); err != nil {
		t.Fatal(err)
	}
	token, err := s.Login("481920", "192.168.0.9", "tablet")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < DefaultPerSource.FreeAttempts+2; i++ {
		_, _ = s.Login("000001", "192.168.0.50", "")
	}
	if s.LockedFor("192.168.0.50") == 0 {
		t.Fatal("the attacker was not locked out before the restart")
	}

	// Reopen: a new process, the same files.
	again := Open(path)
	again.SetClock(func() time.Time { return now })
	if again.LockedFor("192.168.0.50") == 0 {
		t.Error("restarting cleared the lockout, so an attacker gets a fresh budget by waiting for an update")
	}
	if !again.Valid(token) {
		t.Error("restarting signed out an already-admitted device")
	}
	if !again.HasPIN() {
		t.Error("restarting lost the PIN")
	}
}

func TestChangingThePINSignsEveryDeviceOut(t *testing.T) {
	s := Open(filepath.Join(t.TempDir(), "lanauth.json"))
	if err := s.SetPIN("481920"); err != nil {
		t.Fatal(err)
	}
	token, err := s.Login("481920", "192.168.0.9", "tablet")
	if err != nil {
		t.Fatal(err)
	}
	if !s.Valid(token) {
		t.Fatal("a fresh token was not valid")
	}
	// You change the PIN because you think somebody else has it. A session that
	// survived that would make the change pointless.
	if err := s.SetPIN("739104"); err != nil {
		t.Fatal(err)
	}
	if s.Valid(token) {
		t.Error("a device stayed signed in across a PIN change")
	}
}

func TestTokensAreNotStoredInAUsableForm(t *testing.T) {
	now := time.Now()
	token, sess, err := NewSession(now, "tablet")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Hash == token {
		t.Fatal("the session stored the token itself, so the file is a list of live credentials")
	}
	if !sess.MatchesToken(token) {
		t.Error("the stored hash does not match its own token")
	}
	if sess.MatchesToken(token + "x") {
		t.Error("a wrong token matched")
	}
	// Two mints never collide, or one device's token would admit another.
	other, _, _ := NewSession(now, "")
	if other == token {
		t.Error("two freshly minted tokens were identical")
	}
	// Unused for longer than the TTL, it stops working.
	if !sess.Expired(now.Add(SessionTTL + time.Second)) {
		t.Error("a session outlived its TTL")
	}
}

func TestLoginWithNoPINSetIsRefusedAndCounted(t *testing.T) {
	s := Open(filepath.Join(t.TempDir(), "lanauth.json"))
	if _, err := s.Login("481920", "192.168.0.9", ""); !errors.Is(err, ErrNoPIN) {
		t.Errorf("err = %v, want ErrNoPIN", err)
	}
	// Counted, so an unarmed lock cannot be used as a free oracle either.
	for i := 0; i < DefaultPerSource.FreeAttempts+2; i++ {
		_, _ = s.Login("481920", "192.168.0.9", "")
	}
	if s.LockedFor("192.168.0.9") == 0 {
		t.Error("attempts against an unset PIN were not throttled")
	}
}

// Deliberately not strconv, to keep the test's helpers obvious.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
