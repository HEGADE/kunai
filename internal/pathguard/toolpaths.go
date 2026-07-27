package pathguard

import "encoding/json"

// Reading the filesystem paths out of a tool call.
//
// This is the input side of the guard, and its honesty depends entirely on
// knowing which fields of which tools are paths. That is a closed set only
// because the tools that can take an unbounded path have already been removed at
// spawn for a shared session: Bash cannot be inspected this way at all (a shell
// command has no path field, it has a string that MEANS a path after the shell
// is done with it), and Task can call anything at one remove. Both are denied
// outright rather than guarded here.
//
// So the rule this file encodes is narrow and can be stated: for the tools a
// guest's agent is allowed to use, every argument that names a file is checked.

// pathFields are the tool-input keys that carry a filesystem path. Shared across
// tools rather than keyed by tool name, because the CLI names them consistently
// and a per-tool table would silently miss a tool nobody added to it.
var pathFields = []string{
	"file_path",     // Read, Write, Edit
	"path",          // Glob, Grep, and older spellings of the above
	"notebook_path", // NotebookEdit
}

// ToolPaths returns every filesystem path named in a tool call's input.
//
// Unknown shapes yield nothing, and that is the dangerous direction, so callers
// must treat "no paths found" as "could not verify" rather than "safe" for any
// tool they did not expect. UnknownShape reports that case.
func ToolPaths(input json.RawMessage) []string {
	if len(input) == 0 {
		return nil
	}
	var obj map[string]any
	if json.Unmarshal(input, &obj) != nil {
		return nil
	}
	var out []string
	for _, key := range pathFields {
		if v, ok := obj[key].(string); ok && v != "" {
			out = append(out, v)
		}
	}
	// MultiEdit carries its own list, each entry a separate change with its own
	// path. Missing this would let one call touch a dozen files unchecked.
	if edits, ok := obj["edits"].([]any); ok {
		for _, e := range edits {
			m, ok := e.(map[string]any)
			if !ok {
				continue
			}
			for _, key := range pathFields {
				if v, ok := m[key].(string); ok && v != "" {
					out = append(out, v)
				}
			}
		}
	}
	return out
}

// GuardedTools are the tools whose arguments this package can actually check.
// Anything outside this set that reaches a guard is refused rather than guessed
// at, because a tool nobody has looked at is a tool whose path fields nobody
// knows.
var GuardedTools = map[string]bool{
	"Read":         true,
	"Write":        true,
	"Edit":         true,
	"MultiEdit":    true,
	"NotebookEdit": true,
	"Glob":         true,
	"Grep":         true,
	// These touch no filesystem path at all, so there is nothing to confine.
	"WebSearch":       true,
	"WebFetch":        true,
	"TodoWrite":       true,
	"AskUserQuestion": true,
}
