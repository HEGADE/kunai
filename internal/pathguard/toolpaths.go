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
//
// The table is per tool rather than one shared list of field names, and that is
// the correction to an earlier version that was not. A shared list has to answer
// "is `pattern` a path?" the same way for every tool, and it is not: Glob's
// pattern is a path expression that can begin with `/` or climb out with `..`,
// while Grep's is a regular expression that must never be resolved as a path.
// The shared list dropped `pattern` entirely to avoid the second case, which
// left Glob able to enumerate the whole filesystem.

// spec is what this package knows about one tool's input.
type spec struct {
	// fields are the input keys that carry a filesystem path.
	fields []string
	// needsPath says a call of this tool ALWAYS names a path, so an input that
	// yields none has a shape we do not recognise. That is "could not verify",
	// not "nothing to check", and the guard must refuse it: a renamed field in a
	// later CLI, or a value that is not a string, would otherwise sail through the
	// containment check by having nothing to contain.
	needsPath bool
}

// tools is every tool the guard can actually check, and how.
//
// Anything absent is refused by the caller rather than guessed at, because a
// tool nobody has looked at is a tool whose path fields nobody knows.
var tools = map[string]spec{
	// One file, named outright.
	"Read":         {fields: []string{"file_path", "path"}, needsPath: true},
	"Write":        {fields: []string{"file_path", "path"}, needsPath: true},
	"Edit":         {fields: []string{"file_path", "path"}, needsPath: true},
	"MultiEdit":    {fields: []string{"file_path", "path"}, needsPath: true},
	"NotebookEdit": {fields: []string{"notebook_path", "file_path", "path"}, needsPath: true},

	// Glob's `pattern` is required and IS a path expression: "/etc/**" and
	// "../../*.pem" are both accepted by the tool and both leave the folder. It is
	// checked like any other path, which means a pattern is resolved against the
	// root the way a relative path is. A legitimate "**/*.go" lands inside and
	// passes; the only patterns this refuses are the ones that climb out.
	"Glob": {fields: []string{"pattern", "path"}, needsPath: true},

	// Grep's `pattern` is a REGULAR EXPRESSION and must not be resolved as a path,
	// or every search for "../" would be denied. Its `path` is optional and
	// defaults to the working directory, which is a root by construction, so an
	// input with no path is a complete and safe call rather than an unknown shape.
	// `glob` is a filename filter applied within `path` and cannot widen it.
	"Grep": {fields: []string{"path"}},

	// These touch no filesystem path at all, so there is nothing to confine and
	// nothing missing when none is found.
	"WebSearch":       {},
	"WebFetch":        {},
	"TodoWrite":       {},
	"AskUserQuestion": {},
}

// Guarded reports whether this package can check the given tool's arguments.
func Guarded(tool string) bool {
	_, ok := tools[tool]
	return ok
}

// ToolPaths returns every filesystem path named in a tool call's input, and
// whether the input had a shape this package recognises.
//
// ok=false means "could not verify": the tool is one that always names a path
// and this call named none, so the containment check has nothing to work with.
// Callers must treat that as a refusal. Reading the boolean is not optional —
// it is the whole difference between "there is nothing outside the roots here"
// and "we could not tell".
func ToolPaths(tool string, input json.RawMessage) (paths []string, ok bool) {
	sp, known := tools[tool]
	if !known {
		return nil, false
	}
	var obj map[string]any
	if len(input) == 0 || json.Unmarshal(input, &obj) != nil {
		// No arguments at all is a recognisable shape only for a tool that needs no
		// path; for anything else it is an input we failed to read.
		return nil, !sp.needsPath
	}
	for _, key := range sp.fields {
		if v, isStr := obj[key].(string); isStr && v != "" {
			paths = append(paths, v)
		}
	}
	// MultiEdit carries its own list, each entry a separate change with its own
	// path. Missing this would let one call touch a dozen files unchecked.
	if edits, isList := obj["edits"].([]any); isList {
		for _, e := range edits {
			m, isObj := e.(map[string]any)
			if !isObj {
				continue
			}
			for _, key := range sp.fields {
				if v, isStr := m[key].(string); isStr && v != "" {
					paths = append(paths, v)
				}
			}
		}
	}
	if sp.needsPath && len(paths) == 0 {
		return nil, false
	}
	return paths, true
}
