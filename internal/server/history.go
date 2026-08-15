package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hegade/kunai/internal/claude"
	"github.com/hegade/kunai/internal/project"
	"github.com/hegade/kunai/internal/session"
)

// HistoryEntry is a past Claude Code session found on disk that can be resumed
// with --resume. Sessions survive server restarts this way.
type HistoryEntry struct {
	ID     string    `json:"id"`
	Cwd    string    `json:"cwd"`
	Title  string    `json:"title"`
	CLI    string    `json:"cli,omitempty"` // the account this session belongs to
	Mtime  time.Time `json:"mtime"`
	Pinned bool      `json:"pinned,omitempty"` // user override, merged from the metadata store
	// Workspace groups this past session in the sidebar instead of its
	// directory. Only a name the user set survives here: a closed session's
	// project list is gone with the process, so an unnamed multi-project session
	// falls back to its directory once it is history. Naming it is what makes the
	// grouping outlast the run.
	Workspace string `json:"workspace,omitempty"`
	// Repo is the main checkout when Cwd is a git worktree of it, so a past
	// worktree session still groups under the codebase it belonged to.
	Repo string `json:"repo,omitempty"`
	// Project is the codebase this session belongs to, when that is not the
	// directory it was launched from: the checkout containing Cwd, or, when Cwd
	// is a container folder holding no checkout, the one the transcript says the
	// work went into. See projectDir. Empty when there is no honest answer, and
	// the heading falls back to the folder as before.
	Project string `json:"project,omitempty"`
	// Branch is that worktree's branch, which outlives its directory name.
	Branch string `json:"branch,omitempty"`
	// Review marks a past pull-request review, so reopening one lands on its
	// findings rather than on a transcript of the reviewer talking to itself.
	// The draft outlives the session, which is the whole reason a finished review
	// is worth reopening. See Meta.Review.
	Review bool `json:"review,omitempty"`
	// SnoozedUntil and SnoozedAt (unix ms) park this session on the snoozed
	// shelf, merged from the metadata store the same way Pinned is: a snooze set
	// while the session ran must still hold once it is a transcript.
	SnoozedUntil int64 `json:"snoozed_until,omitempty"`
	SnoozedAt    int64 `json:"snoozed_at,omitempty"`
}

// claudeRoot is the transcripts folder for a Claude config dir. An empty configDir
// means the default account (~/.claude); a named account points CLAUDE_CONFIG_DIR
// (or its profile Dir) somewhere else, and its transcripts live under that.
func claudeRoot(configDir string) string {
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		configDir = filepath.Join(home, ".claude")
	}
	return filepath.Join(configDir, "projects")
}

const historyLimit = 25      // default for the sidebar/dashboard poll
const historyMaxLimit = 1000 // ceiling for the "all sessions" view

// handleHistory lists resumable past sessions, newest first, excluding ones
// that are currently live. `?limit=N` overrides the default (0 or negative uses
// the default; values above the ceiling are clamped).
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	limit := historyLimit
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = min(v, historyMaxLimit)
	}
	var over map[string]sessionMeta
	if s.sessionMeta != nil {
		over = s.sessionMeta.all()
	}
	entries := s.pastSessions(limit)
	for i := range entries {
		// A past session in a worktree still groups under its repository, so long
		// as the worktree is still there to say so.
		entries[i].Repo, entries[i].Branch = s.worktrees.identify(entries[i].Cwd)
		// A review is one the worktree store can never identify: the checkout it
		// read is swept when the review ends, so by the time it is history there
		// is nothing on disk left to ask. The record knows, and it also knows this
		// is a review at all, which is what makes reopening it land on the
		// findings instead of on a dead conversation. See tagReviewRepos.
		if s.prReviews.isReview(entries[i].ID) {
			entries[i].Review = true
			if entries[i].Repo == "" {
				entries[i].Repo = s.prReviews.repoOf(entries[i].ID)
			}
		}
		if o, ok := over[entries[i].ID]; ok {
			if o.Name != "" {
				entries[i].Title = o.Name
			}
			entries[i].Pinned = o.Pinned
			entries[i].Workspace = o.Workspace
			entries[i].SnoozedUntil = o.SnoozedUntil
			entries[i].SnoozedAt = o.SnoozedAt
		}
	}
	writeJSON(w, http.StatusOK, entries)
}

// scanHistory walks each account's <configDir>/projects/*/<sessionId>.jsonl
// transcripts, newest first, tagging every entry with the account it belongs to
// so the client can reopen it on the right one. A session id is unique, so the
// same id is never listed twice across accounts.
// keep, when non-nil, is a set of session ids that must survive the newest-N
// clamp — a pinned session stays in the list even when it is older than the last
// entry the limit would otherwise allow.
func scanHistory(live map[string]bool, limit int, roots []accountRoot, keep map[string]bool) []HistoryEntry {
	// Switching a session's account copies its transcript into the target's
	// folder, so one id can exist under several accounts. The copy its owning
	// account is still appending to is the newest, so newest mtime wins: that is
	// both the account the session last ran on and the timestamp Recent should
	// sort it by. Taking the first root instead credited whichever account was
	// listed first (always the default) and sorted by a stale copy's time, and
	// since the client sends this tag back on reopen, it also resumed the session
	// on the wrong account, seeding it from that account's older copy.
	type candidate struct {
		path  string
		cli   string
		mtime time.Time
		size  int64
	}
	best := map[string]candidate{}
	for _, ar := range roots {
		dirs, err := os.ReadDir(ar.root)
		if err != nil {
			continue
		}
		for _, d := range dirs {
			if !d.IsDir() {
				continue
			}
			files, err := os.ReadDir(filepath.Join(ar.root, d.Name()))
			if err != nil {
				continue
			}
			for _, f := range files {
				name := f.Name()
				if !strings.HasSuffix(name, ".jsonl") {
					continue
				}
				id := strings.TrimSuffix(name, ".jsonl")
				if live[id] {
					continue
				}
				info, err := f.Info()
				// A zero-byte copy is not a conversation, so it can never win over
				// a real one (a truncated stage once hid a session entirely).
				if err != nil || info.Size() == 0 {
					continue
				}
				if cur, ok := best[id]; ok {
					if info.ModTime().Before(cur.mtime) {
						continue
					}
					// Timestamps can tie: the switch copies a transcript and both
					// sides are then touched within the same second, and some
					// filesystems only keep coarse times anyway. A transcript is
					// append-only, so on a tie the longer copy is the one that ran
					// further. (Seen for real: two copies of one session sharing a
					// timestamp and differing by 16KB.)
					if info.ModTime().Equal(cur.mtime) && info.Size() <= cur.size {
						continue
					}
				}
				best[id] = candidate{
					path:  filepath.Join(ar.root, d.Name(), name),
					cli:   ar.name,
					mtime: info.ModTime(),
					size:  info.Size(),
				}
			}
		}
	}
	// Read each session once, from the copy that won: probing every copy would
	// multiply the expensive part of this scan by the number of accounts.
	out := make([]HistoryEntry, 0, len(best))
	for id, c := range best {
		p := probeTranscript(c.path)
		if p.Cwd == "" {
			continue
		}
		// Nobody ever asked this session anything, so it is not a conversation and
		// Recent is not where it belongs.
		//
		// Two things produce these and both are kunai's own doing. A session
		// created and never prompted -- opened to check something, or spawned by a
		// test -- leaves a transcript with a cwd and no user turn. And the /usage
		// poll shells `claude -p` once a minute in kunai's data dir; it deletes the
		// transcript it makes, but a delete that loses the race leaves one behind,
		// and a minute later there is another. Both showed up in Recent titled
		// after their folder, because the title falls back to the directory name
		// when there is no prompt to name them by -- which is the tell, and now the
		// rule. Filtering on "was anything ever asked" catches every way this
		// happens, including ones nobody has thought of, where filtering on a known
		// folder would only catch the two above.
		if !p.Asked {
			continue
		}
		title := p.Title
		if title == "" {
			title = filepath.Base(p.Cwd)
		}
		out = append(out, HistoryEntry{
			ID:      id,
			Cwd:     p.Cwd,
			Title:   title,
			Project: projectDir(p.Cwd, p.Dirs),
			CLI:     c.cli,
			Mtime:   c.mtime,
		})
	}
	// Map order is random, so equal timestamps need a tiebreak or the list would
	// reshuffle between polls.
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Mtime.Equal(out[j].Mtime) {
			return out[i].Mtime.After(out[j].Mtime)
		}
		return out[i].ID < out[j].ID
	})
	if limit > 0 && len(out) > limit {
		head := out[:limit]
		// A pinned session past the cutoff is appended so a pin is never hidden by
		// the newest-N window; both slices stay mtime-sorted.
		if len(keep) > 0 {
			inHead := make(map[string]bool, len(head))
			for _, e := range head {
				inHead[e.ID] = true
			}
			for _, e := range out[limit:] {
				if keep[e.ID] && !inHead[e.ID] {
					head = append(head, e)
				}
			}
		}
		out = head
	}
	return out
}

// projectDir reports the codebase a session belongs to, which is not always the
// directory it was launched from.
//
// The sidebar groups by project, and it grouped by cwd, so a session started in
// ~/coding got a heading called "coding" -- a folder that holds every codebase
// on the machine and is not one itself. Twelve of twenty-five rows here sat
// under ~, ~/coding or /tmp on that rule.
//
// The obvious fix is to hide sessions whose folder has no .git, and it is wrong:
// the two LARGEST sessions on this machine were both launched from ~/coding, so
// that rule would have deleted the biggest work on the machine from Recent. The
// folder is uninformative, the session is not.
//
// So there are two questions, asked in order.
//
//   - Is cwd inside a checkout? Then the checkout is the project, which also
//     merges `kunai/web` back under `kunai` instead of giving a subdirectory a
//     heading of its own. Costs a few stats and no transcript at all.
//   - Otherwise: cwd is a container, so ask the transcript where the work
//     actually went. Every line records the directory the agent was in, so the
//     immediate child of cwd that most of them name is the codebase. Buckets are
//     by immediate child rather than by the raw directory, or a session that
//     spent its time in `hiring-god/web` would be filed under "web".
//
// Three things a candidate must be, each of which a real session tripped over:
// not a dotfolder (~/.claude is not a project), a directory that still EXISTS
// (a heading you cannot start a session in is a bad heading, and one deleted
// folder was outpolling the right answer 25 to 26), and a clear winner. A near
// tie is not noise to be broken: it means the session genuinely spanned several
// codebases, there is no single honest heading, and the right answer is to leave
// it under its folder where naming it a workspace is offered.
func projectDir(cwd string, dirs map[string]int) string {
	if cwd == "" {
		return ""
	}
	if root := project.Root(cwd); root != "" {
		return root
	}
	base := strings.TrimRight(cwd, "/")
	kids := map[string]int{}
	for dir, n := range dirs {
		rest, ok := strings.CutPrefix(strings.TrimRight(dir, "/"), base+"/")
		if !ok {
			continue
		}
		seg, _, _ := strings.Cut(rest, "/")
		if seg == "" || strings.HasPrefix(seg, ".") {
			continue
		}
		kids[seg] += n
	}
	var best, runner string
	for seg := range kids {
		if best == "" || kids[seg] > kids[best] {
			best, runner = seg, best
			continue
		}
		if runner == "" || kids[seg] > kids[runner] {
			runner = seg
		}
	}
	// minDirLines keeps a single stray `cd` from renaming a group; twice the
	// runner-up is what "clear winner" means.
	const minDirLines = 3
	if best == "" || kids[best] < minDirLines || (runner != "" && kids[best] < 2*kids[runner]) {
		return ""
	}
	path := filepath.Join(base, best)
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		return ""
	}
	// The child may itself sit inside a checkout rooted higher up (a repo cloned
	// into a folder of its own); the checkout is the project either way.
	if root := project.Root(path); root != "" {
		return root
	}
	return path
}

// transcriptProbe is everything one read of a transcript yields about it. The
// fields are returned together rather than one at a time because they come from
// the same two reads, and the whole point of this type is that nothing here
// costs a third.
type transcriptProbe struct {
	// Cwd is the directory the session was launched from.
	Cwd string
	// Title is what Claude Code would show for it.
	Title string
	// Asked reports whether anybody ever prompted this session, which is what
	// separates a conversation from a transcript that merely exists. It is kept
	// apart from Title because the two are not the same question: a session can
	// be prompted and still have no usable title (an attachment with no words),
	// and a transcript can carry a generated title having never been asked
	// anything.
	Asked bool
	// Dirs counts, by line, the working directories the tail reported. Every
	// transcript line carries the directory the agent was in when it was written,
	// so a session that was started in a container folder and then worked inside
	// a codebase says so here, in its own record, without kunai having had to
	// note it down at the time. See projectDir for what is made of it.
	Dirs map[string]int
}

// probeTranscript extracts the session cwd (from the head) and the display
// title, mirroring what Claude Code shows: a user's custom title if set, else
// the generated ai-title, else the first real user prompt. Claude Code writes
// the title entries near the END of the transcript, so they're read from the
// tail; the head scan only gets the cwd and a first-prompt fallback.
func probeTranscript(path string) transcriptProbe {
	f, err := os.Open(path)
	if err != nil {
		return transcriptProbe{}
	}
	defer f.Close()

	p := transcriptProbe{}
	var firstPrompt string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024) // transcript lines can be large
	for lines := 0; sc.Scan() && lines < 60 && (p.Cwd == "" || !p.Asked); lines++ {
		var v struct {
			Cwd     string `json:"cwd"`
			Message struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &v) != nil {
			continue
		}
		if p.Cwd == "" && v.Cwd != "" {
			p.Cwd = v.Cwd
		}
		if v.Message.Role == "user" && len(v.Message.Content) > 0 {
			t := strings.TrimSpace(firstUserText(v.Message.Content))
			// Skip harness/system wrappers (<system_instruction>, caveats, …).
			// They are not somebody asking for something, so they do not make this
			// a conversation either.
			if t != "" && !strings.HasPrefix(t, "<") {
				p.Asked = true
				if firstPrompt == "" {
					firstPrompt = t
				}
			}
		}
	}

	// One tail read feeding both readers. The title has always needed it, so the
	// directory histogram is free: taking it from the head instead would have
	// meant giving up the early break above and unmarshalling sixty whole lines
	// per transcript per poll, for a worse answer. The tail is also the better
	// sample on its own terms, since it reports where the session was working by
	// the end rather than which folder somebody happened to type at the start.
	tail := tailBytes(f)
	p.Title = claudeTitle(tail)
	p.Dirs = tailDirs(tail)
	if p.Title == "" {
		p.Title = firstPrompt
	}
	p.Title = truncate(strings.TrimSpace(p.Title), 64)
	return p
}

// tailBytes reads the last titleWindow bytes of an open transcript, which
// is where Claude Code appends the entries the probe is after.
func tailBytes(f *os.File) []byte {
	const window = 128 * 1024
	fi, err := f.Stat()
	if err != nil {
		return nil
	}
	start := int64(0)
	if fi.Size() > window {
		start = fi.Size() - window
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil
	}
	return data
}

// tailDirs counts the working directory each tail line reported. The first line
// of a windowed read is usually a fragment, which json rejects, so a truncated
// line costs one sample and never a wrong one.
func tailDirs(data []byte) map[string]int {
	out := map[string]int{}
	for _, ln := range bytes.Split(data, []byte("\n")) {
		if !bytes.Contains(ln, []byte(`"cwd"`)) { // cheap prefilter
			continue
		}
		var v struct {
			Cwd string `json:"cwd"`
		}
		if json.Unmarshal(ln, &v) != nil || v.Cwd == "" {
			continue
		}
		out[v.Cwd]++
	}
	return out
}

// claudeTitle returns Claude Code's current session name from the tail of a
// transcript: the last custom-title (a user rename) if any, else the last
// ai-title (generated).
func claudeTitle(data []byte) string {
	var ai, custom string
	for _, ln := range bytes.Split(data, []byte("\n")) {
		if !bytes.Contains(ln, []byte("-title")) { // cheap prefilter
			continue
		}
		var v struct {
			Type        string `json:"type"`
			AiTitle     string `json:"aiTitle"`
			CustomTitle string `json:"customTitle"`
		}
		if json.Unmarshal(ln, &v) != nil {
			continue
		}
		switch v.Type {
		case "custom-title":
			if v.CustomTitle != "" {
				custom = v.CustomTitle
			}
		case "ai-title":
			if v.AiTitle != "" {
				ai = v.AiTitle
			}
		}
	}
	if custom != "" {
		return custom
	}
	return ai
}

// transcriptAttachments recovers what a past user turn carried, so a resumed
// session still shows it. A transcript records images as inline content blocks
// with no filename, so only the type survives — enough for a placeholder, which
// is all the message needs (the bytes are never served back).
func transcriptAttachments(content json.RawMessage) []session.Attachment {
	var blocks []struct {
		Type   string `json:"type"`
		Source struct {
			MediaType string `json:"media_type"`
		} `json:"source"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	var out []session.Attachment
	for _, b := range blocks {
		if b.Type == "image" {
			out = append(out, session.Attachment{Name: "Image", MediaType: b.Source.MediaType})
		}
	}
	return out
}

func firstUserText(content json.RawMessage) string {
	var s string
	if json.Unmarshal(content, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) == nil {
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				return b.Text
			}
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// maxSeedTurns bounds how much history is replayed into a resumed session (one
// turn = one user/assistant/tool_result message). Kept in line with the live
// ring so a resumed conversation shows the same depth a warm one does.
const maxSeedTurns = 2000

// loadTranscriptTurns parses a session transcript into displayable turns so a
// resumed session opens with its conversation history.
// transcriptPath locates a session's transcript file under the given account's
// config dir (empty = the default ~/.claude), or "" if none exists.
func transcriptPath(configDir, id string) string {
	root := claudeRoot(configDir)
	dirs, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, d := range dirs {
		p := filepath.Join(root, d.Name(), id+".jsonl")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// seedTailBytes caps how much of a transcript resume-seeding reads. A long-lived
// session's transcript grows to tens of MB, and parsing all of it synchronously
// in the create-session handler was the resume delay (measured: 1.8s on a 69MB
// file, two full scans). The client only mounts the trailing window of turns and
// reveals more from what was seeded, so the tail is all a reopen actually shows;
// reading only it makes resume time constant no matter how big the session got.
// The trade: scrollback on a resumed session ends at this boundary instead of
// the session's birth.
const seedTailBytes = 4 << 20

// histChunkBytes is how much of the transcript one reverse-scroll page reads.
// Sized to yield a comfortable batch of turns without a large response.
const histChunkBytes = 512 << 10

// transcriptTail returns the last seedTailBytes of the file aligned to a line
// start (the whole file when smaller) and the byte offset where that tail begins.
// Older history lives in [0, start); start is 0 when the whole file was read (no
// older history). Returns nil on read error.
func transcriptTail(path string) (tail []byte, start int64) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, 0
	}
	if st.Size() > seedTailBytes {
		off := st.Size() - seedTailBytes
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			return nil, 0
		}
		b, err := io.ReadAll(f)
		if err != nil {
			return nil, 0
		}
		// Drop the partial first line the seek landed in; older history begins at
		// the first complete line boundary.
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			return b[i+1:], off + int64(i) + 1
		}
		return nil, 0
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, 0
	}
	return b, 0
}

// loadTranscriptSlice reads one older page: the ~histChunkBytes ending just before
// `before`, aligned to a line start. Returns the raw bytes and the offset of the
// next (older) page, 0 when the start of the file is reached.
func loadTranscriptSlice(path string, before int64) (data []byte, older int64) {
	if before <= 0 {
		return nil, 0
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0
	}
	defer f.Close()
	start := before - histChunkBytes
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, 0
	}
	buf := make([]byte, before-start)
	if _, err := io.ReadFull(f, buf); err != nil {
		return nil, 0
	}
	if start > 0 {
		// Align to a line boundary; the older cursor is that boundary.
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			return buf[i+1:], start + int64(i) + 1
		}
		return nil, 0
	}
	return buf, 0
}

// loadTranscriptTurns parses the resume tail into turns (see loadTranscriptSeed);
// kept as a plain []SeedTurn so it can pass straight as a seedFn.
func loadTranscriptTurns(configDir, id string) []session.SeedTurn {
	turns, _ := loadTranscriptSeed(configDir, id)
	return turns
}

// loadTranscriptSeed parses the resume tail into turns and returns the byte offset
// where older history begins (0 = none), which becomes the session's reverse-scroll
// cursor.
func loadTranscriptSeed(configDir, id string) ([]session.SeedTurn, int64) {
	path := transcriptPath(configDir, id)
	if path == "" {
		return nil, 0
	}
	tail, start := transcriptTail(path)
	if tail == nil {
		return nil, 0
	}
	turns := parseSeedTurns(tail)
	if len(turns) > maxSeedTurns {
		turns = turns[len(turns)-maxSeedTurns:]
	}
	return turns, start
}

// parseSeedTurns turns raw transcript bytes (a tail or an older page) into
// displayable seed turns. Shared by resume seeding and the reverse-scroll page so
// both render identically.
func parseSeedTurns(data []byte) []session.SeedTurn {
	var turns []session.SeedTurn
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 256*1024), 16*1024*1024)
	for sc.Scan() {
		var v struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
			IsMeta  bool   `json:"isMeta"`
			// A compaction writes its summary as a user record, flagged so a client
			// knows it is the model's new context rather than anything anyone typed.
			IsCompactSummary bool `json:"isCompactSummary"`
			TranscriptOnly   bool `json:"isVisibleInTranscriptOnly"`
			// The transcript file spells this camelCase; the live wire uses
			// snake_case (see claude.CompactBoundary).
			CompactMetadata *struct {
				Trigger    string `json:"trigger"`
				PreTokens  int64  `json:"preTokens"`
				PostTokens int64  `json:"postTokens"`
			} `json:"compactMetadata"`
			Message struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &v) != nil {
			continue
		}
		switch v.Type {
		case "system":
			// Mark where the conversation was summarised, so a resumed session shows
			// the same boundary a live one does — and never the summary itself.
			if v.Subtype == "compact_boundary" && v.CompactMetadata != nil {
				turns = append(turns, session.SeedTurn{
					Role:       "compact",
					Trigger:    v.CompactMetadata.Trigger,
					PreTokens:  v.CompactMetadata.PreTokens,
					PostTokens: v.CompactMetadata.PostTokens,
				})
			}
		case "user":
			// The compaction summary is a user record only because that is how the
			// CLI feeds it back to the model. It is not a turn anyone typed, and it
			// runs to tens of thousands of characters, so seeding it buried the
			// conversation under a wall of text on every resumed session.
			if v.IsMeta || v.IsCompactSummary || v.TranscriptOnly {
				continue
			}
			// A user frame is either something the user typed or a carrier for
			// tool results the CLI fed back — seed both so a resumed session
			// shows tool outputs like a live one does.
			for _, r := range claude.ParseToolResultBlocks(v.Message.Content) {
				turns = append(turns, session.SeedTurn{
					Role:      "tool_result",
					ToolUseID: r.ToolUseID,
					Text:      r.Content,
					IsError:   r.IsError,
				})
			}
			t := strings.TrimSpace(firstUserText(v.Message.Content))
			atts := transcriptAttachments(v.Message.Content)
			// A loop's iterations are user frames only because that is the sole way
			// to send a turn. Replaying them would repeat the same instructions once
			// per lap; show the seam the live session showed instead.
			if n, of, ok := session.ParseLoopIteration(t); ok {
				turns = append(turns, session.SeedTurn{Role: "loop", Iteration: n, MaxIters: of})
				continue
			}
			// Skip harness wrappers — they aren't turns the user typed.
			if (t != "" && !strings.HasPrefix(t, "<")) || len(atts) > 0 {
				turns = append(turns, session.SeedTurn{Role: "user", Text: t, Attachments: atts})
			}
		case "assistant":
			if blocks := assistantSeedBlocks(v.Message.Content); len(blocks) > 0 {
				turns = append(turns, session.SeedTurn{Role: "assistant", Blocks: blocks})
			}
		}
	}
	return turns
}

// handleOlderTurns serves one reverse-scroll page: the turns just older than the
// `before` byte offset the client holds (from hello's hist_before, then each
// page's `older`). Returns the turns as the same app events the live seed emits,
// plus the next older cursor (0 = start of transcript reached). Reading a bounded
// byte slice keeps every page fast regardless of transcript size.
func (s *Server) handleOlderTurns(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, ok := s.mgr.Get(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "session not found")
		return
	}
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	path := s.transcriptForID(id)
	if path == "" || before <= 0 {
		writeJSON(w, http.StatusOK, map[string]any{"events": []session.AppEvent{}, "older": 0})
		return
	}
	data, older := loadTranscriptSlice(path, before)
	turns := parseSeedTurns(data)
	overhead := sess.SeedOverhead()
	events := make([]session.AppEvent, 0, len(turns))
	for _, t := range turns {
		events = append(events, session.SeedEvent(t, overhead))
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "older": older})
}

// transcriptForID finds a session's transcript across every account's projects (an
// id is globally unique, so at most one matches), for the reverse-scroll endpoint.
func (s *Server) transcriptForID(id string) string {
	if id == "" || strings.ContainsAny(id, `/\.`) {
		return ""
	}
	for _, ar := range s.accountRoots() {
		dirs, err := os.ReadDir(ar.root)
		if err != nil {
			continue
		}
		for _, d := range dirs {
			p := filepath.Join(ar.root, d.Name(), id+".jsonl")
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

// assistantSeedBlocks converts transcript assistant content into app blocks
// (text and tool_use; thinking is dropped from replays).
func assistantSeedBlocks(content json.RawMessage) []session.AppBlock {
	var raw []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if json.Unmarshal(content, &raw) != nil {
		return nil
	}
	out := make([]session.AppBlock, 0, len(raw))
	for _, b := range raw {
		switch b.Type {
		case "text":
			if b.Text != "" {
				out = append(out, session.AppBlock{Type: "text", Text: b.Text})
			}
		case "tool_use":
			out = append(out, session.AppBlock{Type: "tool_use", ID: b.ID, Name: b.Name, Input: b.Input})
		}
	}
	return out
}

// transcriptUsage is the token accounting carried by result/assistant frames.
type transcriptUsage struct {
	Input       int64 `json:"input_tokens"`
	CacheCreate int64 `json:"cache_creation_input_tokens"`
	CacheRead   int64 `json:"cache_read_input_tokens"`
}

// loadTranscriptContextTokens returns the context-window occupancy (input plus
// cache tokens) from a transcript's most recent usage, so a resumed session
// shows its real context fill at once instead of the "send a message" prompt.
// It also returns the measured context overhead (see below), which seeds the
// resumed session so the meter is right the moment it next compacts. Returns
// 0, 0 if the transcript records no usage yet.
//
// The overhead is the fixed cost that stays resident in the window no matter how
// small the conversation gets: the system prompt, tool schemas, memory, and
// skills, tens of thousands of tokens. It matters because a compaction's
// postTokens counts ONLY the compacted conversation and omits that overhead, so
// a meter set to the bare postTokens reads far too LOW (13k when Claude's own
// /context shows ~50k). The overhead is NOT in the compaction frame: preTokens
// is the full pre-compaction context, the same basis as the assistant usage the
// meter comes from, so preTokens-postTokens over-subtracts and collapses the
// meter to postTokens. The only honest source is measurement: the gap between a
// compaction's postTokens and the first assistant usage right after it is
// overhead plus that turn's new prompt, so the smallest such gap across the
// transcript is the tightest overhead estimate. With it, the post-compaction
// meter is postTokens+overhead.
func loadTranscriptContextTokens(configDir, id string) (tokens, overhead int64) {
	path := transcriptPath(configDir, id)
	if path == "" {
		return 0, 0
	}
	// Tail-only, like the seed: the current occupancy is the newest usage, which
	// is always in the tail. The overhead measurement only sees compactions inside
	// the tail; an older one is re-measured live at the next compaction, which is
	// the accepted cost of a constant-time resume.
	tail, _ := transcriptTail(path)
	if tail == nil {
		return 0, 0
	}

	var last, pendingPost int64
	sc := bufio.NewScanner(bytes.NewReader(tail))
	sc.Buffer(make([]byte, 0, 256*1024), 16*1024*1024)
	for sc.Scan() {
		var v struct {
			Type    string           `json:"type"`
			Subtype string           `json:"subtype"`
			Usage   *transcriptUsage `json:"usage"`
			Message struct {
				Usage *transcriptUsage `json:"usage"`
			} `json:"message"`
			// A compaction resets the window; on disk the field is camelCase.
			CompactMetadata *struct {
				PreTokens  int64 `json:"preTokens"`
				PostTokens int64 `json:"postTokens"`
			} `json:"compactMetadata"`
		}
		if json.Unmarshal(sc.Bytes(), &v) != nil {
			continue
		}
		// A compaction supersedes any earlier assistant usage: no assistant
		// message follows it to report the smaller number. Keep the resident
		// overhead by seeding postTokens+overhead; the next assistant usage (if
		// any) will refine it. Remember postTokens so that next usage can measure
		// the overhead gap.
		if v.Type == "system" && v.Subtype == "compact_boundary" && v.CompactMetadata != nil && v.CompactMetadata.PostTokens > 0 {
			post := v.CompactMetadata.PostTokens
			last = post + overhead
			pendingPost = post
			continue
		}
		u := v.Usage
		if u == nil {
			u = v.Message.Usage
		}
		if u != nil {
			last = u.Input + u.CacheCreate + u.CacheRead
			// First real usage after a compaction: its size over the compacted
			// conversation is the overhead (plus this turn's new prompt). Take the
			// smallest such gap as the tightest estimate.
			if pendingPost > 0 {
				if gap := last - pendingPost; gap > 0 && (overhead == 0 || gap < overhead) {
					overhead = gap
				}
				pendingPost = 0
			}
		}
	}
	return last, overhead
}
