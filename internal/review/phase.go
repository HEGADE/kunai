package review

// A review as a sequence of phases rather than one question.
//
// The old shape was a single prompt that asked for everything at once, ending
// with an instruction to "re-examine every finding and delete the ones you
// cannot demonstrate". That is the model marking its own homework in the same
// breath it wrote it, and it does not work: the characteristic failure of
// machine review is confident nonsense, and nothing about asking the author of a
// claim whether the claim is true will catch it. Asking harder does not help.
// A separate pass, with a fresh context that has not already committed to the
// answer, does.
//
// So a review runs in phases, and this file is the state machine that walks
// them. It is deliberately pure: it takes the text an agent replied with, reads
// it, and says what to ask next. Nothing here knows about sessions, GitHub or
// HTTP, which is what lets the whole progression be tested against fixtures
// instead of against a live CLI.
//
//	Survey ──▶ Find ──▶ Verify ──▶ Done
//	   │                   │
//	   └─ skipped on a     └─ skipped when every candidate
//	      small change        already came back demonstrated
//
// Both skips are the answer to the obvious objection, which is cost. A phased
// review that always ran every phase would spend three times as much on a
// two-file pull request as the single-shot one did, to find the same nothing.
// The phases are therefore earned: a small change goes straight to Find, and a
// Find that produced only demonstrated findings has nothing for Verify to do.

import (
	"fmt"
	"strings"
)

// Phase is where a review has got to.
type Phase string

const (
	// PhaseSurvey reads the shape of the change before looking for anything
	// wrong with it: what is this trying to do, and where is the risk. Its
	// output makes the Find phase targeted instead of exhaustive, which is what
	// keeps a large pull request affordable.
	PhaseSurvey Phase = "survey"
	// PhaseFind hunts. Deliberately generous, because Verify is what filters:
	// a finder that self-censors to protect its precision loses real bugs, and
	// recall is the half that cannot be recovered later.
	PhaseFind Phase = "find"
	// PhaseVerify tries to REFUTE each candidate from a fresh context. This is
	// the phase that makes the difference, and the one the old design lacked.
	PhaseVerify Phase = "verify"
	// PhaseDone means the findings are settled and ready to be placed.
	PhaseDone Phase = "done"
)

// Thresholds for skipping the survey. A change under both of these is small
// enough that a plan for reading it costs more than reading it.
//
// Two conditions rather than one because they fail differently: six small files
// is a wide change worth planning, and one file with nine hundred new lines is a
// deep one. Either alone is a poor proxy for "big".
const (
	surveyFileThreshold = 6
	surveyLineThreshold = 400
)

// Run is one review in progress.
//
// It holds everything the phases have produced so far, which is also everything
// the UI wants to show while it is happening: what the change is for, what is
// being checked, what survived and what did not.
type Run struct {
	Req   Request
	Phase Phase

	// Survey is what the first phase concluded, empty when it was skipped.
	Survey Survey
	// Candidates are the findings as Find produced them, before verification.
	Candidates []Finding
	// Summary is the review's overall read, from the Find phase.
	Summary string
	// Dropped are the candidates verification refuted, kept with the reason.
	//
	// Kept rather than discarded on purpose. A reviewer you can audit is one you
	// will trust: "4 considered and dropped" with the reasons one click away
	// says the filtering is real, where silently showing three findings looks
	// identical to a reviewer that only found three.
	Dropped []Dropped

	// repairing is the complaint to put to the agent when its last answer could
	// not be read, and repairs counts how often that has happened in this phase.
	//
	// A review is minutes of work and several dollars of tokens, and it was being
	// thrown away whole over a missing fence. One real run ended on "the reply
	// contains no review block" after 32 model calls, having read the code, found
	// whatever it found, and written it up in a shape the parser did not accept.
	// Everything in that reply still existed; only the wrapper was wrong. Asking
	// once for the block alone recovers all of it for the price of one turn.
	repairing string
	repairs   int
}

// maxRepairs is how many times one phase may be asked again for an answer that
// could not be read.
//
// One. A model that has now been shown the exact format twice and produced
// something unreadable both times is not going to get there on the third ask, and
// each attempt costs another turn against a context that is already large.
const maxRepairs = 1

// Survey is the first phase's answer: what this change is for, and where to look.
type Survey struct {
	// Intent is the change's purpose in the reviewer's own words. Stated because
	// most real bugs are a mismatch between what the code does and what it was
	// FOR, and that comparison is impossible without naming the second half.
	Intent string `json:"intent"`
	// Areas are the places worth real attention, most important first.
	Areas []Area `json:"areas"`
}

// Area is one thing worth checking, with the files it lives in.
type Area struct {
	What  string   `json:"what"`
	Files []string `json:"files"`
	Why   string   `json:"why"`
}

// Dropped is a candidate that did not survive verification.
type Dropped struct {
	Finding Finding `json:"finding"`
	// Why is the refutation, in the words of whatever checked it.
	Why string `json:"why"`
}

// NewRun starts a review at its first phase, choosing whether the survey is
// worth running on a change this size.
func NewRun(req Request) *Run {
	r := &Run{Req: req, Phase: PhaseSurvey}
	if !worthSurveying(req.Files) {
		r.Phase = PhaseFind
	}
	return r
}

// Resumed rebuilds a run that was interrupted, from what was written down.
//
// The state machine is a pure reducer -- Accept(text) is (state, event) -> state
// and nothing else -- which is exactly what makes this possible: a run is its
// phase plus what the phases before it produced, so a run that died can be put
// back together from the record and asked the one question it never answered.
//
// It exists because the alternative is what kunai did for months, which is start
// again from the first phase. Measured on one real pull request in a single
// evening: $45.72 spent across four attempts at #7, of which $20.77 bought
// nothing at all, because every interruption -- a permission ask nobody was
// there to answer, a restart, somebody pressing stop -- threw away the survey
// and the find phase along with it. The survey and the candidates cost minutes
// and dollars and were already on the record; only the phase that did not finish
// needs asking again.
//
// Verification is the phase this matters most for, and it is also the one that
// resumes most cleanly: it runs in a session of its own with nothing but the
// claims, so a verify that never finished can be started again from the
// candidates alone at no loss of fidelity whatsoever.
func Resumed(req Request, phase Phase, survey Survey, candidates []Finding, summary string, dropped []Dropped) *Run {
	if phase == "" || phase == PhaseDone {
		phase = PhaseFind
	}
	// A phase cannot be resumed into a state its inputs cannot support. Verify
	// with no candidates has nothing to check, and find with no survey is simply
	// find; both fall back rather than asking a question with a hole in it.
	if phase == PhaseVerify && len(candidates) == 0 {
		phase = PhaseFind
	}
	if phase == PhaseSurvey && !worthSurveying(req.Files) {
		phase = PhaseFind
	}
	return &Run{
		Req: req, Phase: phase, Survey: survey,
		Candidates: candidates, Summary: summary, Dropped: dropped,
	}
}

// Resumable reports whether there is anything left to ask, which is the question
// the UI has to answer before offering to resume at all.
func Resumable(phase Phase) bool {
	return phase != "" && phase != PhaseDone
}

// worthSurveying reports whether a change is big enough that planning how to
// read it beats simply reading it.
func worthSurveying(files []FileSummary) bool {
	if len(files) >= surveyFileThreshold {
		return true
	}
	lines := 0
	for _, f := range files {
		lines += f.Additions + f.Deletions
	}
	return lines >= surveyLineThreshold
}

// Next is the prompt to send for the current phase, with the one-line brief that
// stands in for it in the transcript. ok is false when the review is finished.
func (r *Run) Next() (prompt, brief string, ok bool) {
	// An answer that could not be read is asked for again before the phase's own
	// question is repeated. Repeating the question would make it do the work
	// twice; what went wrong was the wrapper, not the reading.
	if r.repairing != "" {
		return RepairPrompt(r.Phase, r.repairing), "Ask again for the answer block", true
	}
	switch r.Phase {
	case PhaseSurvey:
		return SurveyPrompt(r.Req), fmt.Sprintf("Survey %s#%d", r.Req.Repo, r.Req.Number), true
	case PhaseFind:
		return FindPrompt(r.Req, r.Survey), fmt.Sprintf("Review %s#%d", r.Req.Repo, r.Req.Number), true
	case PhaseVerify:
		return VerifyPrompt(r.Req, r.Candidates), fmt.Sprintf("Check %d finding(s)", len(r.Candidates)), true
	default:
		return "", "", false
	}
}

// repair records that this phase's answer was unreadable and asks for it again,
// reporting whether there is another attempt left.
func (r *Run) repair(err error) bool {
	if r.repairs >= maxRepairs {
		return false
	}
	r.repairs++
	r.repairing = err.Error()
	return true
}

// settle clears the repair state as a phase completes, so the next phase starts
// with its own full allowance.
func (r *Run) settle() { r.repairing, r.repairs = "", 0 }

// Accept reads the agent's reply for the current phase and advances.
//
// The failure policy differs per phase, because the phases are not equally
// load-bearing. A survey that cannot be read costs nothing: the review carries
// on without a plan, which is exactly what a small pull request does anyway. A
// Find that cannot be read is the review, so it is an error. A Verify that
// cannot be read must NOT silently promote unverified claims, so the candidates
// survive but stay marked unverified, and the hedge in their posted comment is
// what keeps that honest.
func (r *Run) Accept(text string) error {
	switch r.Phase {
	case PhaseSurvey:
		if s, err := ParseSurvey(text); err == nil {
			r.Survey = s
		}
		r.settle()
		r.Phase = PhaseFind
		return nil

	case PhaseFind:
		draft, err := Parse(text)
		if err != nil {
			// Asked again before being given up on: the reading was done, only the
			// wrapper was wrong, and a whole review is too much to lose to a fence.
			if r.repair(err) {
				return nil
			}
			return err
		}
		r.settle()
		r.Summary = draft.Summary
		r.Candidates = draft.Findings
		r.Phase = PhaseVerify
		if !needsVerification(r.Candidates) {
			r.Phase = PhaseDone
		}
		return nil

	case PhaseVerify:
		verdicts, err := ParseVerdicts(text)
		if err != nil {
			if r.repair(err) {
				return nil
			}
			// Left unverified rather than dropped or promoted. See above.
			r.settle()
			r.Phase = PhaseDone
			return nil
		}
		r.settle()
		r.Phase = PhaseDone
		r.applyVerdicts(verdicts)
		return nil

	default:
		return nil
	}
}

// needsVerification reports whether there is anything for the second pass to do,
// which is now simply whether anything was found.
//
// It used to skip the phase when every candidate came back marked "high", on the
// reasoning that a finding the finder called demonstrated has by definition been
// demonstrated. That was circular, and measurably so: it asks the finder whether
// the finder needs checking, which is the same question the single-shot prompt
// asked and the same one this whole engine was built to stop asking. The prompt
// then made it worse by SAYING so ("a finding you mark high is posted without
// further checking"), which is a straightforward invitation to mark everything
// high.
//
// It was taken. Across every review this engine completed before the rule
// changed, 5 findings out of 5 came back "high" and the verification phase never
// ran once; every card in the UI carried "Not independently checked" and nobody
// could tell that from a reviewer that simply had not been asked to check.
//
// So confidence is now a report to the reader and nothing else, and the pass runs
// on everything that could be posted. The cost is real and it is the right trade:
// verification fans out to one subagent per claim, each with a small fresh
// context, which is a fraction of what the find phase spends and is the only
// thing standing between a plausible wrong claim and somebody's pull request.
func needsVerification(candidates []Finding) bool { return len(candidates) > 0 }

// applyVerdicts folds the verification pass back into the candidates.
//
// A verdict may lower a severity or a confidence but never raise either, and
// that asymmetry is deliberate. Verification exists to catch claims that are
// too strong; letting it promote as well would give a second chance to inflate
// every finding it was meant to restrain, and a "blocker" arrived at by two
// passes agreeing with each other is not more true than one.
func (r *Run) applyVerdicts(verdicts []Verdict) {
	byIndex := make(map[int]Verdict, len(verdicts))
	for _, v := range verdicts {
		byIndex[v.Index] = v
	}

	kept := make([]Finding, 0, len(r.Candidates))
	for i, f := range r.Candidates {
		v, judged := byIndex[i]
		// A verdict naming a different file than the claim at that position has
		// drifted, and applying it would pair a refutation with an unrelated
		// finding. Treated as no verdict at all rather than trusted: the cost is
		// one finding shown unverified, where the alternative cost is dropping a
		// real bug because something else was refuted.
		if judged && v.File != "" && v.File != f.File {
			judged = false
		}
		if !judged {
			// Nothing came back about this one. It stays, unverified: dropping a
			// finding because the verifier forgot to mention it would lose real
			// bugs to a formatting slip.
			kept = append(kept, f)
			continue
		}
		if !v.Stands {
			r.Dropped = append(r.Dropped, Dropped{Finding: f, Why: strings.TrimSpace(v.Note)})
			continue
		}
		f.Verified = true
		if v.Severity != "" && normaliseSeverity(v.Severity).Rank() > f.Severity.Rank() {
			f.Severity = normaliseSeverity(v.Severity)
		}
		if v.Confidence != "" && confidenceRank(normaliseConfidence(v.Confidence)) > confidenceRank(f.Confidence) {
			f.Confidence = normaliseConfidence(v.Confidence)
		}
		if note := strings.TrimSpace(v.Note); note != "" {
			f.Evidence = note
		}
		kept = append(kept, f)
	}
	r.Candidates = kept
}

// confidenceRank orders confidence for the "never raise" comparison above.
// Higher is less certain, matching Severity.Rank's direction so both
// comparisons in applyVerdicts read the same way.
func confidenceRank(c Confidence) int {
	switch c {
	case ConfidenceHigh:
		return 0
	case ConfidenceMedium:
		return 1
	default:
		return 2
	}
}

// Draft is the review as it stands, ready to be placed against the diff.
func (r *Run) Draft() Draft {
	return Draft{Summary: r.Summary, Findings: r.Candidates}.Normalise()
}
