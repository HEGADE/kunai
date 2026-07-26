package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/hegade/kunai/internal/session"
)

// The fleet socket: one per machine, carrying the things a client used to ask for
// on a timer.
//
// The client polled every machine for its session list every few seconds. That is
// wrong in two directions at once. It is too slow, because the sidebar reports
// what each agent is doing and a status board that lags by up to eight seconds
// will tell you an agent is working after it has stopped to ask you something. And
// it is too much, because the answer usually has not changed, and the cost is a
// request per machine per tick, so it grows with the size of your fleet rather
// than with how much is happening.
//
// A push fixes both: nothing on the wire while nothing happens, and a change
// arrives in one round trip instead of on the next beat. The session list is sent
// whole rather than as a delta -- it is a handful of small objects, and a whole
// list cannot drift out of sync with the server the way a stream of patches can.
//
// Stats stay on a timer, because CPU and memory are sampled rather than evented,
// but the timer now lives on the server: it ticks only while somebody is
// connected, and one tick serves every client on that machine.
const (
	// How long to gather changes before sending. A turn ending fires several
	// changes in a few milliseconds (state to idle, the session's own events, a
	// reap), and each would otherwise be its own list send.
	fleetCoalesce = 120 * time.Millisecond
	// Stats cadence while a client is watching. The client used to fetch these
	// every ~16s per machine.
	fleetStatsEvery = 10 * time.Second
)

// fleetHub fans machine-level changes out to connected clients.
type fleetHub struct {
	mu   sync.Mutex
	subs map[chan struct{}]bool
}

func newFleetHub() *fleetHub {
	return &fleetHub{subs: make(map[chan struct{}]bool)}
}

// notify wakes every subscriber. The channel is depth-1 and the send is
// non-blocking, so a client that is slow to read coalesces into one wake-up
// instead of applying back-pressure to the session that changed.
func (h *fleetHub) notify() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (h *fleetHub) subscribe() chan struct{} {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	h.subs[ch] = true
	h.mu.Unlock()
	// Seeded so a new subscriber sends its first snapshot immediately without a
	// special case in the loop.
	ch <- struct{}{}
	return ch
}

func (h *fleetHub) unsubscribe(ch chan struct{}) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

// fleetMsg is what goes over the socket. One field set per message; the client
// switches on T. Mirrors FleetMsg in web/src/lib/fleet.ts.
type fleetMsg struct {
	T        string         `json:"t"`
	Sessions []session.Meta `json:"sessions,omitempty"`
	Stats    *Stats         `json:"stats,omitempty"`
}

func (s *Server) handleFleetWS(w http.ResponseWriter, r *http.Request) {
	// Same origin policy as /ws/app: the tailnet is the auth perimeter, and the
	// hub's PWA legitimately connects to a peer's origin.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return
	}
	defer conn.CloseNow()

	ctx := r.Context()
	ch := s.fleet.subscribe()
	defer s.fleet.unsubscribe(ch)

	stats := time.NewTicker(fleetStatsEvery)
	defer stats.Stop()

	// Read side: we expect nothing, but a reader must run or the library never
	// notices a client going away, and this goroutine would leak per closed tab.
	go func() {
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	}()

	send := func(m fleetMsg) error {
		b, err := json.Marshal(m)
		if err != nil {
			return err
		}
		wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return conn.Write(wctx, websocket.MessageText, b)
	}

	if err := send(fleetMsg{T: "stats", Stats: s.collectStats()}); err != nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
			// Coalesce a burst: a turn ending moves the state, emits events and may
			// reap the session, and each of those is a change.
			timer := time.NewTimer(fleetCoalesce)
			drain := true
			for drain {
				select {
				case <-ch:
				case <-timer.C:
					drain = false
				case <-ctx.Done():
					timer.Stop()
					return
				}
			}
			if err := send(fleetMsg{T: "sessions", Sessions: s.sessionList()}); err != nil {
				return
			}
		case <-stats.C:
			if err := send(fleetMsg{T: "stats", Stats: s.collectStats()}); err != nil {
				return
			}
		}
	}
}

// sessionList is the same list GET /api/sessions returns, tagged and merged the
// same way. Shared deliberately: a push that showed a different shape from the
// fetch would be a bug nobody could see until a client mixed the two.
func (s *Server) sessionList() []session.Meta {
	metas := s.mgr.List()
	s.worktrees.tagRepos(metas)
	if s.sessionMeta != nil {
		mergeMeta(metas, s.sessionMeta.all())
	}
	return metas
}
