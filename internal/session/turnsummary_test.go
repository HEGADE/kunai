package session

import "testing"

// A notification sits on a lock screen, so a turn's length is rendered compactly
// and never as raw milliseconds.
func TestShortDuration(t *testing.T) {
	cases := map[int64]string{
		0:         "",
		-5:        "",
		1500:      "1s",
		38_000:    "38s",
		252_000:   "4m 12s",
		3600_000:  "1h 0m",
		3_840_000: "1h 4m",
	}
	for ms, want := range cases {
		if got := shortDuration(ms); got != want {
			t.Errorf("shortDuration(%d) = %q, want %q", ms, got, want)
		}
	}
}

// The summary is what reaches the phone when a turn ends: how long it ran and
// what it cost, both measurements kunai took of its own work.
func TestTurnSummary(t *testing.T) {
	got := turnSummary(AppEvent{DurationMs: 252_000, CostUSD: 0.42})
	if got != "4m 12s · $0.42" {
		t.Errorf("got %q, want \"4m 12s · $0.42\"", got)
	}
}

// A sub-cent turn would round to "$0.00", which says less than saying nothing.
func TestTurnSummaryOmitsNegligibleCost(t *testing.T) {
	got := turnSummary(AppEvent{DurationMs: 38_000, CostUSD: 0.004})
	if got != "38s" {
		t.Errorf("got %q, want just the duration", got)
	}
}

// A frame reporting neither leaves the notification at its plain wording rather
// than appending an empty fragment.
func TestTurnSummaryEmptyWhenNothingMeasured(t *testing.T) {
	if got := turnSummary(AppEvent{}); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// Cost alone must still be a well-formed summary (no leading separator).
func TestTurnSummaryCostOnly(t *testing.T) {
	got := turnSummary(AppEvent{CostUSD: 1.5})
	if got != "$1.50" {
		t.Errorf("got %q, want \"$1.50\"", got)
	}
}
