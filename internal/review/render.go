package review

// What the review looks like when it lands on the pull request.
//
// Two audiences, one of them not in the room: the author of the pull request will
// read this without any of the context you had when you clicked Post. So the body
// says who asked for it (a shared bot identity otherwise makes every review
// anonymous), the findings that could not be anchored carry links precise enough
// to act on, and nothing is padded. A review that found nothing says so in a line.

import (
	"fmt"
	"net/url"
	"strings"
)

// Meta is what the rendered review needs to know about where it is going.
type Meta struct {
	Owner string
	Repo  string
	// HeadSHA pins permalinks to the commit that was reviewed, so a link still
	// points at the code the finding is about after the branch moves on.
	HeadSHA string
	// Requester is the GitHub or local username of whoever clicked Review. With
	// one shared bot identity across a team, this is the only thing that says who
	// to ask about a finding.
	Requester string
}

// Body renders the review's summary comment: the overall read, then any findings
// that could not be attached to a line.
func Body(plan Plan, meta Meta) string {
	var b strings.Builder

	summary := strings.TrimSpace(plan.Summary)
	if summary == "" {
		summary = "No summary was produced for this review."
	}
	b.WriteString(summary)

	if demoted := plan.Demoted(); len(demoted) > 0 {
		b.WriteString("\n\n### Findings outside this diff\n\n")
		b.WriteString("These are about code the pull request does not change, so GitHub cannot show them inline.\n")
		for _, pl := range demoted {
			b.WriteString("\n")
			b.WriteString(demotedEntry(pl, meta))
		}
	}

	b.WriteString("\n\n")
	b.WriteString(signature(meta))
	return b.String()
}

// demotedEntry is one finding in the summary, led by a link to exactly the line
// it is about so it is as actionable as an inline comment would have been.
func demotedEntry(pl Placement, meta Meta) string {
	f := pl.Finding
	var b strings.Builder

	b.WriteString("- ")
	if location := permalink(f, meta); location != "" {
		fmt.Fprintf(&b, "[%s](%s)", locationLabel(f), location)
		b.WriteString(" ")
	}
	b.WriteString("**")
	b.WriteString(f.Title)
	b.WriteString("**")
	if f.Body != "" {
		b.WriteString("\n  ")
		b.WriteString(strings.ReplaceAll(f.Body, "\n", "\n  "))
	}
	if pl.Why != "" {
		fmt.Fprintf(&b, "\n  <sub>%s</sub>", pl.Why)
	}
	return b.String()
}

func locationLabel(f Finding) string {
	if f.File == "" {
		return ""
	}
	if f.Line > 0 {
		return fmt.Sprintf("`%s:%d`", f.File, f.Line)
	}
	return "`" + f.File + "`"
}

// permalink points at the file and line as of the reviewed commit. Blob links are
// pinned to a SHA rather than a branch precisely so they survive the next push.
func permalink(f Finding, meta Meta) string {
	if f.File == "" || meta.Owner == "" || meta.Repo == "" || meta.HeadSHA == "" {
		return ""
	}
	// Each path segment is escaped, but the separators are kept: url.PathEscape
	// over the whole path would turn every slash into %2F and break the link.
	parts := strings.Split(f.File, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	link := fmt.Sprintf("https://github.com/%s/%s/blob/%s/%s",
		url.PathEscape(meta.Owner), url.PathEscape(meta.Repo), meta.HeadSHA, strings.Join(parts, "/"))
	if f.Line > 0 {
		link += fmt.Sprintf("#L%d", f.Line)
		if f.EndLine > f.Line {
			link += fmt.Sprintf("-L%d", f.EndLine)
		}
	}
	return link
}

// CommentBody renders one inline comment: the claim, the reasoning, and a
// suggested change when the fix is small enough to be offered as one.
func CommentBody(f Finding) string {
	var b strings.Builder
	if f.Title != "" {
		b.WriteString("**")
		b.WriteString(f.Title)
		b.WriteString("**")
	}
	if f.Body != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(f.Body)
	}
	if f.Suggestion != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		// The fence must be exactly ```suggestion for GitHub to render the Apply
		// button, and the block replaces precisely the anchored lines.
		b.WriteString("```suggestion\n")
		b.WriteString(f.Suggestion)
		b.WriteString("\n```")
	}
	return b.String()
}

// signature is the one line of attribution. With a shared bot identity across a
// team, a review with no requester is a review nobody can be asked about.
func signature(meta Meta) string {
	if meta.Requester == "" {
		return "<sub>Reviewed by kunai.</sub>"
	}
	return fmt.Sprintf("<sub>Reviewed by kunai, requested by %s.</sub>", meta.Requester)
}

// EmptyBody is the review posted when nothing was found. Saying so in one line is
// a good review: it is the difference between "checked, looks right" and silence,
// which are not the same message.
func EmptyBody(meta Meta) string {
	return "No issues found in this diff.\n\n" + signature(meta)
}
