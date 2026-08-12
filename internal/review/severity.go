package review

// How much a finding matters, and how sure we are of it.
//
// These two are deliberately separate, because they answer different questions
// and a review that conflates them is the review this one replaces. Severity is
// about the CODE: if this claim is true, how bad is it? Confidence is about the
// CLAIM: how likely is it to be true at all? A confidently-held nitpick and a
// tentative guess at a data-loss bug are opposite things, and a single "priority"
// number cannot say either without lying about the other.
//
// Both are small closed vocabularies rather than free text or a score. A model
// asked for a number returns noise dressed as precision (0.85 means nothing you
// can act on), and a model asked for free text invents a new word every run, so
// nothing can be sorted, filtered or counted. Three rungs each is the most that
// can be held to consistently and the least that carries information.

import "strings"

// Severity is how much this finding matters if it is real.
type Severity string

const (
	// SeverityBlocker is "do not merge this": it breaks in production, loses
	// data, or opens a hole. Rare by construction. A reviewer that calls three
	// things blockers on an ordinary pull request has devalued the word.
	SeverityBlocker Severity = "blocker"
	// SeverityMajor is a real bug or a real risk that should be fixed, but that
	// a reasonable person could ship and follow up on.
	SeverityMajor Severity = "major"
	// SeverityMinor is worth knowing and not worth blocking on.
	SeverityMinor Severity = "minor"
)

// Confidence is how sure the review is that the finding is true at all.
//
// This is the input to the verification phase rather than only a label: anything
// below ConfidenceHigh is handed back to be refuted before it can be posted. So
// the default for a missing value must be the one that gets CHECKED, never the
// one that skips the check.
type Confidence string

const (
	// ConfidenceHigh is demonstrated from the code that was read, not inferred.
	ConfidenceHigh Confidence = "high"
	// ConfidenceMedium is probable but rests on an assumption that was not
	// verified.
	ConfidenceMedium Confidence = "medium"
	// ConfidenceLow is a suspicion worth checking. On its own it is not worth
	// posting to somebody's pull request.
	ConfidenceLow Confidence = "low"
)

// Rank orders severities for sorting, most serious first. Unknown values sort
// last rather than first: a value nobody recognises must not be able to push
// itself to the top of somebody's review.
func (s Severity) Rank() int {
	switch s {
	case SeverityBlocker:
		return 0
	case SeverityMajor:
		return 1
	case SeverityMinor:
		return 2
	default:
		return 3
	}
}

// Label is the severity as the review shows it to a person.
func (s Severity) Label() string {
	switch s {
	case SeverityBlocker:
		return "Blocker"
	case SeverityMajor:
		return "Major"
	case SeverityMinor:
		return "Minor"
	default:
		return string(s)
	}
}

// normaliseSeverity repairs what a model actually emits.
//
// The synonyms are not speculative: "critical", "high", "medium" and "low" are
// the words a model reaches for when asked to rate something, whatever
// vocabulary it was handed, and dropping a real blocker because it arrived
// labelled "critical" would be a self-inflicted wound.
//
// An unrecognised or absent severity becomes SeverityMajor, the middle rung,
// which is the only defensible default: SeverityMinor would let a mislabelled
// blocker hide at the bottom of the list, and SeverityBlocker would let any
// parse slip shout.
func normaliseSeverity(s Severity) Severity {
	switch strings.ToLower(strings.TrimSpace(string(s))) {
	case "blocker", "critical", "blocking", "high":
		return SeverityBlocker
	case "major", "medium", "moderate", "important":
		return SeverityMajor
	case "minor", "low", "nit", "nitpick", "note", "info", "suggestion":
		return SeverityMinor
	default:
		return SeverityMajor
	}
}

// normaliseConfidence repairs the same way, defaulting to ConfidenceMedium.
//
// Medium rather than high on purpose: an omitted confidence means nothing has
// vouched for this claim, and the verification phase skips only what is already
// high. Defaulting to high would let every unlabelled finding bypass the one
// pass that exists to catch confident nonsense.
func normaliseConfidence(c Confidence) Confidence {
	switch strings.ToLower(strings.TrimSpace(string(c))) {
	case "high", "certain", "confirmed", "verified":
		return ConfidenceHigh
	case "low", "possible", "speculative", "unsure":
		return ConfidenceLow
	default:
		return ConfidenceMedium
	}
}

// Categories are what a finding is ABOUT, used to group and filter rather than
// to rank. Kept as a closed set for the same reason as the others, with anything
// unrecognised folded into CategoryOther rather than becoming a new column
// nobody asked for.
const (
	CategoryCorrectness = "correctness"
	CategorySecurity    = "security"
	CategoryPerformance = "performance"
	CategoryConcurrency = "concurrency"
	CategoryCompat      = "compatibility"
	CategoryResource    = "resource"
	CategoryOther       = "other"
)

// knownCategories maps what a model writes onto the set above. The keys are
// generous because the value of a category is grouping, and a grouping that
// splits "perf" from "performance" into two headings is worse than none.
var knownCategories = map[string]string{
	"correctness":     CategoryCorrectness,
	"bug":             CategoryCorrectness,
	"logic":           CategoryCorrectness,
	"security":        CategorySecurity,
	"vulnerability":   CategorySecurity,
	"auth":            CategorySecurity,
	"performance":     CategoryPerformance,
	"perf":            CategoryPerformance,
	"efficiency":      CategoryPerformance,
	"concurrency":     CategoryConcurrency,
	"race":            CategoryConcurrency,
	"threading":       CategoryConcurrency,
	"compatibility":   CategoryCompat,
	"compat":          CategoryCompat,
	"api":             CategoryCompat,
	"breaking-change": CategoryCompat,
	"resource":        CategoryResource,
	"leak":            CategoryResource,
	"memory":          CategoryResource,
}

// normaliseCategory folds a model's word onto the known set.
func normaliseCategory(c string) string {
	key := strings.ToLower(strings.TrimSpace(c))
	key = strings.ReplaceAll(key, " ", "-")
	key = strings.ReplaceAll(key, "_", "-")
	if known, ok := knownCategories[key]; ok {
		return known
	}
	return CategoryOther
}
