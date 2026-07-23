package grok

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Multi-turn tool-use through real Grok: the model calls a tool, we send the
// tool_result back, and the final answer must return. Exercises the reused
// translator's tool-call path on Grok.
func TestLiveGrokMultiTurnToolUse(t *testing.T) {
	if os.Getenv("KUNAI_GROK_LIVE") != "1" {
		t.Skip("set KUNAI_GROK_LIVE=1 to run")
	}
	p := NewProxy(grokToken())
	model := grokModel()
	tools := `[{"name":"calculator","description":"Evaluate arithmetic","input_schema":{"type":"object","properties":{"expression":{"type":"string"}},"required":["expression"]}}]`

	t1 := `{"model":"` + model + `","max_tokens":1024,"stream":true,"tools":` + tools +
		`,"messages":[{"role":"user","content":"Use the calculator tool to compute 17 * 23. You must call the tool."}]}`
	out1 := driveGrok(t, p, t1)
	assistant, stop := grokAssistantFromSSE(out1)
	t.Logf("turn1 stop=%s assistant=%s", stop, trunc(assistant, 400))
	toolID := gjson.Get(assistant, `content.#(type=="tool_use").id`).String()
	if toolID == "" {
		t.Skipf("model did not emit a tool_use (stop=%s)", stop)
	}

	msgs, _ := sjson.SetRawBytes([]byte(`[]`), "-1", []byte(`{"role":"user","content":"Use the calculator tool to compute 17 * 23. You must call the tool."}`))
	msgs, _ = sjson.SetRawBytes(msgs, "-1", []byte(assistant))
	tr, _ := sjson.SetRawBytes([]byte(`{"role":"user"}`), "content", []byte(`[{"type":"tool_result","tool_use_id":"`+toolID+`","content":"391"}]`))
	msgs, _ = sjson.SetRawBytes(msgs, "-1", tr)
	t2, _ := sjson.SetRawBytes([]byte(`{"model":"`+model+`","max_tokens":1024,"stream":true,"tools":`+tools+`}`), "messages", msgs)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(string(t2)))
	rec := httptest.NewRecorder()
	p.handleMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("turn2 status=%d: %s", rec.Code, trunc(rec.Body.String(), 1200))
	}
	var text strings.Builder
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			d := strings.TrimSpace(line[5:])
			if gjson.Get(d, "type").String() == "content_block_delta" {
				text.WriteString(gjson.Get(d, "delta.text").String())
			}
		}
	}
	t.Logf("turn2 answer: %q", strings.TrimSpace(text.String()))
	if !strings.Contains(text.String(), "391") {
		t.Errorf("expected 391 after tool result, got %q", text.String())
	}
}

func driveGrok(t *testing.T, p *Proxy, body string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	rec := httptest.NewRecorder()
	p.handleMessages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", rec.Code, trunc(rec.Body.String(), 1200))
	}
	return rec.Body.String()
}

func grokAssistantFromSSE(sse string) (string, string) {
	type blk struct{ typ, text, name, id, partial string }
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
			blocks[i] = &blk{typ: cb.Get("type").String(), text: cb.Get("text").String(), name: cb.Get("name").String(), id: cb.Get("id").String()}
		case "content_block_delta":
			i := int(gjson.Get(d, "index").Int())
			b := blocks[i]
			if b == nil {
				b = &blk{}
				blocks[i] = b
			}
			del := gjson.Get(d, "delta")
			switch del.Get("type").String() {
			case "text_delta":
				b.text += del.Get("text").String()
			case "input_json_delta":
				b.partial += del.Get("partial_json").String()
			}
		case "message_delta":
			if s := gjson.Get(d, "delta.stop_reason").String(); s != "" {
				stop = s
			}
		}
	}
	content := []byte(`[]`)
	for i := 0; i < len(blocks); i++ {
		b := blocks[i]
		if b == nil {
			continue
		}
		item := []byte(`{}`)
		switch b.typ {
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
