package codex

// models.go serves the real upstream model list so the app's model picker offers
// every model the Codex account can run, not a stub. The native proxy used to
// return an empty /v1/models, which is why a Codex provider session showed only
// its one saved model with nothing to switch to. The list comes from ChatGPT's
// codex backend (`/models?client_version=...`, whose entries carry a `slug`),
// cached briefly so opening the picker does not hammer the upstream.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// codexClientVersion is the client version the codex backend requires as a query
// param on /models (it 400s without it). Kept in step with codexUserAgent.
const codexClientVersion = "0.135.0"

// modelsExclude are upstream slugs that are not user-selectable chat models (an
// internal auto-review model), so the picker never offers them.
var modelsExclude = map[string]bool{"codex-auto-review": true}

// codexFallbackModels is what /v1/models returns when the upstream cannot be
// reached, so the picker still offers the known-good models instead of nothing.
var codexFallbackModels = []string{"gpt-5.5", "gpt-5.4", "gpt-5.4-mini"}

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

// models returns the account's Codex model ids, cached for a few minutes. On any
// upstream failure it returns the curated fallback, so the picker is never empty.
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
		return codexFallbackModels
	}
	p.mc.mu.Lock()
	p.mc.ids, p.mc.at, p.mc.good = ids, timeNow(), true
	p.mc.mu.Unlock()
	return ids
}

func (p *Proxy) fetchModels(ctx context.Context) ([]string, error) {
	access, account, err := p.tokens.creds(ctx)
	if err != nil {
		return nil, err
	}
	url := strings.TrimSuffix(p.baseURL, "/") + "/models?client_version=" + codexClientVersion
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	applyCodexHeaders(req, access, account)
	req.Header.Set("Accept", "application/json") // not the SSE default the chat call uses
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
	return parseCodexModels(body)
}

// parseCodexModels pulls the selectable model ids out of the codex backend's
// /models response (entries carry a `slug`; some also an `id`), dropping the
// non-chat internal models. Pure, so it is unit-tested without a network.
func parseCodexModels(body []byte) ([]string, error) {
	var parsed struct {
		Models []struct {
			Slug string `json:"slug"`
			ID   string `json:"id"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	var ids []string
	for _, m := range parsed.Models {
		id := m.Slug
		if id == "" {
			id = m.ID
		}
		if id == "" || modelsExclude[id] {
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// timeNow is a var so tests are deterministic.
var timeNow = time.Now

type statusErr int

func (e statusErr) Error() string { return "codex models: unexpected status" }
func errStatus(code int) error    { return statusErr(code) }
