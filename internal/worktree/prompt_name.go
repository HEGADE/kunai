package worktree

import (
	"regexp"
	"strings"
)

// Naming a worktree after the work, rather than asking for a name.
//
// The idea is t3code's and it is the right one: nobody wants to name a branch
// before they have described the task, because describing the task IS the name.
// t3code creates a throwaway branch, then has a model read your first message and
// rename the branch from it.
//
// kunai does the same thing without the model call, for two reasons. Its launcher
// already has the text at the moment the worktree is made, so there is nothing to
// defer; and a model call here would be spent from the same subscription the work
// itself runs on, to rewrite a sentence the user already wrote. Slugging their own
// words is free, offline, deterministic, and testable, and for a sentence like
// "fix the login redirect loop" it lands in the same place a model would.
//
// Where it is worse is a rambling prompt, and that is the accepted cost: a long
// prompt gives a name from its opening words rather than its gist.

// nameWords is how many words survive into a branch name. Four or five reads as
// a branch; more reads as a sentence someone forgot to stop writing.
const nameWords = 5

// maxNameRunes caps the result. t3code caps at 64; this is shorter because the
// name also becomes a directory under the repository's own folder, and a path
// you cannot read at a glance defeats the point of naming it.
const maxNameRunes = 40

// leadIn strips the throat-clearing a prompt usually opens with, so the name
// starts at the verb. "please can you fix the login" names itself "fix-login",
// not "please-can-you-fix".
var leadIn = regexp.MustCompile(`(?i)^\s*(?:` +
	`please|could you|can you|would you|i(?:'| a)?m? ?(?:want|need|would like)(?: you)?(?: to)?|` +
	`let'?s|lets|we should|we need to|help me|hey|hi|ok(?:ay)?|so|now|next` +
	`)\b[\s,]*`)

// filler are words that carry no signal in a branch name. Deliberately short:
// articles, common prepositions, pronouns and auxiliaries. Verbs and nouns are
// what a name is made of, so nothing that could be one is listed here.
var filler = map[string]bool{
	"a": true, "an": true, "the": true, "this": true, "that": true, "these": true,
	"those": true, "and": true, "or": true, "but": true, "of": true, "to": true,
	"in": true, "on": true, "at": true, "for": true, "with": true, "from": true,
	"by": true, "into": true, "is": true, "are": true, "was": true, "were": true,
	"be": true, "been": true, "it": true, "its": true, "my": true, "our": true,
	"your": true, "their": true, "me": true, "us": true, "you": true, "we": true,
	"i": true, "should": true, "would": true, "could": true, "can": true,
	"will": true, "just": true, "some": true, "any": true, "there": true,
}

// NameFromPrompt derives a branch name from what the user asked for.
//
// Empty when the prompt yields nothing usable, so the caller can fall back to a
// placeholder rather than being handed a name that says nothing.
func NameFromPrompt(prompt string) string {
	text := firstLine(prompt)
	// Repeatedly, because the throat-clearing stacks: "could you please fix …"
	// is two of these, and stripping one leaves the other in the name.
	for i := 0; i < 4; i++ {
		stripped := leadIn.ReplaceAllString(text, "")
		if stripped == text {
			break
		}
		text = stripped
	}

	var kept []string
	for _, raw := range strings.Fields(text) {
		word := Slug(raw)
		if !usable(raw, word) || filler[word] {
			continue
		}
		kept = append(kept, word)
		if len(kept) == nameWords {
			break
		}
	}
	if len(kept) == 0 {
		// Every word was filler ("can you do it for me"). Keep the filler rather
		// than nothing: a poor name beats a placeholder that says nothing. Still
		// only words the user actually wrote, so a prompt of pure punctuation
		// still yields nothing and the caller falls back to a placeholder.
		for _, raw := range strings.Fields(text) {
			if word := Slug(raw); word != "" && usable(raw, word) {
				kept = append(kept, word)
			}
			if len(kept) == nameWords {
				break
			}
		}
	}
	if len(kept) == 0 {
		return ""
	}
	return trimRunes(Slug(strings.Join(kept, "-")), maxNameRunes)
}

// firstLine keeps a multi-paragraph prompt from naming itself after its third
// paragraph. A pasted stack trace or a long brief still names itself from its
// opening sentence, which is where people put the point.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			// A sentence is enough; the rest is elaboration. But only punctuation
			// that actually ends a sentence counts: the dot in "spa.go" is part of
			// a filename, and cutting there named the work after half a path.
			if i := sentenceEnd(t); i > 0 {
				t = t[:i]
			}
			return t
		}
	}
	return ""
}

// usable rejects Slug's own fallback, which means the word contributed nothing
// the user actually wrote (punctuation, an emoji) rather than being the word
// "work".
func usable(raw, slugged string) bool {
	return slugged != "" && !(slugged == placeholderName && !strings.EqualFold(raw, placeholderName))
}

// sentenceEnd finds where the first sentence stops: punctuation followed by a
// space or the end of the line. A dot inside a word is part of the word.
func sentenceEnd(s string) int {
	for i, r := range s {
		if r != '.' && r != '?' && r != '!' {
			continue
		}
		rest := strings.TrimLeft(s[i:], ".?!")
		if rest == "" || rest[0] == ' ' || rest[0] == '\t' {
			return i
		}
	}
	return -1
}

// trimRunes cuts to at most n runes, then back to the last whole word so a name
// never ends mid-syllable.
func trimRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := string(r[:n])
	if i := strings.LastIndexByte(cut, '-'); i > 0 {
		cut = cut[:i]
	}
	return strings.Trim(cut, "-")
}

// --- placeholders ---------------------------------------------------------------

// placeholderName is what a worktree is called when it is made with nothing to
// name it after: the sidebar's one-tap start, which deliberately asks nothing.
//
// t3code uses random hex here so a placeholder is unmistakable. This uses a word,
// because kunai's placeholder is also a directory the user may well end up in on
// a terminal, and "work" is a better thing to find yourself in than "a1b2c3d4".
// The two are told apart by shape instead, which is what IsPlaceholder is for.
const placeholderName = "work"

// PlaceholderName is the name to use when there is nothing to name a worktree
// after yet.
func PlaceholderName() string { return placeholderName }

// placeholderRE matches kunai/work and its collision suffixes, and nothing a
// person would have chosen.
var placeholderRE = regexp.MustCompile(`^` + regexp.QuoteMeta(BranchPrefix) + placeholderName + `(?:-\d+)?$`)

// IsPlaceholder reports whether a branch is still the name given to a worktree
// that had nothing to name it after, and so is worth replacing once the user has
// said what they are doing.
func IsPlaceholder(branch string) bool { return placeholderRE.MatchString(branch) }
