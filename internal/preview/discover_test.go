package preview

import "testing"

// Captured from a real machine. The shape that matters is the third process:
// one server holding the SAME port on three addresses, which is one server and
// is NOT loopback-only.
const realLSOF = `p5451
ckunai-nightly
n127.0.0.1:8444
n192.168.0.7:8444
n100.90.239.81:8444
n127.0.0.1:41467
p9001
cnode
n127.0.0.1:3000
p9100
cpython3
n*:8000
p9200
csshd
n1.2.3.4:22->5.6.7.8:51234
`

func TestParseLSOFReadsRealOutput(t *testing.T) {
	got := ParseLSOF(realLSOF)
	byPort := map[int]Server{}
	for _, s := range got {
		byPort[s.Port] = s
	}

	// Three addresses, one port, one server -- and reachable off this machine, so
	// kunai must not offer to forward it.
	s8444, ok := byPort[8444]
	if !ok {
		t.Fatal("the multi-address server was dropped")
	}
	if s8444.Local {
		t.Error("a port also bound to a routable address was reported as loopback-only")
	}
	if s8444.Command != "kunai-nightly" || s8444.PID != 5451 {
		t.Errorf("process details lost: %+v", s8444)
	}
	// The same process's other port is genuinely loopback-only.
	if p, ok := byPort[41467]; !ok || !p.Local {
		t.Errorf("41467 = %+v, want a loopback-only entry", p)
	}
	// A plain dev server.
	if p := byPort[3000]; p.Command != "node" || !p.Local || p.PID != 9001 {
		t.Errorf("3000 = %+v, want node on loopback", p)
	}
	// "*:8000" is every interface, so not loopback-only.
	if p, ok := byPort[8000]; !ok || p.Local {
		t.Errorf("8000 = %+v, want a non-local entry", p)
	}
	// An established connection is not a listener.
	if _, ok := byPort[22]; ok {
		t.Error("a connection (addr->addr) was read as a listening server")
	}
}

func TestParseListenAddrShapes(t *testing.T) {
	for _, c := range []struct {
		in       string
		port     int
		loopback bool
		ok       bool
	}{
		{"127.0.0.1:3000", 3000, true, true},
		{"[::1]:5173", 5173, true, true},
		{"*:8080", 8080, false, true},
		{"0.0.0.0:8080", 8080, false, true},
		{"192.168.0.7:8444", 8444, false, true},
		{"127.0.0.53:53", 53, true, true},
		{"1.2.3.4:80->5.6.7.8:90", 0, false, false}, // a connection
		{"/run/some.sock", 0, false, false},
		{"127.0.0.1:notaport", 0, false, false},
		{"127.0.0.1:0", 0, false, false},
		{"127.0.0.1:99999", 0, false, false},
	} {
		got, ok := parseListenAddr(c.in)
		if ok != c.ok {
			t.Errorf("parseListenAddr(%q) ok = %v, want %v", c.in, ok, c.ok)
			continue
		}
		if ok && (got.port != c.port || got.loopback != c.loopback) {
			t.Errorf("parseListenAddr(%q) = %+v, want port %d loopback %v", c.in, got, c.port, c.loopback)
		}
	}
}

// Attribution is the whole point: two sessions working at once must not be
// offered each other's servers.
func TestOwnedFollowsProcessAncestry(t *testing.T) {
	// claude(100) -> shell(110) -> npm(120) -> node(130) listening on 3000
	// claude(200) -> node(210) listening on 4000
	parents := ParseProcessTree(`
  100 1
  110 100
  120 110
  130 120
  200 1
  210 200
  900 1
`)
	servers := []Server{
		{Port: 3000, PID: 130, Command: "node"},
		{Port: 4000, PID: 210, Command: "node"},
		{Port: 5000, PID: 900, Command: "unrelated"},
	}

	a := Owned(servers, parents, 100)
	if len(a) != 1 || a[0].Port != 3000 {
		t.Errorf("session A got %+v, want only :3000 (four levels down)", a)
	}
	b := Owned(servers, parents, 200)
	if len(b) != 1 || b[0].Port != 4000 {
		t.Errorf("session B got %+v, want only :4000", b)
	}
	// A process started by neither belongs to neither.
	for _, root := range []int{100, 200} {
		for _, s := range Owned(servers, parents, root) {
			if s.Port == 5000 {
				t.Error("an unrelated process's port was attributed to a session")
			}
		}
	}
	// A dead or unknown session owns nothing rather than everything.
	if got := Owned(servers, parents, 0); len(got) != 0 {
		t.Errorf("root 0 owned %+v, want nothing", got)
	}
}

// A malformed table must not hang the scan.
func TestDescendsFromSurvivesCycles(t *testing.T) {
	cycle := map[int]int{10: 11, 11: 10}
	if DescendsFrom(cycle, 10, 999) {
		t.Error("a cycle reported a false ancestor")
	}
	// Self-parent is the other way a table goes wrong.
	if DescendsFrom(map[int]int{7: 7}, 7, 999) {
		t.Error("a self-parented process reported a false ancestor")
	}
	// And the trivial truth still holds.
	if !DescendsFrom(map[int]int{}, 42, 42) {
		t.Error("a process is not its own descendant")
	}
}

// The case that made ancestry insufficient, and the reason OwnedBy exists.
//
// The agent starts a dev server as a background command; the shell that launched
// it exits, the kernel reparents the server to init, and its chain to `claude` is
// gone. Observed on a real session: next-server -> sh -> (exited) -> systemd.
// Ancestry sees nothing. The working directory still does.
func TestAnOrphanedDevServerIsStillAttributed(t *testing.T) {
	// The chain is severed: 4170030's ancestors reach systemd, never claude(999).
	parents := ParseProcessTree(`
  4170030 4170001
  4170001 4170000
  4170000 5329
     5329 1
      999 1
`)
	servers := []Server{{Port: 3000, PID: 4170030, Command: "next-server", Local: true}}
	cwds := map[int]string{4170030: "/home/me/landing-page"}

	if got := Owned(servers, parents, 999); len(got) != 0 {
		t.Fatal("ancestry alone should not find an orphaned server; the fixture is wrong")
	}
	got := OwnedBy(servers, parents, 999, cwds, "/home/me/landing-page")
	if len(got) != 1 || got[0].Port != 3000 {
		t.Errorf("the orphaned server was not attributed by working directory: %+v", got)
	}
	// A server inside a subdirectory of the session still counts.
	cwds[4170030] = "/home/me/landing-page/apps/web"
	if got := OwnedBy(servers, parents, 999, cwds, "/home/me/landing-page"); len(got) != 1 {
		t.Error("a server running in a subdirectory of the session was not attributed")
	}
	// A neighbouring directory does NOT, even though its path shares a prefix.
	// This is why the check is by segment rather than by string prefix.
	cwds[4170030] = "/home/me/landing-page2"
	if got := OwnedBy(servers, parents, 999, cwds, "/home/me/landing-page"); len(got) != 0 {
		t.Error("a sibling directory sharing a name prefix was attributed to this session")
	}
	// And an unknown directory attributes nothing rather than everything.
	if got := OwnedBy(servers, parents, 999, map[int]string{}, "/home/me/landing-page"); len(got) != 0 {
		t.Error("a server with no readable cwd was attributed anyway")
	}
}

func TestParseCwdsReadsTheFieldFormat(t *testing.T) {
	got := ParseCwds("p4170030\nn/home/me/landing-page\np999\nn/home/me/other\n")
	if got[4170030] != "/home/me/landing-page" || got[999] != "/home/me/other" {
		t.Errorf("ParseCwds = %+v", got)
	}
	if len(ParseCwds("")) != 0 {
		t.Error("empty output produced entries")
	}
}
