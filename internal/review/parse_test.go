package review

import (
	"errors"
	"strings"
	"testing"
)

// The whole contract with the agent: prose, then a fenced block. Everything
// before the block is its working and is deliberately ignored here, because it is
// worth reading in the chat and worth nothing to the parser.
func TestParseReadsTheFencedBlock(t *testing.T) {
	msg := "I read the diff and the callers.\n\n" +
		"```" + FenceTag + "\n" +
		`{"summary":"Solid.","findings":[{"file":"run.go","line":11,"title":"unchecked"}]}` + "\n" +
		"```\n"

	draft, err := Parse(msg)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Summary != "Solid." || len(draft.Findings) != 1 {
		t.Fatalf("got %+v", draft)
	}
	if draft.Findings[0].Side != SideRight {
		t.Errorf("side = %q, want the parsed draft already normalised", draft.Findings[0].Side)
	}
}

// A model that reconsiders emits a corrected block after the first, so the LAST
// one is the answer. Taking the first would post findings the agent itself
// withdrew.
func TestParseTakesTheLastBlock(t *testing.T) {
	msg := "```" + FenceTag + "\n" + `{"summary":"first"}` + "\n```\n" +
		"On reflection:\n\n" +
		"```" + FenceTag + "\n" + `{"summary":"second"}` + "\n```\n"

	draft, err := Parse(msg)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Summary != "second" {
		t.Errorf("summary = %q, want the reconsidered block", draft.Summary)
	}
}

// A review legitimately contains fenced code inside its own findings, so the
// scanner must not run from the first fence to the last backtick in the message.
func TestParseSurvivesCodeFencesInsideTheReview(t *testing.T) {
	msg := "Here is the problem:\n\n```go\nfunc broken() {}\n```\n\n" +
		"```" + FenceTag + "\n" +
		`{"summary":"ok","findings":[{"file":"a.go","line":2,"title":"t","suggestion":"x := 1"}]}` + "\n" +
		"```\n"

	draft, err := Parse(msg)
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.Findings) != 1 || draft.Findings[0].Suggestion != "x := 1" {
		t.Fatalf("got %+v", draft.Findings)
	}
}

// "The review found nothing" and "the review did not finish" are different
// things, and the reader must not have to guess which happened.
func TestParseDistinguishesNoBlockFromNoFindings(t *testing.T) {
	if _, err := Parse("I had a look and everything seems fine."); !errors.Is(err, ErrNoFindings) {
		t.Errorf("a reply with no block should be ErrNoFindings, got %v", err)
	}

	empty := "```" + FenceTag + "\n" + `{"summary":"Nothing to report.","findings":[]}` + "\n```"
	draft, err := Parse(empty)
	if err != nil {
		t.Fatalf("an empty findings list is a valid review, got %v", err)
	}
	if len(draft.Findings) != 0 || draft.Summary == "" {
		t.Fatalf("got %+v, want a summary and no findings", draft)
	}
}

// Malformed JSON is not salvaged. A half-parsed review would post findings whose
// line numbers came from a guess, and no review is much better than a wrong one.
func TestParseRefusesMalformedJSON(t *testing.T) {
	msg := "```" + FenceTag + "\n{\"summary\": \"broken\", \n```"
	_, err := Parse(msg)
	if err == nil {
		t.Fatal("malformed JSON was accepted")
	}
	if errors.Is(err, ErrNoFindings) {
		t.Error("malformed JSON should not read as a missing block")
	}
	if !strings.Contains(err.Error(), "JSON") {
		t.Errorf("error = %q, want it to name the problem", err)
	}
}

// A message cut short mid-block still parses if the JSON happens to be complete,
// because the alternative is losing a finished review to a missing final fence.
func TestParseAcceptsAnUnterminatedBlock(t *testing.T) {
	msg := "```" + FenceTag + "\n" + `{"summary":"cut off"}`
	draft, err := Parse(msg)
	if err != nil || draft.Summary != "cut off" {
		t.Fatalf("got %+v, %v", draft, err)
	}
}
