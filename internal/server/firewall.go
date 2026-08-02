package server

// Noticing that the machine's own firewall is about to swallow the thing we just
// started serving.
//
// This exists because of a specific, wasted afternoon. The network listener came
// up, logged its address, reported itself enabled in Settings, and answered
// perfectly when asked from the machine itself -- while ufw dropped every packet
// arriving from the wifi. The other device saw a connection time out, with
// nothing anywhere to say why, and the local test that would have caught it does
// not: a request from this machine to its own address is routed internally and
// never crosses the network at all.
//
// So the check cannot be "can I reach myself". It is instead "is there a firewall
// here that defaults to dropping", which is answerable from files any user can
// read, and it is reported as a caution rather than a fact: the rules themselves
// need root to list, so kunai cannot know whether the port is already allowed.
// Saying "if you cannot reach it, this is why" is both honest and enough.

import (
	"os"
	"strings"
)

// firewallAdvice is what to tell somebody who cannot reach the port, or "" when
// nothing on this machine suggests a firewall is in the way.
type firewallAdvice struct {
	// Tool names what was found ("ufw", "firewalld"), for the log line.
	Tool string
	// Command is the exact thing to run, scoped to the local network rather than
	// opening the port on every interface.
	Command string
}

// detectFirewall reports a likely inbound-blocking firewall on this host.
func detectFirewall(port string) *firewallAdvice {
	if ufwDrops("/etc/ufw/ufw.conf", "/etc/default/ufw") {
		return &firewallAdvice{
			Tool:    "ufw",
			Command: "sudo ufw allow from 192.168.0.0/16 to any port " + port + " proto tcp",
		}
	}
	if firewalldRunning() {
		return &firewallAdvice{
			Tool:    "firewalld",
			Command: "sudo firewall-cmd --add-port=" + port + "/tcp --permanent && sudo firewall-cmd --reload",
		}
	}
	return nil
}

// ufwDrops reports whether ufw is enabled AND defaults to dropping inbound.
//
// Both halves matter: an enabled firewall whose default is ACCEPT is not the
// problem, and a DROP policy in a config file that is switched off is not either.
// Paths are parameters so the parsing can be tested against fixtures.
func ufwDrops(confPath, defaultsPath string) bool {
	return confValue(confPath, "ENABLED") == "yes" &&
		strings.EqualFold(confValue(defaultsPath, "DEFAULT_INPUT_POLICY"), "drop")
}

// confValue reads a KEY=VALUE line from a shell-style config file, unquoted.
// Missing file, missing key and commented-out keys all read as absent.
func confValue(path, key string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		name, val, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(name) != key {
			continue
		}
		return strings.Trim(strings.TrimSpace(val), `"'`)
	}
	return ""
}

// firewalldRunning is a cheap presence check. firewalld's own state needs root to
// query properly, so this only notices that it is installed and has a running
// unit, which is enough for a caution.
func firewalldRunning() bool {
	if _, err := os.Stat("/etc/firewalld"); err != nil {
		return false
	}
	// A running systemd unit leaves this behind; absence is not proof of much,
	// which is why the advice is phrased conditionally either way.
	if _, err := os.Stat("/run/firewalld.pid"); err == nil {
		return true
	}
	_, err := os.Stat("/var/run/firewalld.pid")
	return err == nil
}
