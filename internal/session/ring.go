package session

// ring is a bounded FIFO of AppEvents kept for reconnect replay. Events are
// appended with strictly increasing Seq; when capacity is exceeded the oldest
// are dropped. since() returns every buffered event with Seq greater than a
// caller-supplied high-water mark — the core of detached-reconnect resume.
type ring struct {
	buf []AppEvent
	cap int
}

func newRing(capacity int) *ring {
	if capacity < 1 {
		capacity = 1
	}
	return &ring{buf: make([]AppEvent, 0, capacity), cap: capacity}
}

// add appends ev, evicting the oldest event if at capacity.
func (r *ring) add(ev AppEvent) {
	if len(r.buf) == r.cap {
		// Drop the oldest. Shift in place to keep order simple and correct;
		// the buffer is small and appends dominate reads.
		copy(r.buf, r.buf[1:])
		r.buf[len(r.buf)-1] = ev
		return
	}
	r.buf = append(r.buf, ev)
}

// since returns all buffered events with Seq > afterSeq, in order. The result
// is a fresh slice safe for the caller to use without holding a lock.
func (r *ring) since(afterSeq uint64) []AppEvent {
	out := make([]AppEvent, 0, len(r.buf))
	for _, ev := range r.buf {
		if ev.Seq > afterSeq {
			out = append(out, ev)
		}
	}
	return out
}

// oldestSeq is the lowest Seq still buffered (0 if empty). If a client's
// afterSeq is below this, some history was evicted and the gap is unrecoverable
// from the buffer alone.
func (r *ring) oldestSeq() uint64 {
	if len(r.buf) == 0 {
		return 0
	}
	return r.buf[0].Seq
}
