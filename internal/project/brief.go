package project

import (
	"fmt"
	"strings"
)

// Brief renders the Info as the note handed to the model when a project joins a
// session. It is deliberately a description and not an instruction: it says
// what is there and where, and leaves what to do with it to the conversation.
// Everything here is cheap to read and slow to change, so it stays true for the
// life of the session.
func (i Info) Brief() string {
	var b strings.Builder
	b.WriteString("Another project has been added to this session for context.\n\n")
	fmt.Fprintf(&b, "%s\n%s\n", i.Name, i.Path)

	if i.Branch != "" || i.Remote != "" {
		b.WriteString("\ngit: ")
		if i.Branch != "" {
			b.WriteString(i.Branch)
		}
		if i.Remote != "" {
			if i.Branch != "" {
				b.WriteString(" · ")
			}
			b.WriteString(i.Remote)
		}
		b.WriteString("\n")
	}
	if len(i.Langs) > 0 {
		parts := make([]string, 0, len(i.Langs))
		for _, l := range i.Langs {
			parts = append(parts, fmt.Sprintf("%s %d", l.Name, l.Files))
		}
		fmt.Fprintf(&b, "languages (files): %s\n", strings.Join(parts, ", "))
	}
	if i.Files > 0 {
		fmt.Fprintf(&b, "files: %d\n", i.Files)
	}
	if len(i.Dirs) > 0 {
		fmt.Fprintf(&b, "top level: %s\n", strings.Join(i.Dirs, ", "))
	}
	if len(i.Docs) > 0 {
		fmt.Fprintf(&b, "docs: %s\n", strings.Join(i.Docs, ", "))
	}
	if len(i.Build) > 0 {
		fmt.Fprintf(&b, "build: %s\n", strings.Join(i.Build, ", "))
	}

	b.WriteString(
		"\nThis is metadata only — nothing here has been read for you. " +
			"Its files are on the same machine, so read them from the path above when you need them. " +
			"Reply with one short line confirming you have it; do not summarise the project.",
	)
	return b.String()
}
