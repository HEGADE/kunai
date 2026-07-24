package grok

// models.go serves the real upstream model list so the app's model picker offers
// the models the xAI account can run rather than a hardcoded single entry. xAI's
// cli-chat-proxy exposes a standard OpenAI-shape /v1/models, so this proxies it
// through (cached briefly). The list is small today (a free account sees only
// grok-4.5), but this reflects the account honestly instead of pretending.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// grokFallbackModels is returned when the upstream list cannot be fetched, so the
// picker is never empty.
var grokFallbackModels = []string{"grok-4.5"}

type modelsCache struct {
	mu   sync.Mutex
	ids  []string
	at   time.Time
	good bool
}

func (p *Proxy) handleModels(w http.ResponseWriter, r *http.Request) {
	ids := p.models(r.Context())
	w.Header().Set("Content-Type", "application/json")
	out := struct {
		Object string           `json:"object"`
		Data   []map[string]any `json:"data"`
	}{Object: "list"}
	for _, id := range ids {
		out.Data = append(out.Data, map[string]any{"id": id, "object": "model"})
	}
	_ = json.NewEncoder(w).Encode(out)
}

func (p *Proxy) models(ctx context.Context) []string {
	p.mc.mu.Lock()
	if p.mc.good && time.Since(p.mc.at) < 5*time.Minute {
		ids := p.mc.ids
		p.mc.mu.Unlock()
		return ids
	}
	p.mc.mu.Unlock()

	ids, err := p.fetchModels(ctx)
	if err != nil || len(ids) == 0 {
		return grokFallbackModels
	}
	p.mc.mu.Lock()
	p.mc.ids, p.mc.at, p.mc.good = ids, timeNow(), true
	p.mc.mu.Unlock()
	return ids
}

func (p *Proxy) fetchModels(ctx context.Context) ([]string, error) {
	token, err := p.tokens.token(ctx)
	if err != nil {
		return nil, err
	}
	url := strings.TrimSuffix(p.baseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	applyGrokHeaders(req, token)
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errStatus(resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return parseGrokModels(body)
}

// parseGrokModels pulls model ids from xAI's OpenAI-shape /v1/models response.
// Pure, so it is unit-tested without a network.
func parseGrokModels(body []byte) ([]string, error) {
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	var ids []string
	for _, m := range parsed.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}

type statusErr int

func (e statusErr) Error() string { return "grok models: unexpected status" }
func errStatus(code int) error    { return statusErr(code) }
