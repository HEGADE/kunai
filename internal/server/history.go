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
	ID    string    `json:"id"`
	Cwd   string    `json:"cwd"`
	Title string    `json:"title"`
	Mtime time.Time `json:"mtime"`
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
	writeJSON(w, http.StatusOK, scanHistory(live, limit))
}

// scanHistory walks ~/.claude/projects/*/<sessionId>.jsonl transcripts.
func scanHistory(live map[string]bool, limit int) []HistoryEntry {
	home, err := os.UserHomeDir()
	if err != nil {
		return []HistoryEntry{}
	}
	root := filepath.Join(home, ".claude", "projects")
	dirs, err := os.ReadDir(root)
	if err != nil {
		return []HistoryEntry{}
	}

	out := []HistoryEntry{}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, d.Name()))
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
			if err != nil || info.Size() == 0 {
				continue
			}
			cwd, title := probeTranscript(filepath.Join(root, d.Name(), name))
			if cwd == "" {
				continue
			}
			if title == "" {
				title = filepath.Base(cwd)
			}
			out = append(out, HistoryEntry{ID: id, Cwd: cwd, Title: title, Mtime: info.ModTime()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Mtime.After(out[j].Mtime) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
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
// transcriptPath locates a session's transcript file, or "" if none exists.
func transcriptPath(id string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	root := filepath.Join(home, ".claude", "projects")
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

func loadTranscriptTurns(id string) []session.SeedTurn {
	path := transcriptPath(id)
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var turns []session.SeedTurn
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 256*1024), 16*1024*1024)
	for sc.Scan() {
		var v struct {
			Type    string `json:"type"`
			IsMeta  bool   `json:"isMeta"`
			Message struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &v) != nil {
			continue
		}
		switch v.Type {
		case "user":
			if v.IsMeta {
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
			// Skip harness wrappers — they aren't turns the user typed.
			if t != "" && !strings.HasPrefix(t, "<") {
				turns = append(turns, session.SeedTurn{Role: "user", Text: t})
			}
		case "assistant":
			if blocks := assistantSeedBlocks(v.Message.Content); len(blocks) > 0 {
				turns = append(turns, session.SeedTurn{Role: "assistant", Blocks: blocks})
			}
		}
	}
	if len(turns) > maxSeedTurns {
		turns = turns[len(turns)-maxSeedTurns:]
	}
	return turns
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
// Returns 0 if the transcript records no usage yet.
func loadTranscriptContextTokens(id string) int64 {
	path := transcriptPath(id)
	if path == "" {
		return 0
	}
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	var last int64
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 256*1024), 16*1024*1024)
	for sc.Scan() {
		var v struct {
			Usage   *transcriptUsage `json:"usage"`
			Message struct {
				Usage *transcriptUsage `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &v) != nil {
			continue
		}
		u := v.Usage
		if u == nil {
			u = v.Message.Usage
		}
		if u != nil {
			last = u.Input + u.CacheCreate + u.CacheRead
		}
	}
	return last
}
