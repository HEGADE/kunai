package codex

// Live multi-turn tool-use test: the real check of whether the reasoning-replay /
// signature gap actually bites. Turn 1 asks for a tool call; we reconstruct the
// assistant message (thinking+signature, text, tool_use) from the Anthropic SSE
// exactly as the claude CLI would, then send turn 2 with the tool_result. If Codex
// rejects the replayed reasoning with "invalid signature", this fails and we know
// the replay cache is required. Gated the same way as TestLiveCodexRoundTrip.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// assistantFromSSE reconstructs an Anthropic assistant message ({"role","content":[...]})
// from a streamed Anthropic SSE response, assembling content blocks by index.
func assistantFromSSE(sse string) (string, string) {
	type blk struct {
		typ, text, thinking, sig, name, id string
		partial                            string // tool_use input json / accumulated
	}
	blocks := map[int]*blk{}
	stop := ""
	for _, line := range strings.Split(sse, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		d := strings.TrimSpace(line[5:])
		switch gjson.Get(d, "type").String() {
		case "content_block_start":
			i := int(gjson.Get(d, "index").Int())
			cb := gjson.Get(d, "content_block")
			blocks[i] = &blk{
				typ:  cb.Get("type").String(),
				text: cb.Get("text").String(),
				name: cb.Get("name").String(),
				id:   cb.Get("id").String(),
			}
		case "content_block_delta":
			i := int(gjson.Get(d, "index").Int())
			b := blocks[i]
			if b == nil {
				b = &blk{}
				blocks[i] = b
			}
			delta := gjson.Get(d, "delta")
			switch delta.Get("type").String() {
			case "text_delta":
				b.text += delta.Get("text").String()
			case "thinking_delta":
				b.thinking += delta.Get("thinking").String()
			case "signature_delta":
				b.sig += delta.Get("signature").String()
			case "input_json_delta":
				b.partial += delta.Get("partial_json").String()
			}
		case "message_delta":
			if s := gjson.Get(d, "delta.stop_reason").String(); s != "" {
				stop = s
			}
		}
	}
	// Assemble content array in index order.
	content := []byte(`[]`)
	for i := 0; i < len(blocks); i++ {
		b := blocks[i]
		if b == nil {
			continue
		}
		item := []byte(`{}`)
		switch b.typ {
		case "thinking":
			item, _ = sjson.SetBytes(item, "type", "thinking")
			item, _ = sjson.SetBytes(item, "thinking", b.thinking)
			item, _ = sjson.SetBytes(item, "signature", b.sig)
		case "text":
			item, _ = sjson.SetBytes(item, "type", "text")
			item, _ = sjson.SetBytes(item, "text", b.text)
		case "tool_use":
			item, _ = sjson.SetBytes(item, "type", "tool_use")
			item, _ = sjson.SetBytes(item, "id", b.id)
			item, _ = sjson.SetBytes(item, "name", b.name)
			in := b.partial
			if in == "" {
				in = "{}"
			}
			item, _ = sjson.SetRawBytes(item, "input", []byte(in))
		default:
			continue
		}
		content, _ = sjson.SetRawBytes(content, "-1", item)
	}
	msg, _ := sjson.SetRawBytes([]byte(`{"role":"assistant"}`), "content", content)
	return string(msg), stop
}

func TestLiveCodexMultiTurnToolUse(t *testing.T) {
	if os.Getenv("KUNAI_CODEX_LIVE") != "1" {
		t.Skip("set KUNAI_CODEX_LIVE=1 and KUNAI_CODEX_TOKEN to run")
	}
	tokenPath := os.Getenv("KUNAI_CODEX_TOKEN")
	if tokenPath == "" {
		t.Fatal("KUNAI_CODEX_TOKEN not set")
	}
	p := NewProxy(tokenPath, false)
	model := liveModel()

	tools := `[{"name":"calculator","description":"Evaluate an arithmetic expression","input_schema":{"type":"object","properties":{"expression":{"type":"string"}},"required":["expression"]}}]`

	// Turn 1: force a tool call.
	t1 := `{"model":"` + model + `","max_tokens":1024,"stream":true,"tools":` + tools + `,` +
		`"messages":[{"role":"user","content":"Use the calculator tool to compute 17 * 23. You must call the tool."}]}`
	out1 := doLive(t, p, t1)
	assistant, stop := assistantFromSSE(out1)
	t.Logf("turn1 stop_reason=%s assistant=%s", stop, truncate(assistant, 600))

	toolUseID := gjson.Get(assistant, `content.#(type=="tool_use").id`).String()
	if toolUseID == "" {
		t.Skipf("model did not emit a tool_use on turn 1 (stop=%s); cannot exercise multi-turn replay", stop)
	}

	// Turn 2: send history + tool_result. This replays the assistant's reasoning
	// (thinking+signature) back to Codex, which is where an invalid-signature
	// rejection would occur if the replay cache were required.
	toolResult, _ := sjson.SetRawBytes([]byte(`{"role":"user"}`), "content", []byte(
		`[{"type":"tool_result","tool_use_id":"`+toolUseID+`","content":"391"}]`))
	msgs, _ := sjson.SetRawBytes([]byte(`[]`), "-1", []byte(
		`{"role":"user","content":"Use the calculator tool to compute 17 * 23. You must call the tool."}`))
	msgs, _ = sjson.SetRawBytes(msgs, "-1", []byte(assistant))
	msgs, _ = sjson.SetRawBytes(msgs, "-1", toolResult)
	t2, _ := sjson.SetRawBytes([]byte(`{"model":"`+model+`","max_tokens":1024,"stream":true,"tools":`+tools+`}`), "messages", msgs)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(string(t2)))
	rec := httptest.NewRecorder()
	p.handleMessages(rec, req)
	out2 := rec.Body.String()
	t.Logf("turn2 status=%d", rec.Code)
	if rec.Code != http.StatusOK {
		t.Fatalf("MULTI-TURN FAILED (status %d) — likely the reasoning-replay/signature gap:\n%s", rec.Code, truncate(out2, 1500))
	}
	var text strings.Builder
	for _, line := range strings.Split(out2, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			d := strings.TrimSpace(line[5:])
			if gjson.Get(d, "type").String() == "content_block_delta" {
				text.WriteString(gjson.Get(d, "delta.text").String())
			}
		}
	}
	got := strings.TrimSpace(text.String())
	t.Logf("turn2 final answer: %q", got)
	if !strings.Contains(got, "391") {
		t.Errorf("expected the answer 391 after the tool result, got %q", got)
	}
}

func doLive(t *testing.T, p *Proxy, body string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	rec := httptest.NewRecorder()
	p.handleMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("live request failed: status=%d body=%s", rec.Code, truncate(rec.Body.String(), 1500))
	}
	return rec.Body.String()
}
