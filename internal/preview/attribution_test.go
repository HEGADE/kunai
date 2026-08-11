package preview

import "testing"

// A session started in the HOME directory must not claim the machine.
//
// The working-directory rule exists to recover a dev server whose ancestry was
// severed by reparenting, and it is only as good as the directory. Opened in
// /home/ninja it matched every process under home, which on a personal machine
// is nearly all of them: the card offered the OTHER kunai instance's service
// port and both of its internal provider proxies as things to share.
func TestAHomeDirectorySessionClaimsNothingByDirectory(t *testing.T) {
	restore := userHome
	userHome = func() string { return "/home/ninja" }
	t.Cleanup(func() { userHome = restore })

	servers := []Server{
		{Port: 3000, Command: "node", PID: 10},
		{Port: 5173, Command: "vite", PID: 11},
	}
	cwds := map[int]string{10: "/home/ninja/coding/app", 11: "/home/ninja"}
	parents := map[int]int{} // nothing is a descendant of the session

	if got := OwnedBy(servers, parents, 99, cwds, "/home/ninja"); len(got) != 0 {
		t.Errorf("a home-directory session claimed %d servers, want 0: %+v", len(got), got)
	}
	// Ancestry still works there, which is the point of leaving it alone.
	desc := map[int]int{10: 99}
	if got := OwnedBy(servers, desc, 99, cwds, "/home/ninja"); len(got) != 1 || got[0].Port != 3000 {
		t.Errorf("ancestry stopped working in a home session: %+v", got)
	}
	// And a real project directory still attributes by directory as before.
	if got := OwnedBy(servers, parents, 99, cwds, "/home/ninja/coding/app"); len(got) != 1 || got[0].Port != 3000 {
		t.Errorf("a project session lost directory attribution: %+v", got)
	}
}

func TestContainerDirectoriesAreNotAttributable(t *testing.T) {
	restore := userHome
	userHome = func() string { return "/home/ninja" }
	t.Cleanup(func() { userHome = restore })

	for _, dir := range []string{"/", "/home", "/home/ninja", "/home/ninja/"} {
		if attributableDir(dir) {
			t.Errorf("%q was treated as a project directory", dir)
		}
	}
	for _, dir := range []string{"/home/ninja/coding", "/home/ninja/app", "/srv/thing", "/tmp/work"} {
		if !attributableDir(dir) {
			t.Errorf("%q was refused as a project directory", dir)
		}
	}
}

// Another kunai is never somebody's dev server. The nightly channel is designed
// to run beside a stable install, so two of them is the ordinary state of a
// developer's machine, and the other one's ports are the app you are looking at
// or its internal provider proxies.
func TestAnotherKunaiIsNeverAPreview(t *testing.T) {
	restore := userHome
	userHome = func() string { return "/home/ninja" }
	t.Cleanup(func() { userHome = restore })

	servers := []Server{
		{Port: 8443, Command: "kunai", PID: 20},
		{Port: 33225, Command: "kunai", PID: 20},
		{Port: 8444, Command: "kunai-nightly", PID: 21},
		{Port: 3000, Command: "node", PID: 22},
	}
	cwds := map[int]string{20: "/home/ninja/app", 21: "/home/ninja/app", 22: "/home/ninja/app"}

	got := OwnedBy(servers, map[int]int{}, 99, cwds, "/home/ninja/app")
	if len(got) != 1 || got[0].Port != 3000 {
		t.Errorf("want only the node server, got %+v", got)
	}
}

// A test run is nobody's dev server. `go test ./...` binds a real port for the
// couple of seconds a suite with an httptest server takes, and offering that is a
// link to a process that has already exited.
func TestATestBinaryIsNeverAPreview(t *testing.T) {
	restore := userHome
	userHome = func() string { return "/home/ninja" }
	t.Cleanup(func() { userHome = restore })

	servers := []Server{
		{Port: 37595, Command: "server.test", PID: 40},
		{Port: 41000, Command: "preview.test", PID: 41},
		{Port: 3000, Command: "next-server", PID: 42},
		// Not a Go test binary: a real program whose name merely contains the word.
		{Port: 5173, Command: "testbed", PID: 43},
	}
	cwds := map[int]string{40: "/home/ninja/app", 41: "/home/ninja/app", 42: "/home/ninja/app", 43: "/home/ninja/app"}

	got := OwnedBy(servers, map[int]int{}, 99, cwds, "/home/ninja/app")
	var ports []int
	for _, s := range got {
		ports = append(ports, s.Port)
	}
	if len(ports) != 2 || ports[0] != 3000 || ports[1] != 5173 {
		t.Errorf("want the real servers only, got %v", ports)
	}
}

// The exclusion is of the SOCKET, never the port: a forwarded preview is two
// listeners on one port, and dropping the port would delete the row the instant
// you shared it, taking the Stop button with it.
func TestForwardingAPortDoesNotDeleteItsRow(t *testing.T) {
	restore := userHome
	userHome = func() string { return "/home/ninja" }
	t.Cleanup(func() { userHome = restore })

	servers := []Server{
		{Port: 4999, Command: "next-server", PID: 30}, // the real dev server
		{Port: 4999, Command: "kunai", PID: 31},       // kunai holding the forward
	}
	cwds := map[int]string{30: "/home/ninja/app", 31: "/home/ninja"}
	got := OwnedBy(servers, map[int]int{}, 99, cwds, "/home/ninja/app")
	if len(got) != 1 || got[0].PID != 30 {
		t.Errorf("the forwarded row was lost or misattributed: %+v", got)
	}
}
