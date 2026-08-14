package review

import (
	"strings"
	"testing"
)

// A small change goes straight to Find. The survey exists to make a big diff
// affordable to read; on a two-file change, planning how to read it costs more
// than reading it.
func TestSmallChangeSkipsTheSurvey(t *testing.T) {
	r := NewRun(Request{Files: []FileSummary{
		{Path: "a.go", Additions: 20, Deletions: 3},
		{Path: "b.go", Additions: 10, Deletions: 1},
	}})
	if r.Phase != PhaseFind {
		t.Fatalf("phase = %q, want %q", r.Phase, PhaseFind)
	}
}

// Wide and deep are different shapes of "big" and either one earns a survey.
func TestBigChangeEarnsASurvey(t *testing.T) {
	wide := NewRun(Request{Files: []FileSummary{
		{Path: "a"}, {Path: "b"}, {Path: "c"}, {Path: "d"}, {Path: "e"}, {Path: "f"},
	}})
	if wide.Phase != PhaseSurvey {
		t.Errorf("six files: phase = %q, want %q", wide.Phase, PhaseSurvey)
	}

	deep := NewRun(Request{Files: []FileSummary{{Path: "a.go", Additions: 900}}})
	if deep.Phase != PhaseSurvey {
		t.Errorf("900 added lines: phase = %q, want %q", deep.Phase, PhaseSurvey)
	}
}

// A survey that cannot be read costs nothing: the review carries on without a
// plan, which is exactly what a small pull request does anyway.
func TestUnreadableSurveyIsSurvived(t *testing.T) {
	r := &Run{Phase: PhaseSurvey}
	if err := r.Accept("I could not produce a plan, sorry."); err != nil {
		t.Fatalf("Accept() returned %v, want nil", err)
	}
	if r.Phase != PhaseFind {
		t.Fatalf("phase = %q, want %q", r.Phase, PhaseFind)
	}
}

// An unreadable Find is asked for again before it is given up on. A whole
// review is minutes of work and dollars of tokens, and the reading survives in
// the context: only the wrapper was wrong.
func TestUnreadableFindIsAskedForAgain(t *testing.T) {
	r := &Run{Phase: PhaseFind}
	if err := r.Accept("no block here"); err != nil {
		t.Fatalf("Accept() = %v, want the first unreadable answer to be repaired", err)
	}
	if r.Phase != PhaseFind {
		t.Fatalf("phase = %q, want to stay on %q while repairing", r.Phase, PhaseFind)
	}
	// And what it asks for is the block, not the review over again.
	prompt, _, ok := r.Next()
	if !ok {
		t.Fatal("Next() = !ok, want a repair prompt")
	}
	if strings.Contains(prompt, "## What to look for") {
		t.Error("the repair prompt repeats the whole review question, so the reading is paid for twice")
	}
	if !strings.Contains(prompt, FenceTag) {
		t.Error("the repair prompt does not say which block it wants")
	}
}

// Asked twice and still unreadable IS the review failing, and must be an error
// rather than an empty review reported as a clean bill of health.
func TestFindUnreadableTwiceIsAnError(t *testing.T) {
	r := &Run{Phase: PhaseFind}
	if err := r.Accept("no block here"); err != nil {
		t.Fatalf("first Accept() = %v, want a repair", err)
	}
	if err := r.Accept("still no block"); err == nil {
		t.Fatal("second Accept() returned nil, want an error")
	}
}

// A phase that recovers gets its full allowance back, or a review that stumbled
// once early would have no slack left where it mattered.
func TestRepairAllowanceIsPerPhase(t *testing.T) {
	r := &Run{Phase: PhaseFind}
	if err := r.Accept("no block here"); err != nil {
		t.Fatalf("Accept() = %v", err)
	}
	if err := r.Accept(findBlock(`{"file":"a.go","line":1,"title":"x","body":"y","severity":"major","confidence":"high"}`)); err != nil {
		t.Fatalf("Accept() = %v", err)
	}
	if r.Phase != PhaseVerify {
		t.Fatalf("phase = %q, want %q", r.Phase, PhaseVerify)
	}
	if err := r.Accept("no verdicts here"); err != nil {
		t.Fatalf("Accept() = %v, want verify to have its own repair", err)
	}
	if r.Phase != PhaseVerify {
		t.Fatalf("phase = %q, want verify to stay open for its repair", r.Phase)
	}
}

// The rule the whole engine rests on: verification runs on everything that could
// be posted, whatever the finder said about itself.
//
// This is the inverse of what it used to assert. Skipping the pass for findings
// marked "high" asks the finder whether the finder needs checking, and the
// finder answered yes to itself every single time: across every review completed
// under the old rule, 5 findings out of 5 came back "high" and the verification
// phase never ran once.
func TestVerificationRunsEvenOnDemonstratedFindings(t *testing.T) {
	r := &Run{Phase: PhaseFind}
	if err := r.Accept(findBlock(`
      {"file":"a.go","line":1,"title":"x","body":"y","severity":"major","confidence":"high"},
      {"file":"b.go","line":2,"title":"z","body":"w","severity":"minor","confidence":"high"}
    `)); err != nil {
		t.Fatalf("Accept() = %v", err)
	}
	if r.Phase != PhaseVerify {
		t.Fatalf("phase = %q, want %q: a finder's own confidence must not skip the check", r.Phase, PhaseVerify)
	}
}

// One unproven candidate is enough to earn the pass.
func TestOneUnprovenCandidateEarnsVerification(t *testing.T) {
	r := &Run{Phase: PhaseFind}
	if err := r.Accept(findBlock(`
      {"file":"a.go","line":1,"title":"x","body":"y","severity":"major","confidence":"high"},
      {"file":"b.go","line":2,"title":"z","body":"w","severity":"major","confidence":"low"}
    `)); err != nil {
		t.Fatalf("Accept() = %v", err)
	}
	if r.Phase != PhaseVerify {
		t.Fatalf("phase = %q, want %q", r.Phase, PhaseVerify)
	}
}

// A finding with no findings at all has nothing to verify.
func TestNoFindingsGoesStraightToDone(t *testing.T) {
	r := &Run{Phase: PhaseFind}
	if err := r.Accept(findBlock("")); err != nil {
		t.Fatalf("Accept() = %v", err)
	}
	if r.Phase != PhaseDone {
		t.Fatalf("phase = %q, want %q", r.Phase, PhaseDone)
	}
}

// The point of the whole rewrite: a refuted claim is dropped, and the reason is
// kept so the filtering can be audited rather than taken on trust.
//
// Note the order. The find phase sorts by severity, so the blocker is claim 0
// and the major is claim 1, which is exactly the order the verifier is shown.
func TestRefutedFindingsAreDroppedWithTheirReason(t *testing.T) {
	r := runAtVerify(t, `
      {"file":"a.go","line":1,"title":"real bug","body":"y","severity":"major","confidence":"medium"},
      {"file":"b.go","line":2,"title":"imagined bug","body":"w","severity":"blocker","confidence":"low"}
    `)
	if err := r.Accept(verdictBlock(`
      {"index":0,"file":"b.go","stands":false,"note":"the wrapper already guards this"},
      {"index":1,"file":"a.go","stands":true,"note":"confirmed: the caller can pass nil"}
    `)); err != nil {
		t.Fatalf("Accept() = %v", err)
	}

	if len(r.Candidates) != 1 || r.Candidates[0].Title != "real bug" {
		t.Fatalf("candidates = %+v, want just the real bug", r.Candidates)
	}
	if !r.Candidates[0].Verified {
		t.Error("a confirmed finding was not marked verified")
	}
	if len(r.Dropped) != 1 || r.Dropped[0].Finding.Title != "imagined bug" {
		t.Fatalf("dropped = %+v, want the imagined bug", r.Dropped)
	}
	if !strings.Contains(r.Dropped[0].Why, "wrapper already guards") {
		t.Errorf("dropped reason = %q, want the refutation", r.Dropped[0].Why)
	}
}

// A verdict that names a different file than the claim at its index has drifted,
// and applying it would pair a refutation with an unrelated finding: a real bug
// dropped because something else was refuted. Discarded rather than trusted, so
// the finding merely goes unverified.
func TestAMispairedVerdictIsDiscardedRatherThanApplied(t *testing.T) {
	r := runAtVerify(t, `{"file":"a.go","line":1,"title":"real bug","body":"y","severity":"major","confidence":"medium"}`)
	if err := r.Accept(verdictBlock(`{"index":0,"file":"somewhere-else.go","stands":false,"note":"nope"}`)); err != nil {
		t.Fatalf("Accept() = %v", err)
	}
	if len(r.Candidates) != 1 {
		t.Fatalf("a mispaired verdict dropped the finding: %+v", r.Dropped)
	}
	if r.Candidates[0].Verified {
		t.Error("a mispaired verdict was treated as a verification")
	}
}

// Verification restrains claims; it does not get to reinforce them. A pass
// allowed to promote would give every finding a second chance to inflate, and a
// blocker two passes agreed on is not more true than one.
func TestAVerdictMayLowerSeverityButNeverRaiseIt(t *testing.T) {
	// Sorted by severity, so the blocker is claim 0 and the minor is claim 1.
	r := runAtVerify(t, `
      {"file":"a.go","line":1,"title":"overstated","body":"y","severity":"blocker","confidence":"medium"},
      {"file":"b.go","line":2,"title":"understated","body":"w","severity":"minor","confidence":"medium"}
    `)
	if err := r.Accept(verdictBlock(`
      {"index":0,"file":"a.go","stands":true,"severity":"minor","confidence":"high","note":"real but cosmetic"},
      {"index":1,"file":"b.go","stands":true,"severity":"blocker","confidence":"high","note":"actually terrible"}
    `)); err != nil {
		t.Fatalf("Accept() = %v", err)
	}

	if got := r.Candidates[0].Severity; got != SeverityMinor {
		t.Errorf("a lowered severity was not applied: got %q, want minor", got)
	}
	if got := r.Candidates[1].Severity; got != SeverityMinor {
		t.Errorf("a verdict raised a severity: got %q, want it held at minor", got)
	}
}

// Same asymmetry for confidence, and for the same reason.
func TestAVerdictMayNotRaiseConfidence(t *testing.T) {
	r := runAtVerify(t, `{"file":"a.go","line":1,"title":"x","body":"y","severity":"major","confidence":"low"}`)
	if err := r.Accept(verdictBlock(`{"index":0,"stands":true,"confidence":"high","note":"sure"}`)); err != nil {
		t.Fatalf("Accept() = %v", err)
	}
	if got := r.Candidates[0].Confidence; got != ConfidenceLow {
		t.Errorf("a verdict raised confidence to %q, want it held at low", got)
	}
}

// A verifier that forgets to mention a claim must not delete it: losing a real
// bug to a formatting slip is a worse failure than showing one unverified.
func TestAnUnjudgedCandidateSurvivesUnverified(t *testing.T) {
	r := runAtVerify(t, `
      {"file":"a.go","line":1,"title":"judged","body":"y","severity":"major","confidence":"medium"},
      {"file":"b.go","line":2,"title":"forgotten","body":"w","severity":"major","confidence":"medium"}
    `)
	if err := r.Accept(verdictBlock(`{"index":0,"stands":true,"note":"fine"}`)); err != nil {
		t.Fatalf("Accept() = %v", err)
	}
	if len(r.Candidates) != 2 {
		t.Fatalf("candidates = %d, want both kept", len(r.Candidates))
	}
	if r.Candidates[1].Verified {
		t.Error("an unjudged finding was marked verified")
	}
}

// A verification pass that cannot be read must neither drop the findings nor
// promote them: they survive, marked unverified, and the hedge in their posted
// comment is what keeps that honest.
func TestUnreadableVerdictsLeaveFindingsUnverified(t *testing.T) {
	r := runAtVerify(t, `{"file":"a.go","line":1,"title":"x","body":"y","severity":"major","confidence":"medium"}`)
	// Asked once more first, then given up on: the same allowance every phase has.
	if err := r.Accept("the checker crashed"); err != nil {
		t.Fatalf("Accept() = %v", err)
	}
	if err := r.Accept("the checker crashed again"); err != nil {
		t.Fatalf("Accept() = %v", err)
	}
	if r.Phase != PhaseDone {
		t.Fatalf("phase = %q, want %q", r.Phase, PhaseDone)
	}
	if len(r.Candidates) != 1 {
		t.Fatalf("candidates = %d, want the finding kept", len(r.Candidates))
	}
	if r.Candidates[0].Verified {
		t.Error("a finding was marked verified by a verification pass that did not parse")
	}
}

// The verifier is asked for one entry per claim and told the index refers to the
// list it was shown, so the prompt must actually number them from zero.
func TestVerifyPromptNumbersTheClaimsFromZero(t *testing.T) {
	p := VerifyPrompt([]Finding{
		{File: "a.go", Line: 7, Title: "first", Severity: SeverityMajor, Confidence: ConfidenceLow},
		{File: "b.go", Line: 9, Title: "second", Severity: SeverityMinor, Confidence: ConfidenceMedium},
	})
	for _, want := range []string{"### 0. first", "### 1. second", "`a.go` line 7"} {
		if !strings.Contains(p, want) {
			t.Errorf("VerifyPrompt() is missing %q", want)
		}
	}
}

// The single most important instruction in the package. A verifier that gives
// the benefit of the doubt confirms everything, and a pass that confirms
// everything is worse than no pass, because it stamps claims nobody checked.
func TestVerifyPromptDefaultsToRefuted(t *testing.T) {
	p := VerifyPrompt([]Finding{{File: "a.go", Title: "x"}})
	if !strings.Contains(p, "Default to refuted") {
		t.Error("VerifyPrompt() does not tell the verifier to default to refuted")
	}
}

// The survey's areas have to reach the find phase or the phase bought nothing.
func TestFindPromptCarriesTheSurvey(t *testing.T) {
	p := FindPrompt(
		Request{Repo: "o/r", Number: 1, DiffPath: "d.diff"},
		Survey{
			Intent: "adds a retry loop",
			Areas:  []Area{{What: "the backoff bound", Files: []string{"retry.go"}, Why: "it can spin"}},
		},
	)
	for _, want := range []string{"adds a retry loop", "the backoff bound", "retry.go", "it can spin"} {
		if !strings.Contains(p, want) {
			t.Errorf("FindPrompt() is missing %q", want)
		}
	}
}

// A run with no survey must not print an empty heading for one.
func TestFindPromptOmitsAnEmptySurvey(t *testing.T) {
	p := FindPrompt(Request{Repo: "o/r", Number: 1, DiffPath: "d.diff"}, Survey{})
	if strings.Contains(p, "What this change is for") {
		t.Error("FindPrompt() printed a survey heading with no survey")
	}
}

// ParseVerdicts takes either shape, because both are what a model produces when
// asked for a list and throwing away a whole pass over a wrapper is not a trade
// worth making.
func TestParseVerdictsAcceptsBareArrayOrWrappedObject(t *testing.T) {
	wrapped, err := ParseVerdicts(verdictBlock(`{"index":0,"stands":true,"note":"a"}`))
	if err != nil || len(wrapped) != 1 {
		t.Fatalf("wrapped: got %v, %v", wrapped, err)
	}
	bare, err := ParseVerdicts("```" + VerdictFenceTag + "\n[{\"index\":0,\"stands\":true,\"note\":\"a\"}]\n```")
	if err != nil || len(bare) != 1 {
		t.Fatalf("bare: got %v, %v", bare, err)
	}
}

func runAtVerify(t *testing.T, findings string) *Run {
	t.Helper()
	r := &Run{Phase: PhaseFind}
	if err := r.Accept(findBlock(findings)); err != nil {
		t.Fatalf("setting up the verify phase: %v", err)
	}
	if r.Phase != PhaseVerify {
		t.Fatalf("setup did not reach the verify phase, got %q", r.Phase)
	}
	return r
}

func findBlock(findings string) string {
	return "```" + FenceTag + "\n{\"summary\":\"s\",\"findings\":[" + findings + "]}\n```"
}

func verdictBlock(verdicts string) string {
	return "```" + VerdictFenceTag + "\n{\"verdicts\":[" + verdicts + "]}\n```"
}
