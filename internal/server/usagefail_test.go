package server

import (
	"testing"
	"time"
)

// A quota poll that cannot get an answer must stop asking, and must stop saying
// so. Both halves were missing: the Codex and Grok caches stored only success, so
// every caller repeated a request a third party had already refused, and every
// repeat wrote a log line. On a machine with one stale Grok login that was half of
// everything in the journal.
func TestAFailedQuotaPollIsRememberedAndReportedOnce(t *testing.T) {
	var f usageFailure
	t0 := time.Unix(1785200000, 0)

	if f.holding(t0) {
		t.Fatal("a fresh cache is holding a failure it never had")
	}

	// First failure is news.
	if !f.note(t0, time.Minute, "HTTP 401") {
		t.Error("the first failure was not reported")
	}
	if !f.holding(t0.Add(30 * time.Second)) {
		t.Error("the failure was forgotten inside its own back-off window")
	}

	// The same failure again is not news, however often it is seen.
	for i := 0; i < 5; i++ {
		if f.note(t0.Add(time.Duration(i)*time.Second), time.Minute, "HTTP 401") {
			t.Errorf("repeat %d of an unchanged failure was reported again", i)
		}
	}

	// A DIFFERENT failure is news: 401 becoming 503 means something else is wrong.
	if !f.note(t0, time.Minute, "HTTP 503") {
		t.Error("a failure with a new message was suppressed as a repeat")
	}

	// The window lapses, so the poll is retried rather than given up on forever.
	if f.holding(t0.Add(2 * time.Minute)) {
		t.Error("the back-off never expires, so a fixed login would never be noticed")
	}

	// A success forgets it, so the NEXT failure is reported rather than swallowed
	// as a repeat of one that was resolved long ago.
	f.clear()
	if f.holding(t0.Add(time.Second)) {
		t.Error("a success left the failure standing")
	}
	if !f.note(t0, time.Minute, "HTTP 401") {
		t.Error("the first failure after a success was suppressed")
	}
}
