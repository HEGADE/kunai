package usagestats

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// line builds one assistant transcript record with the usage shape the real CLI
// writes, including the nested cache_creation split.
func line(ts, model string, in, w5, w1, read, out int64) string {
	return `{"type":"assistant","timestamp":"` + ts + `","message":{"model":"` + model +
		`","usage":{"input_tokens":` + itoa(in) +
		`,"cache_creation_input_tokens":` + itoa(w5+w1) +
		`,"cache_read_input_tokens":` + itoa(read) +
		`,"output_tokens":` + itoa(out) +
		`,"cache_creation":{"ephemeral_5m_input_tokens":` + itoa(w5) +
		`,"ephemeral_1h_input_tokens":` + itoa(w1) + `}}}}` + "\n"
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// A day is the LOCAL calendar day, so build the timestamp from local noon rather
// than hardcoding a UTC string that would land on the previous day west of
// Greenwich and break this test for half the planet.
func stamp(day time.Time) string {
	return time.Date(day.Year(), day.Month(), day.Day(), 12, 0, 0, 0, time.Local).
		UTC().Format(time.RFC3339)
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func append_(t *testing.T, path, body string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(body); err != nil {
		t.Fatal(err)
	}
}

func TestScanBucketsByDayAndModel(t *testing.T) {
	now := time.Now()
	p := filepath.Join(t.TempDir(), "a.jsonl")
	write(t, p, line(stamp(now), "claude-opus-5", 10, 100, 0, 1000, 5)+
		line(stamp(now), "claude-opus-5", 20, 0, 200, 2000, 7)+
		line(stamp(now), "claude-sonnet-5", 1, 0, 0, 0, 1))

	b, off, err := ScanFile(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The offset lands after the last complete record, which is one byte short of
	// the file when it ends in a newline. What matters is not the exact number but
	// that resuming from it finds nothing: the scan is idempotent, so a rescan of
	// an idle transcript can never double-count it.
	if again, _, err := ScanFile(p, off); err != nil || len(again) != 0 {
		t.Fatalf("rescan from %d found %d buckets (err %v), want none", off, len(again), err)
	}
	day := now.Format("2006-01-02")
	got := b[Key{Day: day, Model: "claude-opus-5"}]
	want := Tokens{Input: 30, CacheWrite5m: 100, CacheWrite1h: 200, CacheRead: 3000, Output: 12, Responses: 2}
	if got != want {
		t.Errorf("opus bucket = %+v, want %+v", got, want)
	}
	if n := len(b); n != 2 {
		t.Errorf("got %d buckets, want 2 (one per model)", n)
	}
}

// The whole point of the offset: a second scan reads only what was appended, and
// never double-counts what it already saw.
func TestScanResumesFromOffset(t *testing.T) {
	now := time.Now()
	day := now.Format("2006-01-02")
	p := filepath.Join(t.TempDir(), "a.jsonl")
	write(t, p, line(stamp(now), "claude-opus-5", 10, 0, 0, 0, 1))

	_, off, err := ScanFile(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	append_(t, p, line(stamp(now), "claude-opus-5", 7, 0, 0, 0, 2))

	b, _, err := ScanFile(p, off)
	if err != nil {
		t.Fatal(err)
	}
	got := b[Key{Day: day, Model: "claude-opus-5"}]
	if got.Input != 7 || got.Responses != 1 {
		t.Errorf("resumed scan = %+v, want only the appended record (input 7, 1 response)", got)
	}
}

// A transcript is appended to while this runs, so the last line is routinely
// half-written. The offset must stop before it, and the next scan must pick that
// record up whole rather than losing it.
func TestScanStopsBeforePartialRecord(t *testing.T) {
	now := time.Now()
	day := now.Format("2006-01-02")
	p := filepath.Join(t.TempDir(), "a.jsonl")
	full := line(stamp(now), "claude-opus-5", 10, 0, 0, 0, 1)
	partial := line(stamp(now), "claude-opus-5", 99, 0, 0, 0, 9)
	write(t, p, full+partial[:len(partial)/2])

	b, off, err := ScanFile(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := b[Key{Day: day, Model: "claude-opus-5"}]; got.Input != 10 {
		t.Errorf("input = %d, want 10: the half-written record must not be counted", got.Input)
	}
	if off > int64(len(full)) {
		t.Fatalf("offset %d ran past the last complete record at %d", off, len(full))
	}

	// Now it lands whole.
	write(t, p, full+partial)
	b2, _, err := ScanFile(p, off)
	if err != nil {
		t.Fatal(err)
	}
	if got := b2[Key{Day: day, Model: "claude-opus-5"}]; got.Input != 99 {
		t.Errorf("second scan input = %d, want 99: the completed record must arrive", got.Input)
	}
}

func TestScanSkipsSyntheticAndUsagelessRecords(t *testing.T) {
	now := time.Now()
	p := filepath.Join(t.TempDir(), "a.jsonl")
	write(t, p, line(stamp(now), "<synthetic>", 0, 0, 0, 0, 0)+
		`{"type":"user","timestamp":"`+stamp(now)+`","message":{"role":"user"}}`+"\n"+
		line(stamp(now), "claude-opus-5", 1, 0, 0, 0, 1))

	b, _, err := ScanFile(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(b); n != 1 {
		t.Fatalf("got %d buckets, want only the real assistant message", n)
	}
}

func TestCostPricesCacheTiersApart(t *testing.T) {
	// 1M plain input, 1M 5m-write, 1M 1h-write, 1M read, 1M output on Opus 5
	// ($5 in / $25 out): 5 + 6.25 + 10 + 0.5 + 25.
	got, priced := Cost("claude-opus-5", Tokens{
		Input: 1e6, CacheWrite5m: 1e6, CacheWrite1h: 1e6, CacheRead: 1e6, Output: 1e6,
	})
	if !priced {
		t.Fatal("opus-5 must be priced")
	}
	if want := 46.75; got < want-1e-9 || got > want+1e-9 {
		t.Errorf("cost = %v, want %v", got, want)
	}
}

// A dated model id resolves to its family, and the longest matching prefix wins
// so `claude-opus-4-8` never falls through to the `claude-opus-4` row.
func TestRateForMatchesLongestPrefix(t *testing.T) {
	for _, tc := range []struct {
		model string
		in    float64
	}{
		{"claude-haiku-4-5-20251001", 1},
		{"claude-opus-4-8", 5},
		{"claude-opus-4-1", 15},
	} {
		r, ok := RateFor(tc.model)
		if !ok || r.In != tc.in {
			t.Errorf("%s: rate = %+v ok=%v, want input %v", tc.model, r, ok, tc.in)
		}
	}
}

// An unknown model must be reported as unpriced, never silently folded in at a
// neighbour's rate and never presented as free.
func TestUnknownModelIsUnpricedNotFree(t *testing.T) {
	cost, priced := Cost("gpt-5.5", Tokens{Input: 1e9, Output: 1e9})
	if priced || cost != 0 {
		t.Errorf("gpt-5.5 = $%v priced=%v, want unpriced and zero", cost, priced)
	}
	if s := statsOf(map[string]Tokens{"gpt-5.5": {Input: 10}}); len(s) != 1 || s[0].Priced {
		t.Errorf("stats = %+v, want a single unpriced entry", s)
	}
}

// The index is the feature. A rescan must not re-read a file it has already
// consumed, and must pick up a file that has grown.
func TestCollectorIsIncremental(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	root := filepath.Join(dir, "projects")
	p := filepath.Join(root, "-proj", "s.jsonl")
	write(t, p, line(stamp(now), "claude-opus-5", 10, 0, 0, 0, 1))

	roots := []Root{{Name: "test", Dir: root}}
	c := New(dir, func() []Root { return roots })
	c.Refresh()

	rep, _ := c.Report()
	if len(rep.Models) != 1 || rep.Models[0].Input != 10 {
		t.Fatalf("first pass = %+v, want input 10", rep.Models)
	}

	append_(t, p, line(stamp(now), "claude-opus-5", 5, 0, 0, 0, 1))
	c.Refresh()
	rep, _ = c.Report()
	if rep.Models[0].Input != 15 {
		t.Errorf("input = %d, want 15 (10 carried in the index plus 5 appended)", rep.Models[0].Input)
	}
	if rep.Models[0].Responses != 2 {
		t.Errorf("responses = %d, want 2", rep.Models[0].Responses)
	}

	// And the index survives a restart: a fresh collector over the same dataDir
	// must not re-read what the first one already counted.
	c2 := New(dir, func() []Root { return roots })
	c2.Refresh()
	rep2, _ := c2.Report()
	if rep2.Models[0].Input != 15 {
		t.Errorf("after reload input = %d, want 15 (not double-counted, not lost)", rep2.Models[0].Input)
	}
}

// A transcript that shrank was replaced, not appended to, so its offset is
// meaningless and it must be re-read from the start.
func TestCollectorRescansTruncatedFile(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	root := filepath.Join(dir, "projects")
	p := filepath.Join(root, "-proj", "s.jsonl")
	write(t, p, line(stamp(now), "claude-opus-5", 10, 0, 0, 0, 1)+
		line(stamp(now), "claude-opus-5", 10, 0, 0, 0, 1))

	c := New(dir, func() []Root { return []Root{{Dir: root}} })
	c.Refresh()

	write(t, p, line(stamp(now), "claude-opus-5", 3, 0, 0, 0, 1))
	c.Refresh()
	rep, _ := c.Report()
	if rep.Models[0].Input != 3 {
		t.Errorf("input = %d, want 3: a shrunken file is rescanned from zero, not appended to", rep.Models[0].Input)
	}
}

// A transcript that has gone drops out of the report. The /usage poll deletes one
// of these every minute, so a leak here would accumulate forever.
func TestCollectorForgetsDeletedTranscripts(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	root := filepath.Join(dir, "projects")
	p := filepath.Join(root, "-proj", "s.jsonl")
	write(t, p, line(stamp(now), "claude-opus-5", 10, 0, 0, 0, 1))

	c := New(dir, func() []Root { return []Root{{Dir: root}} })
	c.Refresh()
	os.Remove(p)
	c.Refresh()

	rep, _ := c.Report()
	if len(rep.Models) != 0 || rep.Files != 0 {
		t.Errorf("report = %+v files=%d, want empty after the transcript was deleted", rep.Models, rep.Files)
	}
}

// One session, counted once, however many accounts it has lived on.
//
// Switching a session's account copies its whole transcript into the target
// account's folder, so a conversation that has moved around exists under every
// account it ever ran on. Counting files instead of sessions counted it once per
// copy: on the machine this was found, one session sat in seven folders and made
// up ~1.1GB of a 1.5GB corpus, and the reported spend was inflated to match.
func TestOneSessionIsCountedOncePerAccountItHasLivedOn(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	a := filepath.Join(dir, "acctA", "projects")
	b := filepath.Join(dir, "acctB", "projects")

	// The same session id under two accounts. B is the newer copy and, as a real
	// account switch produces, a superset: it carries A's turn plus its own.
	one := line(stamp(now), "claude-opus-5", 10, 0, 0, 0, 1)
	write(t, filepath.Join(a, "-proj", "same-id.jsonl"), one)
	write(t, filepath.Join(b, "-proj", "same-id.jsonl"), one+line(stamp(now), "claude-opus-5", 5, 0, 0, 0, 1))
	older := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(filepath.Join(a, "-proj", "same-id.jsonl"), older, older); err != nil {
		t.Fatal(err)
	}

	c := New(dir, func() []Root { return []Root{{Name: "A", Dir: a}, {Name: "B", Dir: b}} })
	c.Refresh()
	rep, _ := c.Report()

	if rep.Files != 1 {
		t.Errorf("scanned %d files, want 1: the older copy is contained in the newer", rep.Files)
	}
	if len(rep.Models) != 1 {
		t.Fatalf("models = %+v", rep.Models)
	}
	// 15, not 25: the newest copy alone, never it plus the copy it superseded.
	if got := rep.Models[0].Input; got != 15 {
		t.Errorf("input = %d, want 15 (the newest copy only, not both)", got)
	}
	if got := rep.Models[0].Responses; got != 2 {
		t.Errorf("responses = %d, want 2", got)
	}
}
