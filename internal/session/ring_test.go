package session

import "testing"

func TestRingSinceReturnsEventsAfterSeq(t *testing.T) {
	r := newRing(10)
	for i := uint64(1); i <= 5; i++ {
		r.add(AppEvent{Seq: i, T: EvDelta})
	}
	got := r.since(2)
	if len(got) != 3 {
		t.Fatalf("since(2): want 3 events, got %d", len(got))
	}
	if got[0].Seq != 3 || got[2].Seq != 5 {
		t.Fatalf("since(2): want seqs 3..5, got %d..%d", got[0].Seq, got[2].Seq)
	}
	if n := len(r.since(5)); n != 0 {
		t.Fatalf("since(5): want 0, got %d", n)
	}
	if n := len(r.since(0)); n != 5 {
		t.Fatalf("since(0): want all 5, got %d", n)
	}
}

func TestRingEvictsOldestAtCapacity(t *testing.T) {
	r := newRing(3)
	for i := uint64(1); i <= 5; i++ {
		r.add(AppEvent{Seq: i})
	}
	if len(r.buf) != 3 {
		t.Fatalf("want len 3, got %d", len(r.buf))
	}
	if r.oldestSeq() != 3 {
		t.Fatalf("want oldestSeq 3 after eviction, got %d", r.oldestSeq())
	}
	got := r.since(0)
	if got[0].Seq != 3 || got[2].Seq != 5 {
		t.Fatalf("want retained seqs 3..5, got %d..%d", got[0].Seq, got[2].Seq)
	}
}
