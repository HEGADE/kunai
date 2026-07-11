package server

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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

const historyLimit = 25

// handleHistory lists resumable past sessions, newest first, excluding ones
// that are currently live.
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	live := map[string]bool{}
	for _, m := range s.mgr.List() {
		live[m.ID] = true
	}
	writeJSON(w, http.StatusOK, scanHistory(live))
}

// scanHistory walks ~/.claude/projects/*/<sessionId>.jsonl transcripts.
func scanHistory(live map[string]bool) []HistoryEntry {
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
	if len(out) > historyLimit {
		out = out[:historyLimit]
	}
	return out
}

// probeTranscript extracts the session cwd and a human title (summary line or
// first user prompt) from the head of a transcript.
func probeTranscript(path string) (cwd, title string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024) // transcript lines can be large
	for lines := 0; sc.Scan() && lines < 60 && (cwd == "" || title == ""); lines++ {
		var v struct {
			Type    string `json:"type"`
			Cwd     string `json:"cwd"`
			Summary string `json:"summary"`
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
		if title == "" {
			if v.Type == "summary" && v.Summary != "" {
				title = v.Summary
			} else if v.Message.Role == "user" && len(v.Message.Content) > 0 {
				t := strings.TrimSpace(firstUserText(v.Message.Content))
				// Skip harness/system wrappers (<system_instruction>, caveats, …)
				// — they aren't what the user actually asked.
				if t != "" && !strings.HasPrefix(t, "<") {
					title = t
				}
			}
		}
	}
	return cwd, truncate(strings.TrimSpace(title), 64)
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

// maxSeedTurns bounds how much history is replayed into a resumed session.
const maxSeedTurns = 400

// loadTranscriptTurns parses a session transcript into displayable turns so a
// resumed session opens with its conversation history.
func loadTranscriptTurns(id string) []session.SeedTurn {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	root := filepath.Join(home, ".claude", "projects")
	dirs, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var path string
	for _, d := range dirs {
		p := filepath.Join(root, d.Name(), id+".jsonl")
		if _, err := os.Stat(p); err == nil {
			path = p
			break
		}
	}
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
