package usagestats

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// What the tokens would have cost at API rates.
//
// The number this produces is deliberately NOT a bill. Everything here runs on a
// subscription or a free tier, so nobody is charged per token; what the page
// reports is the counterfactual, "this is what this work would have cost had it
// gone through the API", which is the only way to compare a Claude session
// against a Codex one or to see which model ate the month. The UI has to say
// that in as many words, or the headline reads as an invoice.
//
// A rate table goes stale, and the honest response to that is not to guess. A
// model with no entry is reported as UNPRICED rather than being folded in at some
// neighbouring model's rate: its tokens still count toward the token totals, and
// the page shows what share of the corpus it could not price. A number quietly
// wrong is worse than a number openly missing.
//
// It goes stale anyway, which is why the built-in table is not the only source:
// see Table and <dataDir>/pricing.json.

// Rate is dollars per million tokens.
type Rate struct {
	In  float64 `json:"in"`
	Out float64 `json:"out"`
	// CacheRead is the multiplier on In for a cached-input token, for providers
	// whose discount is not the usual 0.1. Zero means the default.
	//
	// It exists because the three providers do not agree: Anthropic and OpenAI
	// both bill a cached input token at a tenth, and xAI bills it at 0.15 ($0.30
	// against $2.00). A single hardcoded 0.1 under-priced every Grok read.
	CacheRead float64 `json:"cache_read,omitempty"`
}

// Cache multipliers on the input rate. A 5-minute cache WRITE costs 1.25x input
// and a 1-hour write 2x; both are Anthropic concepts, and a provider that has no
// write premium simply reports no cache-creation tokens, so these never apply to
// it. defaultCacheRead is the read discount every provider but xAI uses.
const (
	cacheWrite5mMult = 1.25
	cacheWrite1hMult = 2.0
	defaultCacheRead = 0.1
)

func (r Rate) cacheRead() float64 {
	if r.CacheRead > 0 {
		return r.CacheRead
	}
	return defaultCacheRead
}

// builtin is keyed by model-id prefix, matched longest-first so a dated variant
// (`claude-haiku-4-5-20251001`) resolves to its family without needing its own
// row.
//
// Every rate here is a published list price, read from the provider's own
// pricing page rather than recalled:
//   - Anthropic, platform.claude.com/pricing
//   - OpenAI, developers.openai.com/api/docs/pricing (checked 2026-08-08)
//   - xAI, docs.x.ai/docs/models (checked 2026-08-08)
//
// The non-Anthropic rows are the sub-200k/272k context tier, which is the one a
// coding session almost always sits in; both providers charge more above it, so
// a very long context is under-priced rather than over-. Overriding a row is one
// file away (see Table), which is the answer to any of this going stale.
var builtin = map[string]Rate{
	// Anthropic.
	"claude-fable-5":    {In: 10, Out: 50},
	"claude-mythos-5":   {In: 10, Out: 50},
	"claude-opus-5":     {In: 5, Out: 25},
	"claude-opus-4-8":   {In: 5, Out: 25},
	"claude-opus-4-7":   {In: 5, Out: 25},
	"claude-opus-4-6":   {In: 5, Out: 25},
	"claude-opus-4-5":   {In: 5, Out: 25},
	"claude-opus-4-1":   {In: 15, Out: 75},
	"claude-opus-4":     {In: 15, Out: 75},
	"claude-3-opus":     {In: 15, Out: 75},
	"claude-sonnet-5":   {In: 3, Out: 15},
	"claude-sonnet-4-6": {In: 3, Out: 15},
	"claude-sonnet-4-5": {In: 3, Out: 15},
	"claude-sonnet-4":   {In: 3, Out: 15},
	"claude-3-7-sonnet": {In: 3, Out: 15},
	"claude-3-5-sonnet": {In: 3, Out: 15},
	"claude-haiku-4-5":  {In: 1, Out: 5},
	"claude-3-5-haiku":  {In: 0.80, Out: 4},
	"claude-3-haiku":    {In: 0.25, Out: 1.25},

	// OpenAI, for a Codex provider.
	"gpt-5.5":      {In: 5, Out: 30},
	"gpt-5.4-mini": {In: 0.75, Out: 4.50},
	"gpt-5.4":      {In: 2.50, Out: 15},

	// xAI, for a Grok provider. The cached-read discount is 0.15 here, not 0.1.
	// `grok-4.5-build-free` is the free tier: nobody is billed for it, but this
	// page reports the counterfactual, so it is priced at what the same tokens
	// would have cost on the API -- exactly as a Claude subscription's are.
	"grok-4.5": {In: 2, Out: 6, CacheRead: 0.15},
}

// Table is the rate table in force: the built-in prices, with a machine's own
// overrides on top.
//
// The overrides exist because a hardcoded table is wrong twice over -- it goes
// stale the next time a provider changes a price, and it cannot know a model
// released after the binary. Rather than require a new release for either, a
// machine can price anything itself, and anything still unlisted stays UNPRICED
// rather than guessed. The honesty rule is unchanged; only the source of truth
// is extensible.
type Table struct{ extra map[string]Rate }

// pricingFile is where a machine keeps its own rates:
//
//	{"gpt-5.6": {"in": 5, "out": 30}, "claude-opus-5": {"in": 4.5, "out": 22.5}}
//
// A key is a model-id prefix, matched the same way the built-ins are, and an
// entry with the same key as a built-in replaces it (an enterprise discount, or
// a price that moved before kunai caught up).
const pricingFile = "pricing.json"

// LoadTable reads <dataDir>/pricing.json if it is there. A missing file is the
// normal case and not an error; a malformed one is ignored rather than fatal,
// because a typo in an optional override must not take the page down.
func LoadTable(dataDir string) *Table {
	t := &Table{}
	if dataDir == "" {
		return t
	}
	b, err := os.ReadFile(filepath.Join(dataDir, pricingFile))
	if err != nil {
		return t
	}
	var extra map[string]Rate
	if json.Unmarshal(b, &extra) != nil {
		return t
	}
	t.extra = extra
	return t
}

// Rate returns the rate for a model id, and whether one is known. The longest
// matching prefix wins, and an override beats a built-in of the same length.
func (t *Table) Rate(model string) (Rate, bool) {
	m := strings.ToLower(strings.TrimSpace(model))
	best, bestLen, found := Rate{}, -1, false
	look := func(src map[string]Rate, wins bool) {
		for prefix, r := range src {
			if !strings.HasPrefix(m, prefix) {
				continue
			}
			if len(prefix) > bestLen || (wins && len(prefix) == bestLen) {
				best, bestLen, found = r, len(prefix), true
			}
		}
	}
	look(builtin, false)
	if t != nil {
		look(t.extra, true)
	}
	return best, found
}

// Cost prices one bucket. priced is false when the model has no rate, in which
// case cost is 0 and the caller must report the tokens as unpriced rather than
// as free.
func (t *Table) Cost(model string, tk Tokens) (cost float64, priced bool) {
	r, ok := t.Rate(model)
	if !ok {
		return 0, false
	}
	in := float64(tk.Input) +
		float64(tk.CacheWrite5m)*cacheWrite5mMult +
		float64(tk.CacheWrite1h)*cacheWrite1hMult +
		float64(tk.CacheRead)*r.cacheRead()
	return (in*r.In + float64(tk.Output)*r.Out) / 1e6, true
}

// CacheSaving is what the cache reads in this bucket would have cost at the full
// input rate, minus what they actually cost. It is the one number here that is
// unambiguously good news, and on an agent workload it dwarfs the bill.
func (t *Table) CacheSaving(model string, tk Tokens) float64 {
	r, ok := t.Rate(model)
	if !ok {
		return 0
	}
	return float64(tk.CacheRead) * (1 - r.cacheRead()) * r.In / 1e6
}

// Agent is the family a model belongs to, which is the axis worth splitting the
// spend on: "how much of this month was Claude and how much was Codex" is a
// question about the brain, not about the point release.
func Agent(model string) string {
	m := strings.ToLower(model)
	switch {
	case strings.HasPrefix(m, "claude"):
		return "Claude"
	case strings.HasPrefix(m, "gpt"), strings.HasPrefix(m, "o1"), strings.HasPrefix(m, "o3"),
		strings.HasPrefix(m, "codex"):
		return "Codex"
	case strings.HasPrefix(m, "grok"):
		return "Grok"
	case strings.HasPrefix(m, "kimi"), strings.HasPrefix(m, "moonshot"):
		return "Kimi"
	default:
		return "Other"
	}
}
