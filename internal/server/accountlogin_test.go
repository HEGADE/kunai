package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in (spawns a real `claude auth login`, cancelled before completion): proves
// the PTY spawn + URL scrape work against the actual CLI. KUNAI_E2E=1 to run.
func TestAccountLoginStartCapturesURL(t *testing.T) {
	if os.Getenv("KUNAI_E2E") == "" {
		t.Skip("opt-in: set KUNAI_E2E=1 to spawn a real claude auth login")
	}
	m := newLoginManager("claude", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	id, url, dir, err := m.start(ctx, "Work Test")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.cancel(id)
	t.Logf("flow id=%s dir=%s", id, dir)
	t.Logf("url=%s", url)
	if !strings.Contains(url, "oauth") || !strings.HasPrefix(url, "https://") {
		t.Fatalf("url = %q, want an https oauth link", url)
	}
}

func TestAccountSlug(t *testing.T) {
	cases := map[string]string{"Work": "work", "My Work Acct!": "my-work-acct", "  a  b ": "a-b", "": ""}
	for in, want := range cases {
		if got := accountSlug(in); got != want {
			t.Errorf("accountSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCopyFileSelfCopyPreserves guards the data-loss bug where copying a
// transcript onto itself truncated it to zero (os.Create truncates first). A
// self-copy must be a no-op that keeps the content intact.
func TestCopyFileSelfCopyPreserves(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/t.jsonl"
	want := "line one\nline two\n"
	if err := os.WriteFile(p, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(p, p); err != nil {
		t.Fatalf("self-copy: %v", err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("self-copy truncated the file: got %q, want %q", got, want)
	}
}

// TestCopyFileNormal copies to a distinct path (the real switch case).
func TestCopyFileNormal(t *testing.T) {
	dir := t.TempDir()
	src, dst := dir+"/a.jsonl", dir+"/sub/b.jsonl"
	want := "hello\n"
	if err := os.WriteFile(src, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copy: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("copy = %q, want %q", got, want)
	}
	// The source must be untouched.
	if s, _ := os.ReadFile(src); string(s) != want {
		t.Fatalf("source changed: %q", s)
	}
}

// writeTranscriptAt drops a transcript for cid in an account's projects folder.
func writeTranscriptAt(t *testing.T, configDir, slug, cid, body string) string {
	t.Helper()
	dir := filepath.Join(configDir, "projects", slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, cid+".jsonl")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// The regression that lost a real 80MB conversation: a session running on the
// work account is switched to the default one, and BOTH accounts already hold a
// copy of the transcript (the default's is stale from an earlier switch). The
// source must be the account the session is on now, so the live work transcript
// wins; sourcing it from the default account instead copied a stale file over the
// good one, and when the paths coincided it truncated it to zero.
func TestSwitchSourcesTranscriptFromCurrentAccount(t *testing.T) {
	work, personal := t.TempDir(), t.TempDir()
	const cid, slug = "sess-1", "-home-me-proj"
	const live = "turn1\nturn2\nturn3-done-on-work\n"
	writeTranscriptAt(t, work, slug, cid, live)
	writeTranscriptAt(t, personal, slug, cid, "stale\n") // an older copy, must be overwritten

	dst, err := stageTranscriptForSwitch(work, personal, cid, func() string {
		t.Fatal("fallback must not run: the current account has the transcript")
		return ""
	})
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != live {
		t.Fatalf("target got %q, want the live work transcript %q", got, live)
	}
	// The source must survive the switch untouched.
	if s, _ := os.ReadFile(filepath.Join(work, "projects", slug, cid+".jsonl")); string(s) != live {
		t.Fatalf("source transcript damaged: %q", s)
	}
}

// Switching when the source resolves to the target's own file (the same account,
// or a lookup that landed on the target) must leave the transcript intact. This
// is the exact shape that truncated a live conversation to 0 bytes.
func TestSwitchToSameAccountPreservesTranscript(t *testing.T) {
	dir := t.TempDir()
	const cid, slug = "sess-2", "-home-me-proj"
	const body = "the whole conversation\n"
	writeTranscriptAt(t, dir, slug, cid, body)

	if _, err := stageTranscriptForSwitch(dir, dir, cid, nil); err != nil {
		t.Fatalf("stage: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "projects", slug, cid+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("transcript truncated by a self-directed switch: got %q, want %q", got, body)
	}
}

// With no copy in the current account, the cross-account fallback supplies the
// source (an id assigned before anything was flushed to the new account's dir).
func TestSwitchUsesFallbackWhenCurrentAccountHasNoCopy(t *testing.T) {
	other, target := t.TempDir(), t.TempDir()
	empty := t.TempDir()
	const cid, slug = "sess-3", "-home-me-proj"
	const body = "found via fallback\n"
	src := writeTranscriptAt(t, other, slug, cid, body)

	dst, err := stageTranscriptForSwitch(empty, target, cid, func() string { return src })
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	if got, _ := os.ReadFile(dst); string(got) != body {
		t.Fatalf("got %q, want %q", got, body)
	}
}

// Nothing anywhere is not an error: a brand-new session has no transcript yet and
// the switch must still proceed to the respawn.
func TestSwitchWithNoTranscriptIsNotAnError(t *testing.T) {
	dst, err := stageTranscriptForSwitch(t.TempDir(), t.TempDir(), "nope", nil)
	if err != nil || dst != "" {
		t.Fatalf("got (%q, %v), want (\"\", nil)", dst, err)
	}
}

// A failed copy must leave the destination as it was rather than a truncated
// stub, which a resume would load as an empty conversation. copyFile writes to a
// temp file and renames, so an unreadable source can never clobber the target.
func TestCopyFileFailureLeavesDestinationIntact(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "dst.jsonl")
	const keep = "existing conversation\n"
	if err := os.WriteFile(dst, []byte(keep), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(filepath.Join(dir, "missing.jsonl"), dst); err == nil {
		t.Fatal("copy from a missing source should fail")
	}
	if got, _ := os.ReadFile(dst); string(got) != keep {
		t.Fatalf("destination damaged by a failed copy: %q", got)
	}
	if _, err := os.Stat(dst + ".tmp"); err == nil {
		t.Fatal("a temp file was left behind")
	}
}

// A staged transcript must be an INDEPENDENT file, whichever path copyFile took
// (a CoW clone on btrfs/XFS, a byte copy on ext4). This is the property that
// makes reflink a safe substitute for copying and a hard link an unsafe one:
// appending to one account's transcript must never change the other's.
func TestCopyFileProducesIndependentFile(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src.jsonl"), filepath.Join(dir, "dst.jsonl")
	const original = "turn-one\nturn-two\n"
	if err := os.WriteFile(src, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copy: %v", err)
	}

	// Append to the destination, as the CLI would after a switch.
	f, err := os.OpenFile(dst, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("turn-three-on-the-new-account\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	got, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("source changed when the copy was appended to (hard link?): %q", got)
	}
	// And the destination kept both.
	d, _ := os.ReadFile(dst)
	if len(d) <= len(original) {
		t.Fatalf("destination lost its appended turn: %q", d)
	}
}

// Whether or not this filesystem supports cloning, copyFile must produce byte
// identical content. On ext4 reflinkFile fails and the buffered copy runs; on
// btrfs/XFS the clone runs. Both are exercised by whatever fs the tests land on,
// and the assertion is the same.
func TestCopyFileContentMatchesOnEitherPath(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "big.jsonl"), filepath.Join(dir, "out.jsonl")
	// Larger than the 1MB copy buffer so the chunked path loops.
	body := strings.Repeat("{\"type\":\"user\",\"cwd\":\"/x\"}\n", 80000)
	if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copy: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("copied content differs: %d bytes, want %d", len(got), len(body))
	}

	// Report which path this filesystem actually took, so the run is honest about
	// what it proved here.
	in, _ := os.Open(src)
	defer in.Close()
	probe := filepath.Join(dir, "probe")
	out, _ := os.Create(probe)
	defer out.Close()
	if err := reflinkFile(in, out); err != nil {
		t.Logf("filesystem does not support reflink (%v): exercised the copy fallback", err)
	} else {
		t.Log("filesystem supports reflink: exercised the clone path")
	}
}

// A clone or copy of an empty source is still a valid, empty destination rather
// than an error, and must not leave a temp file behind.
func TestCopyFileEmptySource(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "empty.jsonl"), filepath.Join(dir, "out.jsonl")
	if err := os.WriteFile(src, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if fi, err := os.Stat(dst); err != nil || fi.Size() != 0 {
		t.Fatalf("stat dst: %v size=%v", err, fi)
	}
	if _, err := os.Stat(dst + ".tmp"); err == nil {
		t.Fatal("temp file left behind")
	}
}
