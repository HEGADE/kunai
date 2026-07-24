package codex

// nonstream.go repairs the one seam in the non-streaming path: the Codex backend's
// terminal `response.completed` event can carry an EMPTY `output` array, with the
// real content only in the streamed `response.output_item.done` events. The
// non-streaming translator builds the whole Anthropic reply from the terminal
// event, so without this the reply had no content at all. That single gap broke
// Claude Code's auto-mode Bash classifier on a Codex provider: the classifier is
// the only non-streaming caller, its verdict text was dropped, and the CLI
// reported "Auto mode could not evaluate this action" and denied the command.
// (Grok's terminal event includes the output, which is why the same translator
// worked there.)

import (
	"bytes"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// CompletedEventForNonStream scans a raw /responses SSE body and returns the
// terminal event's data (response.completed or response.incomplete), with
// response.output backfilled from the streamed output_item.done items whenever the
// terminal event's own output array is empty. Returns nil when the stream has no
// terminal event (the caller keeps its existing error path).
func CompletedEventForNonStream(raw []byte) []byte {
	var completed []byte
	var items [][]byte
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(line[5:])
		switch gjson.GetBytes(data, "type").String() {
		case "response.completed", "response.incomplete":
			completed = data
		case "response.output_item.done":
			if item := gjson.GetBytes(data, "item"); item.Exists() {
				items = append(items, []byte(item.Raw))
			}
		}
	}
	if completed == nil {
		return nil
	}
	out := gjson.GetBytes(completed, "response.output")
	if out.IsArray() && len(out.Array()) > 0 {
		return completed // the terminal event is complete on its own (Grok's shape)
	}
	if len(items) == 0 {
		return completed
	}
	var arr bytes.Buffer
	arr.WriteByte('[')
	for i, it := range items {
		if i > 0 {
			arr.WriteByte(',')
		}
		arr.Write(it)
	}
	arr.WriteByte(']')
	patched, err := sjson.SetRawBytes(completed, "response.output", arr.Bytes())
	if err != nil {
		return completed
	}
	return patched
}
