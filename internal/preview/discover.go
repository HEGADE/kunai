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
	"strconv"
	"strings"
)

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
	for _, s := range servers {
		if DescendsFrom(parents, s.PID, root) || withinDir(cwds[s.PID], sessionCwd) {
			out = append(out, s)
		}
	}
	return out
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
