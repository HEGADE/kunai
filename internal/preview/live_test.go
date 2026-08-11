package preview

import (
	"context"
	"net"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

// The parsers are unit-tested against captured output; this runs the whole thing
// against the real machine, because the two commands are the part that rots.
// lsof's flags differ between builds, ps's column defaults differ between Linux
// and macOS, and a captured fixture cannot notice either.
//
// It starts a child that listens, then asks: does the scan attribute that port to
// THIS process? That is exactly the question the server asks about a session,
// with this test's own pid standing in for the `claude` process.
//
// Gated like the project's other live tests, since it shells out and binds a
// port: KUNAI_LIVE=1 go test ./internal/preview/ -run Live
func TestLiveDiscoveryFindsAChildsPort(t *testing.T) {
	if os.Getenv("KUNAI_LIVE") == "" {
		t.Skip("set KUNAI_LIVE=1 to run against this machine's real lsof and ps")
	}
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("lsof is not installed here")
	}

	// A free port, released immediately so the child can take it.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, portStr, _ := net.SplitHostPort(probe.Addr().String())
	port, _ := strconv.Atoi(portStr)
	_ = probe.Close()

	// A child of this test process, holding that port. Started through a shell so
	// the tree has a middle link, like the real chain (claude -> shell -> npm ->
	// node).
	//
	// Deliberately NOT netcat: `nc -l` serves one connection and exits, so the
	// readiness check below would be the thing that killed the listener before
	// the scan could see it. A server that survives being connected to is the
	// only kind this test can measure.
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed here")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	child := exec.CommandContext(ctx, "sh", "-c",
		"exec node -e "+strconv.Quote("require('net').createServer(function(c){c.end()}).listen("+portStr+",'127.0.0.1')"))
	if err := child.Start(); err != nil {
		t.Skipf("could not start a listening child: %v", err)
	}
	defer func() { _ = child.Process.Kill() }()

	// Wait for the port to actually be held.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", "127.0.0.1:"+portStr, 200*time.Millisecond); err == nil {
			_ = c.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	lsofOut, _ := run("lsof", "-iTCP", "-sTCP:LISTEN", "-P", "-n", "-F", "pcn")
	psOut, _ := run("ps", "-Ao", "pid=,ppid=")
	if lsofOut == "" {
		t.Fatal("lsof produced nothing; the flag set may not be supported by this build")
	}
	if psOut == "" {
		t.Fatal("ps produced nothing with -Ao pid=,ppid=")
	}

	all := ParseLSOF(lsofOut)
	if len(all) == 0 {
		t.Fatal("parsed no listening sockets from real lsof output; the -F format has changed")
	}
	tree := ParseProcessTree(psOut)
	if len(tree) < 2 {
		t.Fatal("parsed no process tree from real ps output")
	}

	owned := Owned(all, tree, os.Getpid())
	var found bool
	for _, s := range owned {
		if s.Port == port {
			found = true
			if !s.Local {
				t.Errorf("a 127.0.0.1 listener was reported as reachable off-machine: %+v", s)
			}
		}
	}
	if !found {
		t.Errorf("the child's port %d was not attributed to this process; got %+v", port, owned)
	}

	// The negative half is what makes attribution worth anything: a DIFFERENT
	// session must not be offered this port. A sibling process stands in for one.
	//
	// Not pid 1, which would prove nothing: everything descends from init, so
	// "owned by pid 1" is true of every port on the machine and is the correct
	// answer rather than a bug. Ancestry is only useful because `claude` sits
	// partway down the tree.
	sibling := exec.CommandContext(ctx, "sleep", "30")
	if err := sibling.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sibling.Process.Kill() }()

	psAgain, _ := run("ps", "-Ao", "pid=,ppid=")
	for _, s := range Owned(all, ParseProcessTree(psAgain), sibling.Process.Pid) {
		if s.Port == port {
			t.Error("another process's port was attributed to an unrelated sibling")
		}
	}
}

func run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}
