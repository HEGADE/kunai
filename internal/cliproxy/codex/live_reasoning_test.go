package codex

// Live reasoning-replay stress test: request extended thinking so Codex returns a
// reasoning item (which the translator turns into a Claude thinking block with an
// encrypted signature), then send a SECOND turn that replays that thinking block
// back. This is the precise case the reference's reasoning-replay cache exists for;
// if Codex rejects the replayed signature, turn 2 fails here. Gated as the others.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestLiveCodexReasoningReplay(t *testing.T) {
	if os.Getenv("KUNAI_CODEX_LIVE") != "1" {
		t.Skip("set KUNAI_CODEX_LIVE=1 and KUNAI_CODEX_TOKEN to run")
	}
	tokenPath := os.Getenv("KUNAI_CODEX_TOKEN")
	if tokenPath == "" {
		t.Fatal("KUNAI_CODEX_TOKEN not set")
	}
	p := NewProxy(tokenPath, false)
	model := liveModel()

	// Turn 1: reasoning-heavy prompt with extended thinking enabled.
	t1 := `{"model":"` + model + `","max_tokens":2048,"stream":true,` +
		`"thinking":{"type":"enabled","budget_tokens":1024},` +
		`"messages":[{"role":"user","content":"Think step by step: a bat and ball cost $1.10 total, the bat costs $1 more than the ball. How much is the ball? Show your reasoning."}]}`
	out1 := doLive(t, p, t1)
	assistant, stop := assistantFromSSE(out1)

	hasThinking := gjson.Get(assistant, `content.#(type=="thinking")`).Exists()
	sig := gjson.Get(assistant, `content.#(type=="thinking").signature`).String()
	t.Logf("turn1 stop=%s hasThinking=%v signatureLen=%d", stop, hasThinking, len(sig))
	if !hasThinking {
		t.Skipf("model returned no thinking block; cannot stress signature replay (raw turn1: %s)", truncate(out1, 400))
	}

	// Turn 2: replay the assistant's thinking+signature, then ask a follow-up.
	msgs, _ := sjson.SetRawBytes([]byte(`[]`), "-1", []byte(
		`{"role":"user","content":"Think step by step: a bat and ball cost $1.10 total, the bat costs $1 more than the ball. How much is the ball? Show your reasoning."}`))
	msgs, _ = sjson.SetRawBytes(msgs, "-1", []byte(assistant))
	msgs, _ = sjson.SetRawBytes(msgs, "-1", []byte(`{"role":"user","content":"Now state just the final number in cents."}`))
	t2, _ := sjson.SetRawBytes([]byte(`{"model":"`+model+`","max_tokens":2048,"stream":true,"thinking":{"type":"enabled","budget_tokens":1024}}`), "messages", msgs)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(string(t2)))
	rec := httptest.NewRecorder()
	p.handleMessages(rec, req)
	out2 := rec.Body.String()
	t.Logf("turn2 status=%d", rec.Code)
	if rec.Code != http.StatusOK {
		t.Fatalf("REASONING REPLAY FAILED (status %d) — this is the reasoning-replay/signature gap; the replay cache is needed:\n%s", rec.Code, truncate(out2, 1500))
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
	t.Logf("turn2 answer: %q", strings.TrimSpace(text.String()))
	t.Log("REASONING REPLAY SURVIVED: Codex accepted the replayed thinking signature")
}
