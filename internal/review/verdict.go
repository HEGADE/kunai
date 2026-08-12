package review

// Reading the survey and the verification passes.
//
// Both use the same fenced-block contract as the findings themselves, for the
// same reason: the agent is a `claude` process speaking prose, not an API. Each
// phase gets its OWN fence tag so a block produced by one can never be read as
// an answer to another, which matters more here than it did with one phase,
// because a verification reply legitimately quotes the findings it is judging.

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Fence tags, one per phase. See FenceTag in parse.go for the find phase.
const (
	SurveyFenceTag  = "kunai-survey"
	VerdictFenceTag = "kunai-verdicts"
)

// ErrNoSurvey and ErrNoVerdicts report a reply that carried no block at all,
// which both callers treat as "carry on without it" rather than as a failure.
var (
	ErrNoSurvey   = errors.New("the reply contains no survey block")
	ErrNoVerdicts = errors.New("the reply contains no verdicts block")
)

// Verdict is one candidate finding, judged.
type Verdict struct {
	// Index is the candidate's position in the list the verifier was given.
	Index int `json:"index"`
	// File is the claim's path, echoed back purely as a check on Index.
	//
	// Position is a fragile way to identify a finding: the list is sorted by
	// severity on its way out of the find phase, and any future step that
	// reorders or filters between showing the claims and reading the verdicts
	// would silently pair each verdict with the wrong claim. That failure is
	// invisible and its consequence is the worst one available here, dropping a
	// real bug while keeping a refuted one. Echoing the path costs the model a
	// few tokens and turns a silent mispairing into a finding that simply goes
	// unverified. See Run.applyVerdicts.
	File string `json:"file,omitempty"`
	// Stands is false when the claim was refuted. The verifier is instructed to
	// default to false when it cannot demonstrate the claim, so this field
	// carries the burden of proof rather than the benefit of the doubt.
	Stands bool `json:"stands"`
	// Severity and Confidence may be revised DOWN by a verdict, never up. See
	// Run.applyVerdicts for why that asymmetry is deliberate.
	Severity   Severity   `json:"severity,omitempty"`
	Confidence Confidence `json:"confidence,omitempty"`
	// Note is why it stands or why it does not, in the verifier's own words. On
	// a refusal this becomes what the UI shows beside the dropped finding; on a
	// survivor it replaces the finder's evidence, because the verifier looked
	// later and with fresh eyes.
	Note string `json:"note"`
}

// ParseSurvey reads the survey phase's answer.
func ParseSurvey(text string) (Survey, error) {
	block, ok := lastFencedBlock(text, SurveyFenceTag)
	if !ok {
		return Survey{}, ErrNoSurvey
	}
	var s Survey
	if err := json.Unmarshal([]byte(block), &s); err != nil {
		return Survey{}, fmt.Errorf("the survey block is not valid JSON: %w", err)
	}
	return s, nil
}

// ParseVerdicts reads the verification phase's answer.
//
// Accepts either a bare array or an object with a "verdicts" key, because both
// are what a model produces when asked for a list and neither is wrong. Being
// strict here would throw away a whole verification pass over a wrapper.
func ParseVerdicts(text string) ([]Verdict, error) {
	block, ok := lastFencedBlock(text, VerdictFenceTag)
	if !ok {
		return nil, ErrNoVerdicts
	}

	var wrapped struct {
		Verdicts []Verdict `json:"verdicts"`
	}
	if err := json.Unmarshal([]byte(block), &wrapped); err == nil && wrapped.Verdicts != nil {
		return wrapped.Verdicts, nil
	}

	var bare []Verdict
	if err := json.Unmarshal([]byte(block), &bare); err != nil {
		return nil, fmt.Errorf("the verdicts block is not valid JSON: %w", err)
	}
	return bare, nil
}
