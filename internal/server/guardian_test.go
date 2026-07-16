package server

import (
	"errors"
	"testing"
	"time"
)

var errPoweroffDenied = errors.New("poweroff denied")

// The guardian is a safety net that fires while nobody is watching, so every way
// it trips and every way it must NOT trip has to be proven, not assumed.

// fakeStopper counts how many times the guardian pulled the emergency stop.
type fakeStopper struct {
	calls    int
	sessions int
}

func (f *fakeStopper) StopForThermal() int {
	f.calls++
	return f.sessions
}

// fakeAwake is a keep-awake hold the test can drive.
type fakeAwake struct {
	on        bool
	supported bool
	released  int
}

func (a *fakeAwake) Set(on bool) error {
	if a.on && !on {
		a.released++
	}
	a.on = on
	return nil
}
func (a *fakeAwake) Enabled() bool   { return a.on }
func (a *fakeAwake) Supported() bool { return a.supported }

func newTestGuardian(cfg guardConfig) (*guardian, *fakeStopper, *fakeAwake) {
	stop := &fakeStopper{sessions: 2}
	aw := &fakeAwake{supported: true}
	g := newGuardian(stop, aw, cfg)
	return g, stop, aw
}

// A single hot read must not trip: hysteresis is what keeps a one-off spike from
// killing a session. It takes guardTripN sustained reads.
func TestGuardianNeedsSustainedHeat(t *testing.T) {
	g, stop, _ := newTestGuardian(guardConfig{Enabled: true, SoftC: 90})

	for i := 0; i < guardTripN-1; i++ {
		g.check(95)
		if stop.calls != 0 {
			t.Fatalf("tripped after %d hot reads, want %d", i+1, guardTripN)
		}
	}
	// One cool read resets the streak.
	g.check(40)
	if g.tripped() {
		t.Fatal("a cool read did not clear the streak")
	}
	// Now a fresh run of hot reads must reach the full count again.
	for i := 0; i < guardTripN-1; i++ {
		g.check(95)
	}
	if stop.calls != 0 {
		t.Fatal("tripped early: the cool read should have reset the streak")
	}
	g.check(95)
	if stop.calls != 1 || !g.tripped() {
		t.Fatalf("did not trip after %d sustained hot reads", guardTripN)
	}
}

// On a trip it stops the sessions AND releases the keep-awake hold, because on a
// closed-lid machine dropping the hold is what lets it sleep and cool.
func TestGuardianTripStopsAndReleasesHold(t *testing.T) {
	g, stop, aw := newTestGuardian(guardConfig{Enabled: true, SoftC: 90})
	aw.on = true

	var notified string
	g.notify = func(kind, detail string) { notified = kind + ":" + detail }

	for i := 0; i < guardTripN; i++ {
		g.check(99)
	}
	if stop.calls != 1 {
		t.Fatalf("stop calls = %d, want 1", stop.calls)
	}
	if aw.on || aw.released != 1 {
		t.Fatalf("keep-awake not released: on=%v released=%d", aw.on, aw.released)
	}
	if notified != "thermal:host too hot" {
		t.Fatalf("notify = %q, want thermal:host too hot", notified)
	}
}

// A trip also drops the lid-closed hold, so a Mac held awake with the lid shut is
// free to sleep and cool.
func TestGuardianTripReleasesLidHold(t *testing.T) {
	g, _, _ := newTestGuardian(guardConfig{Enabled: true, SoftC: 90})
	lidReleased := 0
	g.releaseLid = func() { lidReleased++ }

	for i := 0; i < guardTripN; i++ {
		g.check(99)
	}
	if lidReleased != 1 {
		t.Fatalf("lid released %d times, want 1", lidReleased)
	}
}

// Once tripped it stays tripped through more hot reads (it must not stop over and
// over) and only re-arms once the machine has cooled well below the line.
func TestGuardianLatchesAndReArmsOnlyWhenCool(t *testing.T) {
	g, stop, _ := newTestGuardian(guardConfig{Enabled: true, SoftC: 90})
	for i := 0; i < guardTripN; i++ {
		g.check(99)
	}
	if stop.calls != 1 {
		t.Fatalf("stop calls = %d, want 1", stop.calls)
	}
	// Still hot, and just below the line: stays latched, no repeat stop.
	g.check(99)
	g.check(87) // within guardRecoverC of 90, not cool enough
	if !g.tripped() || stop.calls != 1 {
		t.Fatalf("re-armed too early or stopped again: tripped=%v calls=%d", g.tripped(), stop.calls)
	}
	// Cool at last.
	g.check(80)
	if g.tripped() {
		t.Fatal("did not re-arm after cooling well below the line")
	}
	// And it can trip a second time.
	for i := 0; i < guardTripN; i++ {
		g.check(99)
	}
	if stop.calls != 2 {
		t.Fatalf("second trip stop calls = %d, want 2", stop.calls)
	}
}

// A disabled guard does nothing, however hot it gets: it is opt-in.
func TestGuardianDisabledNeverTrips(t *testing.T) {
	g, stop, _ := newTestGuardian(guardConfig{Enabled: false, SoftC: 90})
	for i := 0; i < guardTripN*3; i++ {
		g.check(120)
	}
	if stop.calls != 0 || g.tripped() {
		t.Fatalf("a disabled guard acted: calls=%d tripped=%v", stop.calls, g.tripped())
	}
}

// With no temperature reading (macOS today, temp 0), the guard never trips on
// heat — only the wall-clock cap can fire there.
func TestGuardianIgnoresZeroTemperature(t *testing.T) {
	g, stop, _ := newTestGuardian(guardConfig{Enabled: true, SoftC: 90})
	for i := 0; i < guardTripN*2; i++ {
		g.check(0)
	}
	if stop.calls != 0 {
		t.Fatalf("tripped on an unreadable (0) temperature: calls=%d", stop.calls)
	}
}

// SoftC of 0 means "no temperature check" even with real readings: the guard is
// then purely a time cap.
func TestGuardianSoftZeroDisablesTempCheck(t *testing.T) {
	g, stop, _ := newTestGuardian(guardConfig{Enabled: true, SoftC: 0, MaxHours: 1})
	for i := 0; i < guardTripN*2; i++ {
		g.check(99)
	}
	if stop.calls != 0 {
		t.Fatalf("tripped on temperature with SoftC=0: calls=%d", stop.calls)
	}
}

// The wall-clock cap is the macOS-safe fallback: even with no temperature, an
// unattended keep-awake hold cannot outlast its limit. This is the guard that
// actually protects the Mac, where heat can't be read.
func TestGuardianWallClockCap(t *testing.T) {
	g, stop, aw := newTestGuardian(guardConfig{Enabled: true, SoftC: 0, MaxHours: 1})
	aw.on = true

	// The hold has been up for two hours; a cool reading proves it is time, not
	// heat, that trips this.
	g.awakeFrom = time.Now().Add(-2 * time.Hour)
	g.check(35)

	if stop.calls != 1 || !g.tripped() {
		t.Fatalf("did not trip on the time cap: calls=%d tripped=%v", stop.calls, g.tripped())
	}
	if aw.on {
		t.Fatal("time-cap trip did not release the keep-awake hold")
	}
}

// The time cap only counts while the hold is actually held: a machine nobody
// asked to stay awake is not on the clock.
func TestGuardianTimeCapOnlyWhileHeld(t *testing.T) {
	g, stop, aw := newTestGuardian(guardConfig{Enabled: true, SoftC: 0, MaxHours: 1})
	aw.on = false
	g.awakeFrom = time.Now().Add(-5 * time.Hour) // stale, should be cleared
	g.check(35)
	if stop.calls != 0 {
		t.Fatalf("tripped the time cap while not holding awake: calls=%d", stop.calls)
	}
	if !g.awakeFrom.IsZero() {
		t.Fatal("awakeFrom not cleared when the hold is down")
	}
}

// The hard ceiling powers off only when the host is STILL over the danger line
// after the soft trip already stopped everything, and only when the owner armed
// the poweroff action. This drives it through an injected runner so the real
// command is asserted without a real shutdown.
func TestGuardianHardTripPowersOff(t *testing.T) {
	var ran [][]string
	prev := execRun
	execRun = func(name string, args ...string) error {
		ran = append(ran, append([]string{name}, args...))
		return nil
	}
	defer func() { execRun = prev }()

	g, stop, _ := newTestGuardian(guardConfig{Enabled: true, SoftC: 90, HardC: 100, Action: actionPowerOff})
	for i := 0; i < guardTripN; i++ {
		g.check(103)
	}
	if len(ran) != 1 {
		t.Fatalf("poweroff ran %d times, want 1: %v", len(ran), ran)
	}
	if stop.calls == 0 {
		t.Fatal("hard trip did not stop the sessions before powering off")
	}
}

// The default action never powers off, however hot it gets: nothing privileged
// happens unless the owner deliberately turns it on.
func TestGuardianDefaultActionNeverPowersOff(t *testing.T) {
	var ran int
	prev := execRun
	execRun = func(name string, args ...string) error { ran++; return nil }
	defer func() { execRun = prev }()

	g, stop, _ := newTestGuardian(guardConfig{Enabled: true, SoftC: 90, HardC: 100, Action: actionSleep})
	for i := 0; i < guardTripN*2; i++ {
		g.check(110)
	}
	if ran != 0 {
		t.Fatalf("sleep action ran a command %d times, want 0", ran)
	}
	// It still soft-trips (stops + sleeps); it just never escalates.
	if stop.calls == 0 || !g.tripped() {
		t.Fatal("the soft trip should still have fired")
	}
}

// A poweroff that is denied (no install-time privilege) must not panic or wedge:
// the soft stop already happened, so a failed escalation is logged and survived.
func TestGuardianHardTripSurvivesDeniedPoweroff(t *testing.T) {
	prev := execRun
	execRun = func(name string, args ...string) error { return errPoweroffDenied }
	defer func() { execRun = prev }()

	g, stop, _ := newTestGuardian(guardConfig{Enabled: true, SoftC: 90, HardC: 100, Action: actionPowerOff})
	for i := 0; i < guardTripN; i++ {
		g.check(105)
	}
	if stop.calls == 0 {
		t.Fatal("a denied poweroff should still have stopped the sessions")
	}
}

// The clamp keeps a fat-fingered threshold from making the net useless.
func TestClampGuardConfig(t *testing.T) {
	got := clampGuardConfig(guardConfig{SoftC: 5, MaxHours: -3})
	if got.SoftC != guardMinSoftC {
		t.Errorf("SoftC 5 -> %v, want floor %v", got.SoftC, guardMinSoftC)
	}
	if got.MaxHours != 0 {
		t.Errorf("MaxHours -3 -> %v, want 0", got.MaxHours)
	}
	got = clampGuardConfig(guardConfig{SoftC: 500, MaxHours: 9000})
	if got.SoftC != guardMaxSoftC {
		t.Errorf("SoftC 500 -> %v, want ceiling %v", got.SoftC, guardMaxSoftC)
	}
	if got.MaxHours != guardMaxHours {
		t.Errorf("MaxHours 9000 -> %v, want ceiling %v", got.MaxHours, guardMaxHours)
	}
	// SoftC 0 (no temp check) is a valid choice and must pass through untouched.
	if got := clampGuardConfig(guardConfig{SoftC: 0}); got.SoftC != 0 {
		t.Errorf("SoftC 0 -> %v, want 0 preserved", got.SoftC)
	}
}
