package server

// Writing the /kunai slash command, so the terminal handoff exists everywhere
// the endpoint does.
//
// These files were written by install.sh, and that shipped a feature nobody
// could reach: a self-update swaps the binary and never runs the installer, so
// every machine that updated (rather than reinstalled) had the /api/handoff
// endpoint and no /kunai command to call it. The server knows everything the
// installer knew -- its own public URL and data dir -- so it writes them at
// boot, and an update alone is enough.
//
// Rewritten every boot, deliberately: the command must track the binary it
// talks to, and the public URL can change (a machine renamed, a port moved).
// On a machine running two kunai instances (stable and nightly) the last one
// to boot owns the command file; both handoff endpoints resolve the same
// transcripts, so a handoff through either lands on the same conversation.

import (
	"log"
	"os"
	"path/filepath"
)

// writeHandoffCommand installs /kunai for the terminal `claude` to find. Best
// effort: a machine where it cannot be written (no home dir, read-only) just
// goes without the command, never without the server.
func writeHandoffCommand(dataDir, publicURL string) {
	if dataDir == "" || publicURL == "" {
		return // dev runs; the command would point at nothing durable
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	// The server's own URL, read by handoff.sh at run time rather than baked into
	// it, so a URL change needs no rewrite of the script.
	if err := os.WriteFile(filepath.Join(dataDir, "url"), []byte(publicURL+"\n"), 0o600); err != nil {
		log.Printf("handoff: could not record the server URL: %v", err)
		return
	}

	script := `#!/bin/sh
# Written by kunai on boot. Hands this terminal session to kunai.
set -u
base=${KUNAI_URL:-$(cat "` + dataDir + `/url" 2>/dev/null)}
[ -n "${base:-}" ] || { echo "kunai: no server URL; set KUNAI_URL"; exit 1; }
id=${CLAUDE_CODE_SESSION_ID:-}
[ -n "$id" ] || { echo "kunai: run this inside a Claude Code session"; exit 1; }
body=$(printf '{"session_id":"%s","cwd":"%s"}' "$id" "$PWD")
out=$(curl -sk --max-time 10 -X POST "$base/api/handoff" -H "content-type: application/json" -d "$body")
url=$(printf %s "$out" | sed -n 's/.*"url":"\([^"]*\)".*/\1/p')
if [ -z "$url" ]; then
  # Report what kunai said, not the JSON it said it in: the message is written
  # for a person and the braces only get in the way.
  msg=$(printf %s "$out" | sed -n 's/.*"error":"\([^"]*\)".*/\1/p')
  [ -n "$msg" ] || msg="kunai did not answer (is it running at $base?)"
  echo "kunai: $msg"
  exit 1
fi
# Straight to the terminal as well as to stdout, so the link survives the
# exit below even though the CLI captures this command's output.
( printf "\nContinue here: %s\n\n" "$url" > /dev/tty ) 2>/dev/null || true
echo "$url"
# Best effort, and never fatal: a headless box has no browser and printing
# the link is the whole fallback.
for o in xdg-open open; do command -v "$o" >/dev/null 2>&1 && { "$o" "$url" >/dev/null 2>&1 & break; }; done
# Leave after the turn has rendered. The resume happens when the link is
# opened, so this only has to be gone before somebody clicks.
[ -n "${CLAUDE_PID:-}" ] && (sleep 2; kill "$CLAUDE_PID" >/dev/null 2>&1) &
exit 0
`
	scriptPath := filepath.Join(dataDir, "handoff.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		log.Printf("handoff: could not write %s: %v", scriptPath, err)
		return
	}

	cmdDir := filepath.Join(home, ".claude", "commands")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		log.Printf("handoff: could not create %s: %v", cmdDir, err)
		return
	}
	cmd := `---
description: Continue this session in kunai, and close the terminal
allowed-tools: Bash(sh:*)
---

!` + "`sh " + scriptPath + "`" + `

Report the link above to the user in one short line and nothing else.
Do not offer to do anything further: this terminal is about to exit.
`
	if err := os.WriteFile(filepath.Join(cmdDir, "kunai.md"), []byte(cmd), 0o644); err != nil {
		log.Printf("handoff: could not write the /kunai command: %v", err)
	}
}
