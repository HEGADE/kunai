package usagestats

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// The incremental index.
//
// A first scan of a real corpus takes tens of seconds; every scan after it takes
// milliseconds, because a transcript only ever grows and the index remembers how
// far into each one it has already read. What is stored per file is the offset
// plus the (day, model) rows derived from everything before it -- never the
// transcript's own bytes -- so the index stays a few hundred KB against gigabytes
// of source.
//
// The offset is validated against the file's size on every pass. A file that
// SHRANK was replaced rather than appended to (the account-switch copy does this,
// and so does a `/usage` poll's transcript being deleted), so its offset is
// meaningless and it is rescanned from zero. Trusting a stale offset there would
// silently attribute one session's spend to another's bytes.

// fileState is one transcript's place in the index.
type fileState struct {
	Size int64 `json:"size"`
	Off  int64 `json:"off"`
	Rows []Row `json:"rows"`
}

// Collector owns the index and the report built from it.
type Collector struct {
	path  string // where the index persists
	roots func() []Root

	mu       sync.Mutex
	files    map[string]*fileState
	report   *Report
	scanning bool
	scanned  time.Time
}

// Root is one account's transcripts folder, named so a report can say which
// account spent what.
type Root struct {
	Name string
	Dir  string
}

// New builds a collector. roots is a function rather than a slice because the
// account list can change under a running server (an account added from the app),
// and a usage page that never sees the new account's spend would be wrong in a
// way nobody would think to check.
func New(dataDir string, roots func() []Root) *Collector {
	c := &Collector{roots: roots, files: map[string]*fileState{}}
	if dataDir != "" {
		c.path = filepath.Join(dataDir, "usage-index.json")
		c.load()
	}
	return c
}

func (c *Collector) load() {
	b, err := os.ReadFile(c.path)
	if err != nil {
		return
	}
	var files map[string]*fileState
	if json.Unmarshal(b, &files) == nil && files != nil {
		c.files = files
	}
}

func (c *Collector) save() {
	if c.path == "" {
		return
	}
	b, err := json.Marshal(c.files)
	if err != nil {
		return
	}
	tmp := c.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		os.Rename(tmp, c.path)
	}
}

// Status is what a client learns while a scan is still running.
type Status struct {
	Scanning  bool  `json:"scanning"`
	ScannedAt int64 `json:"scanned_at,omitempty"`
}

// Report returns the last computed report, which is nil until the first scan
// finishes, plus whether one is running now.
func (c *Collector) Report() (*Report, Status) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := Status{Scanning: c.scanning}
	if !c.scanned.IsZero() {
		st.ScannedAt = c.scanned.UnixMilli()
	}
	return c.report, st
}

// Stale reports whether the report is old enough to be worth rebuilding.
func (c *Collector) Stale(d time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.scanning && (c.report == nil || time.Since(c.scanned) > d)
}

// Refresh brings the index up to date and rebuilds the report. It is safe to
// call concurrently: a second caller returns immediately rather than scanning the
// same gigabytes twice.
func (c *Collector) Refresh() {
	c.mu.Lock()
	if c.scanning {
		c.mu.Unlock()
		return
	}
	c.scanning = true
	// Copy the index out, scan without the lock held, then merge back. A first
	// scan runs for tens of seconds and holding the mutex through it would block
	// every read of the previous report -- turning "the page is a little stale"
	// into "the page hangs".
	snapshot := make(map[string]*fileState, len(c.files))
	for k, v := range c.files {
		snapshot[k] = v
	}
	roots := c.roots()
	c.mu.Unlock()

	fresh := scanRoots(roots, snapshot)
	report := build(fresh)

	c.mu.Lock()
	c.files = fresh
	c.report = report
	c.scanned = time.Now()
	c.scanning = false
	c.save()
	c.mu.Unlock()
}

// scanRoots walks every account's transcripts, reading only what is new in each.
//
// The returned index contains ONLY the files still on disk, so a transcript that
// has gone (deleted, or its account removed) drops out rather than lingering as
// spend that can never be corrected. The `/usage` poll deletes one of these every
// minute, so this is a live case rather than a hypothetical.
func scanRoots(roots []Root, index map[string]*fileState) map[string]*fileState {
	out := make(map[string]*fileState, len(index))

	for _, r := range roots {
		dirs, err := os.ReadDir(r.Dir)
		if err != nil {
			continue
		}
		for _, d := range dirs {
			if !d.IsDir() {
				continue
			}
			entries, err := os.ReadDir(filepath.Join(r.Dir, d.Name()))
			if err != nil {
				continue
			}
			for _, e := range entries {
				if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
					continue
				}
				path := filepath.Join(r.Dir, d.Name(), e.Name())
				info, err := e.Info()
				if err != nil {
					continue
				}
				st := index[path]
				if st == nil || info.Size() < st.Size {
					// New, or rewritten shorter than we had read: start over.
					st = &fileState{}
				}
				if info.Size() == st.Size {
					out[path] = st
					continue // nothing appended since the last pass
				}
				buckets, off, err := ScanFile(path, st.Off)
				if err != nil {
					out[path] = st
					continue
				}
				out[path] = &fileState{Size: info.Size(), Off: off, Rows: mergeRows(st.Rows, buckets)}
			}
		}
	}
	return out
}

// mergeRows folds a scan's new buckets into a file's existing rows.
func mergeRows(rows []Row, add map[Key]Tokens) []Row {
	if len(add) == 0 {
		return rows
	}
	byKey := make(map[Key]Tokens, len(rows)+len(add))
	for _, r := range rows {
		byKey[r.Key] = r.Tokens
	}
	for k, t := range add {
		cur := byKey[k]
		cur.Add(t)
		byKey[k] = cur
	}
	out := make([]Row, 0, len(byKey))
	for k, t := range byKey {
		out = append(out, Row{Key: k, Tokens: t})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Day != out[j].Day {
			return out[i].Day < out[j].Day
		}
		return out[i].Model < out[j].Model
	})
	return out
}
