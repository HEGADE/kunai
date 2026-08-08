package usagestats

import "strings"

// What the tokens would have cost at API rates.
//
// The number this produces is deliberately NOT a bill. Everything here runs on a
// subscription, so nobody is charged per token; what the page reports is the
// counterfactual, "this is what this work would have cost had it gone through the
// API", which is the only way to compare a Claude session against a Codex one or
// to see which model ate the month. The UI has to say that in as many words, or
// the headline reads as an invoice.
//
// A rate table goes stale, and the honest response to that is not to guess. A
// model with no entry is reported as UNPRICED rather than being folded in at some
// neighbouring model's rate: its tokens still count toward the token totals, and
// the page shows what share of the corpus it could not price. A number quietly
// wrong is worse than a number openly missing.

// Rate is dollars per million tokens.
type Rate struct {
	In  float64
	Out float64
}

// Cache multipliers, applied to the input rate. A 5-minute cache write costs
// 1.25x input, a 1-hour write 2x, and a read 0.1x.
const (
	cacheWrite5mMult = 1.25
	cacheWrite1hMult = 2.0
	cacheReadMult    = 0.1
)

// rates is keyed by model-id prefix, matched longest-first so a dated variant
// (`claude-haiku-4-5-20251001`) resolves to its family without needing its own
// row.
//
// Anthropic rates are the published first-party API prices. Non-Anthropic models
// are absent on purpose: kunai can drive Codex and Grok through a provider, and
// making up an OpenAI or xAI rate here would put a confident dollar figure on a
// guess. They land in the unpriced bucket until someone adds a rate they can
// point at.
var rates = map[string]Rate{
	"claude-fable-5":    {10, 50},
	"claude-mythos-5":   {10, 50},
	"claude-opus-5":     {5, 25},
	"claude-opus-4-8":   {5, 25},
	"claude-opus-4-7":   {5, 25},
	"claude-opus-4-6":   {5, 25},
	"claude-opus-4-5":   {5, 25},
	"claude-opus-4-1":   {15, 75},
	"claude-opus-4":     {15, 75},
	"claude-3-opus":     {15, 75},
	"claude-sonnet-5":   {3, 15},
	"claude-sonnet-4-6": {3, 15},
	"claude-sonnet-4-5": {3, 15},
	"claude-sonnet-4":   {3, 15},
	"claude-3-7-sonnet": {3, 15},
	"claude-3-5-sonnet": {3, 15},
	"claude-haiku-4-5":  {1, 5},
	"claude-3-5-haiku":  {0.80, 4},
	"claude-3-haiku":    {0.25, 1.25},
}

// RateFor returns the rate for a model id, and whether one is known.
func RateFor(model string) (Rate, bool) {
	m := strings.ToLower(strings.TrimSpace(model))
	best, bestLen, found := Rate{}, 0, false
	for prefix, r := range rates {
		if len(prefix) > bestLen && strings.HasPrefix(m, prefix) {
			best, bestLen, found = r, len(prefix), true
		}
	}
	return best, found
}

// Cost prices one bucket. priced is false when the model has no rate, in which
// case cost is 0 and the caller must report the tokens as unpriced rather than
// as free.
func Cost(model string, t Tokens) (cost float64, priced bool) {
	r, ok := RateFor(model)
	if !ok {
		return 0, false
	}
	in := float64(t.Input) +
		float64(t.CacheWrite5m)*cacheWrite5mMult +
		float64(t.CacheWrite1h)*cacheWrite1hMult +
		float64(t.CacheRead)*cacheReadMult
	return (in*r.In + float64(t.Output)*r.Out) / 1e6, true
}

// CacheSaving is what the cache reads in this bucket would have cost at the full
// input rate, minus what they actually cost. It is the one number here that is
// unambiguously good news, and on an agent workload it dwarfs the bill.
func CacheSaving(model string, t Tokens) float64 {
	r, ok := RateFor(model)
	if !ok {
		return 0
	}
	return float64(t.CacheRead) * (1 - cacheReadMult) * r.In / 1e6
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
