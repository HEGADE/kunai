package review

import (
	"strings"
	"testing"
)

func testMeta() Meta {
	return Meta{Owner: "lyzr", Repo: "kunai", HeadSHA: "abc123", Requester: "@shorya"}
}

// A finding that could not be anchored is still precise: the summary carries a
// link to exactly the line, pinned to the reviewed commit so it survives the next
// push. Without that, "this breaks the caller over here" is a treasure hunt.
func TestBodyLinksDemotedFindingsToTheExactLine(t *testing.T) {
	plan := Build(Draft{
		Summary:  "The change is sound, with one caller left behind.",
		Findings: []Finding{{File: "internal/app/caller.go", Line: 88, Title: "still calls old()", Body: "It will not compile."}},
	}, testAnchors())

	body := Body(plan, testMeta())
	want := "https://github.com/lyzr/kunai/blob/abc123/internal/app/caller.go#L88"
	if !strings.Contains(body, want) {
		t.Errorf("body does not link to the line:\n%s", body)
	}
	if !strings.Contains(body, "The change is sound") {
		t.Error("the summary was lost")
	}
	if !strings.Contains(body, "It will not compile.") {
		t.Error("the finding's explanation was lost")
	}
}

// With one bot identity shared across a team, a review nobody can be traced to is
// a review nobody can be asked about.
func TestBodyNamesTheRequester(t *testing.T) {
	body := Body(Build(Draft{Summary: "Fine."}, testAnchors()), testMeta())
	if !strings.Contains(body, "@shorya") {
		t.Errorf("the requester is not named:\n%s", body)
	}
	// And with nobody named it still says who wrote it, rather than trailing off.
	anon := Body(Build(Draft{Summary: "Fine."}, testAnchors()), Meta{})
	if !strings.Contains(anon, "kunai") {
		t.Errorf("an unattributed review should still name the tool:\n%s", anon)
	}
}

// The fence has to be exactly ```suggestion or GitHub renders a code block
// instead of the Apply button, which turns a one-click fix into a copy-paste.
func TestCommentBodyRendersASuggestionBlock(t *testing.T) {
	body := CommentBody(Finding{
		Title:      "nil check missing",
		Body:       "cfg can be nil here.",
		Suggestion: "if cfg == nil {\n\treturn nil\n}",
	})
	if !strings.Contains(body, "```suggestion\n") {
		t.Errorf("no suggestion fence:\n%s", body)
	}
	if !strings.Contains(body, "if cfg == nil {") {
		t.Errorf("the suggested code was lost:\n%s", body)
	}
	if !strings.HasSuffix(strings.TrimSpace(body), "```") {
		t.Errorf("the suggestion block is not closed:\n%s", body)
	}
}

// A trailing newline inside a suggestion adds a blank line to somebody's file
// when they click Apply, which is a small real defect introduced by the reviewer.
func TestSuggestionTrailingNewlineIsTrimmed(t *testing.T) {
	f := Finding{Title: "t", Suggestion: "x := 1\n\n"}.Normalise()
	if strings.HasSuffix(f.Suggestion, "\n") {
		t.Errorf("suggestion still ends in a newline: %q", f.Suggestion)
	}
}

// A path with segments is escaped per segment: escaping the whole path turns
// every slash into %2F and the link goes nowhere.
func TestPermalinkKeepsPathSeparators(t *testing.T) {
	link := permalink(Finding{File: "internal/some dir/file.go", Line: 3}, testMeta())
	if strings.Contains(link, "%2F") {
		t.Errorf("path separators were escaped away: %s", link)
	}
	if !strings.Contains(link, "some%20dir") {
		t.Errorf("the space in the path was not escaped: %s", link)
	}
}

// Finding nothing is a result worth reporting. Silence and "I checked, it looks
// right" are not the same message.
func TestEmptyBodySaysSo(t *testing.T) {
	body := EmptyBody(testMeta())
	if !strings.Contains(body, "No issues found") || !strings.Contains(body, "@shorya") {
		t.Errorf("empty review body reads badly:\n%s", body)
	}
}
