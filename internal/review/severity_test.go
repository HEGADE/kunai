package review

import (
	"strings"
	"testing"
)

// A model asked for a severity does not reliably use the vocabulary it was
// given. Every synonym here is one a model actually reaches for when rating
// something, and dropping a real blocker because it arrived labelled "critical"
// would be a self-inflicted wound.
func TestNormaliseSeverityAcceptsWhatModelsActuallyWrite(t *testing.T) {
	for _, tc := range []struct {
		in   Severity
		want Severity
	}{
		{"blocker", SeverityBlocker},
		{"critical", SeverityBlocker},
		{"HIGH", SeverityBlocker},
		{"  Blocking ", SeverityBlocker},
		{"major", SeverityMajor},
		{"medium", SeverityMajor},
		{"important", SeverityMajor},
		{"minor", SeverityMinor},
		{"nit", SeverityMinor},
		{"low", SeverityMinor},
		{"suggestion", SeverityMinor},
	} {
		if got := normaliseSeverity(tc.in); got != tc.want {
			t.Errorf("normaliseSeverity(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// An omitted or unrecognised severity must land on the middle rung. Minor would
// let a mislabelled blocker hide at the bottom of somebody's review; blocker
// would let any parse slip shout.
func TestUnknownSeverityIsMajor(t *testing.T) {
	for _, in := range []Severity{"", "   ", "urgent-ish", "P0"} {
		if got := normaliseSeverity(in); got != SeverityMajor {
			t.Errorf("normaliseSeverity(%q) = %q, want %q", in, got, SeverityMajor)
		}
	}
}

// The verification phase skips only what is already high, so an unlabelled
// finding must NOT arrive labelled high: that would let every finding that
// forgot the field bypass the one pass that exists to catch confident nonsense.
func TestUnknownConfidenceIsMediumSoItGetsVerified(t *testing.T) {
	for _, in := range []Confidence{"", "  ", "pretty sure", "0.9"} {
		if got := normaliseConfidence(in); got != ConfidenceMedium {
			t.Errorf("normaliseConfidence(%q) = %q, want %q", in, got, ConfidenceMedium)
		}
	}
	if got := normaliseConfidence("certain"); got != ConfidenceHigh {
		t.Errorf("normaliseConfidence(certain) = %q, want high", got)
	}
	if got := normaliseConfidence("speculative"); got != ConfidenceLow {
		t.Errorf("normaliseConfidence(speculative) = %q, want low", got)
	}
}

// An unrecognised severity sorts LAST. A value nobody recognises must not be
// able to push itself to the top of a review.
func TestUnknownSeveritySortsLast(t *testing.T) {
	if Severity("wat").Rank() <= SeverityMinor.Rank() {
		t.Fatalf("an unknown severity outranked minor")
	}
}

func TestNormaliseCategoryFoldsOntoTheKnownSet(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"perf", CategoryPerformance},
		{"Performance", CategoryPerformance},
		{"race", CategoryConcurrency},
		{"breaking_change", CategoryCompat},
		{"breaking change", CategoryCompat},
		{"bug", CategoryCorrectness},
		{"astrology", CategoryOther},
		{"", CategoryOther},
	} {
		if got := normaliseCategory(tc.in); got != tc.want {
			t.Errorf("normaliseCategory(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The order at the top of the list is what a reader relies on to stop reading,
// so it is kept by the schema rather than hoped for from the model.
func TestNormaliseSortsMostSeriousFirst(t *testing.T) {
	d := Draft{Findings: []Finding{
		{Title: "a nit", Severity: SeverityMinor},
		{Title: "the real one", Severity: SeverityBlocker},
		{Title: "a risk", Severity: SeverityMajor},
	}}.Normalise()

	want := []string{"the real one", "a risk", "a nit"}
	for i, w := range want {
		if d.Findings[i].Title != w {
			t.Fatalf("position %d = %q, want %q (order: %v)", i, d.Findings[i].Title, w, titles(d))
		}
	}
}

// Stable within a severity: the reviewer ranked these itself, and reordering
// equally-serious findings for no reason destroys that judgement.
func TestSortIsStableWithinASeverity(t *testing.T) {
	d := Draft{Findings: []Finding{
		{Title: "first major", Severity: SeverityMajor},
		{Title: "second major", Severity: SeverityMajor},
		{Title: "third major", Severity: SeverityMajor},
	}}.Normalise()

	want := []string{"first major", "second major", "third major"}
	for i, w := range want {
		if d.Findings[i].Title != w {
			t.Fatalf("position %d = %q, want %q", i, d.Findings[i].Title, w)
		}
	}
}

func TestCountsAreTheHeadline(t *testing.T) {
	d := Draft{Findings: []Finding{
		{Title: "a", Severity: SeverityBlocker},
		{Title: "b", Severity: SeverityMinor},
		{Title: "c", Severity: SeverityMajor},
		{Title: "d", Severity: SeverityMinor},
	}}.Normalise()

	blocker, major, minor := d.Counts()
	if blocker != 1 || major != 1 || minor != 2 {
		t.Fatalf("Counts() = %d/%d/%d, want 1/1/2", blocker, major, minor)
	}
}

func titles(d Draft) []string {
	out := make([]string, 0, len(d.Findings))
	for _, f := range d.Findings {
		out = append(out, f.Title)
	}
	return out
}

// The author of the pull request reads an inline comment in a notification
// email with no other context, so the severity has to be in the first words.
func TestCommentBodyLeadsWithSeverity(t *testing.T) {
	body := CommentBody(Finding{
		Title: "the retry loop never terminates", Severity: SeverityBlocker,
		Confidence: ConfidenceHigh,
	})
	if want := "**Blocker: the retry loop never terminates**"; !strings.Contains(body, want) {
		t.Fatalf("CommentBody() = %q, want it to contain %q", body, want)
	}
	// A high-confidence finding must not be annotated: labelling every finding
	// with its confidence is noise that trains people to skip the line.
	if strings.Contains(body, "<sub>") {
		t.Errorf("a high-confidence finding was hedged: %q", body)
	}
}

// A finding the review could not demonstrate says so. This looks like weakness
// and is the opposite: hedging when unsure is what earns belief when it is not.
func TestCommentBodyHedgesWhenConfidenceIsNotHigh(t *testing.T) {
	for _, tc := range []struct {
		c    Confidence
		want string
	}{
		{ConfidenceMedium, "assumption the review could not confirm"},
		{ConfidenceLow, "suspicion rather than a demonstrated bug"},
	} {
		body := CommentBody(Finding{Title: "x", Severity: SeverityMinor, Confidence: tc.c})
		if !strings.Contains(body, tc.want) {
			t.Errorf("confidence %q produced %q, want it to mention %q", tc.c, body, tc.want)
		}
	}
}
