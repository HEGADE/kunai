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
	live := map[string]bool{}
	for _, m := range s.mgr.List() {
		live[m.ID] = true
	}
	limit := historyLimit
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = min(v, historyMaxLimit)
	}
	var keep map[string]bool
	var over map[string]sessionMeta
	if s.sessionMeta != nil {
		keep = s.sessionMeta.pinnedIDs() // a pinned session survives the newest-N clamp
		over = s.sessionMeta.all()
	}
	entries := scanHistory(live, limit, s.accountRoots(), keep)
	for i := range entries {
		if o, ok := over[entries[i].ID]; ok {
			if o.Name != "" {
				entries[i].Title = o.Name
			}
			entries[i].Pinned = o.Pinned
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
	out := []HistoryEntry{}
	seen := map[string]bool{}
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
				if live[id] || seen[id] {
					continue
				}
				info, err := f.Info()
				if err != nil || info.Size() == 0 {
					continue
				}
				cwd, title := probeTranscript(filepath.Join(ar.root, d.Name(), name))
				if cwd == "" {
					continue
				}
				if title == "" {
					title = filepath.Base(cwd)
				}
				seen[id] = true
				out = append(out, HistoryEntry{ID: id, Cwd: cwd, Title: title, CLI: ar.name, Mtime: info.ModTime()})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Mtime.After(out[j].Mtime) })
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

// probeTranscript extracts the session cwd (from the head) and the display
// title, mirroring what Claude Code shows: a user's custom title if set, else
// the generated ai-title, else the first real user prompt. Claude Code writes
// the title entries near the END of the transcript, so they're read from the
// tail; the head scan only gets the cwd and a first-prompt fallback.
func probeTranscript(path string) (cwd, title string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()

	var firstPrompt string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024) // transcript lines can be large
	for lines := 0; sc.Scan() && lines < 60 && (cwd == "" || firstPrompt == ""); lines++ {
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
		if cwd == "" && v.Cwd != "" {
			cwd = v.Cwd
		}
		if firstPrompt == "" && v.Message.Role == "user" && len(v.Message.Content) > 0 {
			t := strings.TrimSpace(firstUserText(v.Message.Content))
			// Skip harness/system wrappers (<system_instruction>, caveats, …).
			if t != "" && !strings.HasPrefix(t, "<") {
				firstPrompt = t
			}
		}
	}

	title = claudeTitle(f)
	if title == "" {
		title = firstPrompt
	}
	return cwd, truncate(strings.TrimSpace(title), 64)
}

// claudeTitle reads the tail of a transcript and returns Claude Code's current
// session name: the last custom-title (a user rename) if any, else the last
// ai-title (generated). These entries are appended near the end of the file, so
// a bounded tail read finds them without scanning the whole transcript.
func claudeTitle(f *os.File) string {
	fi, err := f.Stat()
	if err != nil {
		return ""
	}
	const window = 128 * 1024
	start := int64(0)
	if fi.Size() > window {
		start = fi.Size() - window
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return ""
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return ""
	}
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
