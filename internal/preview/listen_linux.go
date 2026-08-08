package preview

// Listening sockets, read from /proc instead of from lsof.
//
// The lsof scan was the whole feature's data source, and on this machine it went
// blind. A real dev server -- next-server, pid 10518, holding *:3000, owned by
// the same user kunai runs as -- was reported by `ss`, was present in
// /proc/net/tcp6 with its inode and uid, and had a readable /proc/10518/fd, and
// `lsof -p 10518` still returned zero lines across repeated calls while cheerfully
// listing kunai's own sockets in the same breath. The preview card was therefore
// empty for exactly the case the feature exists to cover, and nothing in the
// attribution logic was wrong: it was being handed nothing to attribute.
//
// So Linux stops asking a third-party binary a question the kernel will answer
// directly. This is the same call previewcwd.go already made for the working
// directory, and for the same stated reason -- "a scan that depends on one tool's
// build options is a scan that stops working for somebody". This is that
// prediction coming true, in the other half of the scan.
//
// It is also strictly better than the subprocess it replaces: no exec, no parse
// of a human-oriented format, no 6-second timeout to bound, and it sees every
// socket the kernel will admit to. macOS has no /proc and keeps the lsof path.

import (
	"encoding/hex"
	"net"
	"os"
	"strconv"
	"strings"
)

// Listeners returns every listening TCP socket this process can attribute to a
// pid, and whether the /proc read worked at all. ok=false means fall back to
// lsof rather than conclude the machine is idle -- the two are not the same
// answer, and confusing them is what made the old cache drop live peers.
func Listeners() ([]Server, bool) {
	socks := map[uint64]listenAddr{}
	read := false
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		read = true
		for inode, addr := range ParseProcNet(string(b)) {
			socks[inode] = addr
		}
	}
	if !read {
		return nil, false
	}
	return attribute(socks, ownerOf(socks)), true
}

// ParseProcNet reads /proc/net/tcp or /proc/net/tcp6 into inode -> address, for
// listening sockets only.
//
// The address is hex, and each four-byte group is in host order, so it is byte
// -swapped per word rather than read straight through. Decoding it into a real
// net.IP (rather than string-matching "0100007F" and friends) is what makes the
// loopback test correct for ::1 and for an IPv4-mapped address without a special
// case for each.
func ParseProcNet(data string) map[uint64]listenAddr {
	out := map[uint64]listenAddr{}
	for i, line := range strings.Split(data, "\n") {
		if i == 0 {
			continue // header
		}
		f := strings.Fields(line)
		// sl local_address rem_address st tx:rx tr:when retrnsmt uid timeout inode
		if len(f) < 10 {
			continue
		}
		// 0A is TCP_LISTEN. Everything else is a connection, not a server.
		if f[3] != "0A" {
			continue
		}
		host, port, ok := splitHexAddr(f[1])
		if !ok {
			continue
		}
		inode, err := strconv.ParseUint(f[9], 10, 64)
		if err != nil || inode == 0 {
			continue
		}
		out[inode] = listenAddr{port: port, loopback: host.IsLoopback()}
	}
	return out
}

// splitHexAddr turns "00000000000000000000000000000000:0BB8" into an IP and a
// port.
func splitHexAddr(s string) (net.IP, int, bool) {
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return nil, 0, false
	}
	port, err := strconv.ParseUint(s[i+1:], 16, 32)
	if err != nil || port == 0 || port > 65535 {
		return nil, 0, false
	}
	ip := parseHexIP(s[:i])
	if ip == nil {
		return nil, 0, false
	}
	return ip, int(port), true
}

// parseHexIP decodes /proc's address encoding: hex bytes, little-endian within
// each 32-bit word.
func parseHexIP(s string) net.IP {
	b, err := hex.DecodeString(s)
	if err != nil || (len(b) != 4 && len(b) != 16) {
		return nil
	}
	for i := 0; i+4 <= len(b); i += 4 {
		b[i], b[i+1], b[i+2], b[i+3] = b[i+3], b[i+2], b[i+1], b[i]
	}
	return net.IP(b)
}

// ownerOf maps socket inodes to the pid holding them, by walking /proc/<pid>/fd.
//
// This is what `ss -p` does. A pid whose fds cannot be read (another user's) is
// skipped rather than treated as an error: kunai only ever cares about servers
// its own agent started, which are its own user's.
func ownerOf(socks map[uint64]listenAddr) map[uint64]int {
	owners := make(map[uint64]int, len(socks))
	if len(socks) == 0 {
		return owners
	}
	procs, err := os.ReadDir("/proc")
	if err != nil {
		return owners
	}
	for _, p := range procs {
		if !p.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(p.Name())
		if err != nil {
			continue
		}
		fds, err := os.ReadDir("/proc/" + p.Name() + "/fd")
		if err != nil {
			continue // not ours, or gone between the listing and the read
		}
		for _, fd := range fds {
			target, err := os.Readlink("/proc/" + p.Name() + "/fd/" + fd.Name())
			if err != nil {
				continue
			}
			inode, ok := socketInode(target)
			if !ok {
				continue
			}
			if _, want := socks[inode]; want {
				owners[inode] = pid
			}
		}
	}
	return owners
}

// socketInode reads the "socket:[12345]" form an fd symlink takes.
func socketInode(target string) (uint64, bool) {
	const prefix = "socket:["
	if !strings.HasPrefix(target, prefix) || !strings.HasSuffix(target, "]") {
		return 0, false
	}
	n, err := strconv.ParseUint(target[len(prefix):len(target)-1], 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// attribute collapses the sockets into one entry per port.
//
// One server routinely holds the same port on several addresses at once
// (127.0.0.1:3000 and [::]:3000 are two rows for one next-server), so they
// collapse, and Local stays true only when EVERY address for that port is
// loopback -- the same rule ParseLSOF has, for the same reason: taking the first
// row's answer would call a server local-only because loopback happened to be
// listed first, and kunai would then forward something already reachable.
func attribute(socks map[uint64]listenAddr, owners map[uint64]int) []Server {
	byPort := map[int]*Server{}
	var order []int
	for inode, addr := range socks {
		pid, ok := owners[inode]
		if !ok {
			continue // a socket we cannot pin to a process is not attributable
		}
		if s := byPort[addr.port]; s != nil {
			s.Local = s.Local && addr.loopback
			continue
		}
		byPort[addr.port] = &Server{
			Port: addr.port, Command: commOf(pid), PID: pid, Local: addr.loopback,
		}
		order = append(order, addr.port)
	}
	sortInts(order)
	out := make([]Server, 0, len(order))
	for _, p := range order {
		out = append(out, *byPort[p])
	}
	return out
}

// commOf is the short process name, for a human to recognise ("node", "vite").
func commOf(pid int) string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// sortInts keeps the port order stable, since a map walk is not. Without it the
// preview card reshuffles itself on every poll.
func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}
