package preview

import "testing"

// Captured from the machine where lsof went blind. The 0BB8 (3000) row is the
// next-server that the preview card refused to show; the 20FC (8444) row is
// kunai's own listener, which lsof DID see -- both were in /proc all along.
const procNetTCP6 = `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000000000000:20FC 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 6751000 1 0000000000000000 100 0 0 10 0
   1: 00000000000000000000000000000000:0BB8 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 6752474 1 0000000000000000 100 0 0 10 0
   2: 00000000000000000000000001000000:1F90 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 6752999 1 0000000000000000 100 0 0 10 0
   3: 00000000000000000000000000000000:0050 00000000000000000000000000000000:0000 01 00000000:00000000 00:00000000 00000000  1000        0 6753111 1 0000000000000000 100 0 0 10 0
`

const procNetTCP = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:20FB 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 6751001 1 0000000000000000 100 0 0 10 0
   1: 0700A8C0:20FB 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 6751002 1 0000000000000000 100 0 0 10 0
   2: 0100007F:8AE6 0100007F:20FB 01 00000000:00000000 00:00000000 00000000  1000        0 6751003 1 0000000000000000 100 0 0 10 0
`

func TestParseProcNetFindsListenersOnly(t *testing.T) {
	got := ParseProcNet(procNetTCP6)
	if len(got) != 3 {
		t.Fatalf("got %d sockets, want 3 (the ESTABLISHED row must be dropped)", len(got))
	}
	// The socket lsof could not see.
	a, ok := got[6752474]
	if !ok {
		t.Fatal("the :3000 listener is missing")
	}
	if a.port != 3000 {
		t.Errorf("port = %d, want 3000", a.port)
	}
	// [::] is the wildcard, which is reachable from off the machine and must NOT
	// be treated as loopback -- kunai would otherwise offer to forward something
	// that already answers.
	if a.loopback {
		t.Error("[::]:3000 was called loopback; the wildcard is not loopback")
	}
}

// ::1 has to come out as loopback without a special case, which is the whole
// reason the address is decoded into a net.IP rather than string-matched.
func TestParseProcNetDecodesIPv6Loopback(t *testing.T) {
	got := ParseProcNet(procNetTCP6)
	a, ok := got[6752999]
	if !ok {
		t.Fatal("the ::1 listener is missing")
	}
	if a.port != 8080 {
		t.Errorf("port = %d, want 8080", a.port)
	}
	if !a.loopback {
		t.Error("[::1]:8080 was not called loopback")
	}
}

// The v4 table stores each word little-endian, so 0100007F is 127.0.0.1 and
// 0700A8C0 is 192.168.0.7. Reading them straight through would invert both.
func TestParseProcNetDecodesIPv4ByteOrder(t *testing.T) {
	got := ParseProcNet(procNetTCP)
	if len(got) != 2 {
		t.Fatalf("got %d listeners, want 2", len(got))
	}
	if a := got[6751001]; !a.loopback || a.port != 8443 {
		t.Errorf("0100007F:20FB = %+v, want 127.0.0.1:8443 loopback", a)
	}
	if a := got[6751002]; a.loopback || a.port != 8443 {
		t.Errorf("0700A8C0:20FB = %+v, want 192.168.0.7:8443 NOT loopback", a)
	}
}

// A socket nothing can be pinned to is dropped rather than reported with pid 0,
// which would then be attributed to no session and, worse, match nothing in the
// ancestry walk while still occupying a row.
func TestAttributeDropsUnownedSockets(t *testing.T) {
	socks := map[uint64]listenAddr{
		1: {port: 3000, loopback: false},
		2: {port: 9999, loopback: true},
	}
	got := attribute(socks, map[uint64]int{1: 4242})
	if len(got) != 1 || got[0].Port != 3000 || got[0].PID != 4242 {
		t.Fatalf("got %+v, want only the owned :3000 socket", got)
	}
}

// One server holding both 127.0.0.1:3000 and [::]:3000 is ONE entry, and it is
// only "local" when every address for that port is loopback. Getting this
// backwards makes kunai forward a port that already answers.
func TestAttributeCollapsesPortsAndRequiresAllLoopback(t *testing.T) {
	owners := map[uint64]int{1: 7, 2: 7}
	both := attribute(map[uint64]listenAddr{
		1: {port: 3000, loopback: true},
		2: {port: 3000, loopback: false},
	}, owners)
	if len(both) != 1 {
		t.Fatalf("got %d entries, want the two addresses collapsed into one", len(both))
	}
	if both[0].Local {
		t.Error("a port also bound to a routable address is not local-only")
	}

	only := attribute(map[uint64]listenAddr{
		1: {port: 3000, loopback: true},
		2: {port: 3000, loopback: true},
	}, owners)
	if !only[0].Local {
		t.Error("a port bound only to loopback addresses is local-only")
	}
}

// A map walk has no order, so without the sort the preview card reshuffles on
// every poll.
func TestAttributeIsOrderedByPort(t *testing.T) {
	socks := map[uint64]listenAddr{}
	owners := map[uint64]int{}
	for i, p := range []int{9000, 3000, 5173, 8080} {
		socks[uint64(i+1)] = listenAddr{port: p}
		owners[uint64(i+1)] = 100 + i
	}
	for range 20 { // the map order differs between runs; any of them must sort
		got := attribute(socks, owners)
		want := []int{3000, 5173, 8080, 9000}
		for i, s := range got {
			if s.Port != want[i] {
				t.Fatalf("order = %+v, want %v", got, want)
			}
		}
	}
}

func TestSocketInode(t *testing.T) {
	if n, ok := socketInode("socket:[6752474]"); !ok || n != 6752474 {
		t.Errorf("socket:[6752474] = %d %v", n, ok)
	}
	for _, s := range []string{"/dev/null", "pipe:[123]", "socket:[]", "socket:[abc]", "anon_inode:[eventfd]"} {
		if _, ok := socketInode(s); ok {
			t.Errorf("%q was read as a socket inode", s)
		}
	}
}

// The live reader, against whatever this machine is running. It must not error,
// and every socket it reports must carry a pid and a port -- the two things the
// attribution and the link both depend on.
func TestListenersOnThisMachine(t *testing.T) {
	got, ok := Listeners()
	if !ok {
		t.Skip("no /proc/net/tcp on this machine")
	}
	for _, s := range got {
		if s.PID <= 0 || s.Port <= 0 {
			t.Errorf("incomplete server %+v", s)
		}
	}
	t.Logf("found %d listening servers", len(got))
}

// Sharing a preview must not make it disappear.
//
// When kunai forwards a port it becomes a SECOND listener on that same port, so
// the port now has two sockets: the dev server's and kunai's. Entries collapse
// by port, so kunai's socket could win the collapse and stamp the row with a pid
// no session owns -- and the row was dropped, taking the Stop button with it and
// leaving the port forwarded with no way to turn it off. The port must stay,
// attributed to the process that actually serves it.
func TestAttributeIgnoresKunaisOwnHalfOfAForwardedPort(t *testing.T) {
	const devServer, port = 4242, 4999
	socks := map[uint64]listenAddr{
		1: {port: port, loopback: true},  // the dev server, on loopback
		2: {port: port, loopback: false}, // kunai's forward, on the tailnet address
	}
	got := attribute(socks, map[uint64]int{1: devServer, 2: selfPID})

	if len(got) != 1 {
		t.Fatalf("got %d entries, want the port still listed once: %+v", len(got), got)
	}
	if got[0].PID != devServer {
		t.Errorf("pid = %d, want the dev server %d, not kunai", got[0].PID, devServer)
	}
	// And kunai's routable socket must not flip the dev server's own reading: it
	// is still a loopback-only server that kunai happens to be forwarding.
	if !got[0].Local {
		t.Error("kunai's own forward made the dev server look already-reachable")
	}
}

// Nothing kunai listens on is ever a session's server, however it was found.
func TestAttributeDropsKunaisOwnListeners(t *testing.T) {
	got := attribute(
		map[uint64]listenAddr{1: {port: 8443}, 2: {port: 8444}},
		map[uint64]int{1: selfPID, 2: selfPID},
	)
	if len(got) != 0 {
		t.Errorf("got %+v, want none of kunai's own ports", got)
	}
}
