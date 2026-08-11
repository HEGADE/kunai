package review

// Reading the agent's findings out of what it wrote.
//
// The agent is a `claude` process speaking prose, not an API returning JSON, so
// the contract is a fenced block it is told to end with. Everything before the
// block is its working: useful to read in the chat, and deliberately ignored
// here.
//
// The parser is forgiving in the ways models are unreliable and strict in the
// ways that matter. It takes the LAST block, because a model that reconsiders
// emits a corrected one after the first. It accepts the fence labelled or bare.
// But it does not try to salvage malformed JSON: a half-parsed review would post
// findings whose line numbers came from a guess, and no review is much better
// than a wrong one.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// FenceTag is the fence the agent is asked to use. Distinct from plain "json" so
// a review that happens to discuss a JSON file cannot be mistaken for the answer.
const FenceTag = "kunai-review"

// ErrNoFindings reports that the agent's answer carried no review block at all.
// Its own error because it is the difference between "the review found nothing"
// and "the review did not finish", which the reader must not have to guess at.
var ErrNoFindings = errors.New("the reply contains no review block")

// Parse extracts a Draft from an agent's message.
func Parse(text string) (Draft, error) {
	block, ok := lastFencedBlock(text, FenceTag)
	if !ok {
		return Draft{}, ErrNoFindings
	}
	var draft Draft
	if err := json.Unmarshal([]byte(block), &draft); err != nil {
		return Draft{}, fmt.Errorf("the review block is not valid JSON: %w", err)
	}
	return draft.Normalise(), nil
}

// lastFencedBlock returns the contents of the last ```tag fenced block.
//
// Scanned line by line rather than with a regular expression because a review
// legitimately contains fenced code inside its own findings (a suggestion is a
// block of code), and a greedy pattern would swallow from the first fence to the
// last backtick in the message.
func lastFencedBlock(text, tag string) (string, bool) {
	lines := strings.Split(text, "\n")
	var found string
	var buf []string
	open := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !open {
			if isOpeningFence(trimmed, tag) {
				open, buf = true, nil
			}
			continue
		}
		if strings.HasPrefix(trimmed, "```") {
			found = strings.Join(buf, "\n")
			open = false
			continue
		}
		buf = append(buf, line)
	}
	// An unterminated final block still counts: the message may have been cut
	// short, and the JSON either parses or it does not.
	if open && len(buf) > 0 {
		found = strings.Join(buf, "\n")
	}
	return found, found != ""
}

func isOpeningFence(trimmed, tag string) bool {
	if !strings.HasPrefix(trimmed, "```") {
		return false
	}
	label := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
	return label == tag || label == "json"
}
