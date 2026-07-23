package codex

// Ported from CLIProxyAPI internal/thinking (suffix.go, types.go, convert.go),
// only the subset the Codex translator uses. The rest of that package pulls in
// internal/registry, which we deliberately do not.

import "strings"

// SuffixResult represents the result of parsing a model name for a thinking suffix.
type SuffixResult struct {
	ModelName string
	HasSuffix bool
	RawSuffix string
}

// ParseSuffix extracts a thinking suffix from a model name, e.g.
// "gpt-5.2(high)" -> ModelName="gpt-5.2", RawSuffix="high".
func ParseSuffix(model string) SuffixResult {
	lastOpen := strings.LastIndex(model, "(")
	if lastOpen == -1 {
		return SuffixResult{ModelName: model, HasSuffix: false}
	}
	if !strings.HasSuffix(model, ")") {
		return SuffixResult{ModelName: model, HasSuffix: false}
	}
	return SuffixResult{
		ModelName: model[:lastOpen],
		HasSuffix: true,
		RawSuffix: model[lastOpen+1 : len(model)-1],
	}
}

// ThinkingLevel represents a discrete thinking level.
type ThinkingLevel string

const (
	LevelNone    ThinkingLevel = "none"
	LevelAuto    ThinkingLevel = "auto"
	LevelMinimal ThinkingLevel = "minimal"
	LevelLow     ThinkingLevel = "low"
	LevelMedium  ThinkingLevel = "medium"
	LevelHigh    ThinkingLevel = "high"
	LevelXHigh   ThinkingLevel = "xhigh"
	LevelMax     ThinkingLevel = "max"
)

// BudgetThreshold constants define the upper bounds for each thinking level.
const (
	ThresholdMinimal = 512
	ThresholdLow     = 1024
	ThresholdMedium  = 8192
	ThresholdHigh    = 24576
)

// ConvertBudgetToLevel converts a token budget to the nearest thinking level.
func ConvertBudgetToLevel(budget int) (string, bool) {
	switch {
	case budget < -1:
		return "", false
	case budget == -1:
		return string(LevelAuto), true
	case budget == 0:
		return string(LevelNone), true
	case budget <= ThresholdMinimal:
		return string(LevelMinimal), true
	case budget <= ThresholdLow:
		return string(LevelLow), true
	case budget <= ThresholdMedium:
		return string(LevelMedium), true
	case budget <= ThresholdHigh:
		return string(LevelHigh), true
	default:
		return string(LevelXHigh), true
	}
}
