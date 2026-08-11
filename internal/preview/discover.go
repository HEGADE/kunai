// Package preview finds the servers your agent started.
//
// An agent that has just built something usually ends by running it: `npm run
// dev`, `python -m http.server`, a test runner with a web UI. Until now kunai
// could tell you what the agent WROTE and never what it MADE -- the thing is
// listening on a port on a machine you are not sitting at, and finding it meant
// reading the transcript for a port number and then working out how to reach it.
//
// This package answers the first half: which ports are listening, and which
// session is responsible for each. The second half (making it reachable) is the
// server's job; see internal/server/preview.go.
//
// Attribution is by PROCESS ANCESTRY, and that choice is the whole design. A
// port has no idea which conversation caused it, but the process holding it is a
// descendant of the `claude` process kunai spawned for that session, and that is
// a fact rather than a guess. Matching on the port number, or on the working
// directory, or on "the session that was running most recently" would all be
// heuristics that are wrong exactly when two sessions are working at once --
// which is the case kunai exists for.
package preview

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// selfPID is this process, whose own listening sockets are never a session's
// server: kunai's service port, its loopback and LAN listeners, the share gate,
// the native provider proxies -- and, the case that made this necessary, the
// ports it is FORWARDING on a session's behalf.
//
// A forwarded preview appears as a second listening socket on the same port
// (the dev server on 127.0.0.1:4999, kunai on 100.x:4999). Since entries
// collapse by port, that socket could win the collapse and stamp the row with
// kunai's pid, which belongs to no session -- so the row was dropped and the
// preview VANISHED from the card the instant you shared it, taking the Stop
// button with it and leaving the port forwarded with no way back.
//
// Excluding by pid rather than by port is what makes this exact: the port stays
// listed and attributed to the session that owns the dev server, while kunai's
// own half of it is ignored. It also closes a quieter hole, since kunai started
// from inside a project directory would otherwise have its own ports attributed
// to that project's session by the working-directory rule.
var selfPID = os.Getpid()

// isOurs reports whether a listening process is another kunai.
//
// selfPID covers this process and nothing else, which was enough until a machine
// ran two of them. The nightly channel is designed to sit beside a stable install
// (separate service, separate port, separate data dir), so the ordinary state of
// a developer's machine is two kunai processes -- and the other one's sockets are
// not somebody's dev server. On a real machine that meant the preview card
// offering `:8443 kunai` next to the two ephemeral ports of the OTHER instance's
// native Codex and Grok proxies, which are internal plumbing that exists to be
// talked to by a `claude` process on this machine and cannot be usefully shared
// with anyone.
//
// Matched on the process name rather than the executable path because that is
// the one identifier both listener backends produce (/proc on Linux, lsof on
// macOS), and because the two channels run from different binaries with
// different names by design (`kunai`, `kunai-nightly`).
//
// This is a superset of the selfPID rule and keeps its important property: the
// socket is skipped, not the PORT. A forwarded preview appears as two listeners
// on one port, and since entries collapse by port, excluding the port would
// delete the row the instant you shared it. Skipping only kunai's own socket
// leaves the row attributed to the dev server that owns it.
func isOurs(command string) bool {
	if command == "" {
		return false
	}
	return command == selfName || strings.HasPrefix(command, "kunai")
}

// isTestBinary reports whether a listener is a test run rather than something
// somebody would want to open.
//
// `go test ./...` compiles each package to `<pkg>.test` and runs it, and a suite
// that stands up an httptest server binds a real port for the couple of seconds
// it takes. kunai's scan catches that and offers it as a preview to share, which
// is a link to a process that has already exited by the time anyone taps it.
//
// Matched on the `.test` suffix, which is the toolchain's own naming rather than
// a guess: a Go test binary is always named that way and nothing a person runs
// on purpose is. Kept separate from isOurs because it is a different claim --
// that one says "this is kunai", this one says "this is nobody's dev server".
func isTestBinary(command string) bool {
	return strings.HasSuffix(command, ".test")
}

// selfName is this binary's own name, so a renamed or wrapped install is still
// recognised as ours even if it is not called kunai at all.
var selfName = func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Base(exe)
}()

// Server is one listening socket kunai found.
type Server struct {
	// Port is what it is listening on.
	Port int `json:"port"`
	// Command is the process name, for a human to recognise ("node", "vite").
	Command string `json:"command"`
	// PID is the listening process.
	PID int `json:"pid"`
	// Local is true when it listens only on loopback, which means nothing off
	// this machine can reach it without help. That distinction decides whether
	// kunai needs to forward it or can simply link to it.
	Local bool `json:"local"`
}

// ParseLSOF reads `lsof -iTCP -sTCP:LISTEN -P -n -F pcn` output.
//
// The -F field format is used rather than the default table because the table is
// aligned for humans and shifts with long command names; -F emits one
// prefixed field per line and is documented as stable. Records are grouped: a
// `p` line opens a process, `c` names it, and each `n` line is one socket it
// holds, so state carries down the lines rather than each line standing alone.
// One process commonly holds the SAME port on several addresses at once --
// 127.0.0.1:8444, 192.168.0.7:8444, 100.90.239.81:8444 are three lines for one
// server. They collapse to a single entry, and Local is true only when EVERY
// address for that port is loopback. Taking the first line's answer instead
// would call a server "local only" because loopback happened to be listed first,
// and kunai would then forward something already reachable.
func ParseLSOF(out string) []Server {
	var order []int
	byPort := map[int]*Server{}
	var pid int
	var command string

	for _, line := range strings.Split(out, "\n") {
		if len(line) < 2 {
			continue
		}
		tag, val := line[0], strings.TrimSpace(line[1:])
		switch tag {
		case 'p':
			pid, _ = strconv.Atoi(val)
			command = ""
		case 'c':
			command = val
		case 'n':
			if pid == selfPID {
				continue // kunai's own socket, including a forward it is holding
			}
			addr, ok := parseListenAddr(val)
			if !ok {
				continue
			}
			if s := byPort[addr.port]; s != nil {
				s.Local = s.Local && addr.loopback
				continue
			}
			byPort[addr.port] = &Server{
				Port: addr.port, Command: command, PID: pid, Local: addr.loopback,
			}
			order = append(order, addr.port)
		}
	}

	out2 := make([]Server, 0, len(order))
	for _, p := range order {
		out2 = append(out2, *byPort[p])
	}
	return out2
}

type listenAddr struct {
	port     int
	loopback bool
}

// parseListenAddr reads lsof's name field for a listening socket.
//
// The shapes are "127.0.0.1:3000", "*:8080", "[::1]:3000" and occasionally
// "1.2.3.4:80->5.6.7.8:90" for a connection, which is not a listener and is
// dropped. Anything unrecognised is dropped too: a preview offered for something
// that is not there wastes a tap, and there is no cost to missing one.
func parseListenAddr(s string) (listenAddr, bool) {
	if strings.Contains(s, "->") {
		return listenAddr{}, false
	}
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return listenAddr{}, false
	}
	port, err := strconv.Atoi(s[i+1:])
	if err != nil || port <= 0 || port > 65535 {
		return listenAddr{}, false
	}
	host := strings.Trim(s[:i], "[]")
	return listenAddr{port: port, loopback: isLoopbackHost(host)}, true
}

func isLoopbackHost(h string) bool {
	return h == "127.0.0.1" || h == "::1" || h == "localhost" || strings.HasPrefix(h, "127.")
}

// ParseProcessTree reads `ps -Ao pid=,ppid=` into a child->parent map.
//
// One call for the whole tree rather than a lookup per process: the answer is
// wanted for every listening socket at once, and walking /proc would need a
// second implementation for macOS.
func ParseProcessTree(out string) map[int]int {
	parents := make(map[int]int)
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			continue
		}
		parents[pid] = ppid
	}
	return parents
}

// maxDepth bounds the walk up the process tree. Deep enough for any real chain
// (claude -> shell -> npm -> node -> esbuild), and a hard stop so a cycle in a
// malformed table cannot hang the scan.
const maxDepth = 32

// DescendsFrom reports whether pid is root, or is descended from it.
func DescendsFrom(parents map[int]int, pid, root int) bool {
	if root == 0 || pid == 0 {
		return false
	}
	for depth := 0; depth < maxDepth; depth++ {
		if pid == root {
			return true
		}
		next, ok := parents[pid]
		if !ok || next == pid || next <= 0 {
			return false
		}
		pid = next
	}
	return false
}

// Owned returns the servers started by (a descendant of) root.
//
// Ports kunai itself is listening on are excluded by the caller rather than
// here, because this package does not know what those are.
func Owned(servers []Server, parents map[int]int, root int) []Server {
	var out []Server
	for _, s := range servers {
		if DescendsFrom(parents, s.PID, root) {
			out = append(out, s)
		}
	}
	return out
}

// OwnedBy returns the servers belonging to a session, by ancestry OR by working
// directory.
//
// Ancestry alone was the original design and it is wrong for the main case, which
// is worth writing down because it looks right until you watch it fail. The agent
// starts a dev server as a BACKGROUND command; the shell that launched it then
// exits, the server is orphaned, and the kernel reparents it to init. Its chain
// to the `claude` process is severed at that moment, so the one thing this
// feature exists to find -- a server the agent left running -- is the one thing
// ancestry cannot see. Observed on a real session: next-server -> sh ->
// (gone) -> systemd.
//
// A working directory survives that, because reparenting does not change it. It
// is a weaker signal: two sessions in the same checkout would both claim the same
// server, since it genuinely is in both their directories. That is a tolerable
// answer (a worktree, which is how kunai runs parallel work, gives each session
// its own path) and a much better one than showing nothing.
//
// Both are used, not one: ancestry is exact when the process is still attached,
// and the directory catches it once it is not.
func OwnedBy(servers []Server, parents map[int]int, root int, cwds map[int]string, sessionCwd string) []Server {
	var out []Server
	byDir := attributableDir(sessionCwd)
	for _, s := range servers {
		if isOurs(s.Command) || isTestBinary(s.Command) {
			continue // another kunai, or a test run: neither is somebody's dev server
		}
		if DescendsFrom(parents, s.PID, root) || (byDir && withinDir(cwds[s.PID], sessionCwd)) {
			out = append(out, s)
		}
	}
	return out
}

// attributableDir reports whether a directory is specific enough to say that a
// process running in it belongs to a session started there.
//
// The working-directory rule exists for one case: the agent backgrounds a dev
// server, the shell exits, the kernel reparents it to init, and its ancestry to
// `claude` is severed. Matching on the directory recovers it. But the rule is
// only as good as the directory, and a session started in the HOME directory
// claims every process on the machine, because on a personal machine nearly
// everything runs somewhere under home. Observed for real: a session opened in
// /home/ninja offered the other kunai instance's service port and both of its
// internal provider proxies as previews to share.
//
// A directory that CONTAINS projects is not one. The home directory, anything
// above it (/home, /Users), and the filesystem root are containers by
// definition, so they attribute nothing and ancestry is left to do the work
// alone. This is the same distinction the sidebar's grouping had to learn:
// ~/coding is where codebases live, not a codebase.
func attributableDir(root string) bool {
	root = strings.TrimRight(root, "/")
	if root == "" {
		return false // the filesystem root, after trimming
	}
	home := strings.TrimRight(userHome(), "/")
	if home == "" {
		return true // nothing to compare against; the old behaviour
	}
	// root is home, or an ancestor of it.
	return root != home && !withinDir(home, root)
}

// userHome is a variable so a test can pin it without depending on the machine
// it runs on.
var userHome = func() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

// withinDir reports whether a process's directory is the session's, or inside it.
//
// Compared as path segments rather than as a string prefix: "/home/me/app2"
// starts with "/home/me/app" and is a different project.
func withinDir(dir, root string) bool {
	if dir == "" || root == "" {
		return false
	}
	dir, root = strings.TrimRight(dir, "/"), strings.TrimRight(root, "/")
	if dir == root {
		return true
	}
	return strings.HasPrefix(dir, root+"/")
}

// ParseCwds reads `lsof -a -d cwd -F pn` output into pid -> working directory.
//
// The same field format as the listener scan: a `p` line opens a process and the
// `n` line that follows is its cwd.
func ParseCwds(out string) map[int]string {
	cwds := make(map[int]string)
	var pid int
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(strings.TrimSpace(line[1:]))
		case 'n':
			if pid != 0 {
				cwds[pid] = strings.TrimSpace(line[1:])
				pid = 0 // one cwd per process; ignore anything further
			}
		}
	}
	return cwds
}
