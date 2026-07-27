// Package pathguard answers one question: is this path inside one of these
// folders, really.
//
// "Really" is the whole package. Comparing cleaned strings is not enough,
// because a symlink inside the folder can point anywhere, and the check has to
// see through it. This started as an unexported helper in internal/telegram,
// guarding what a chat could ask kunai to send back. It is here because a shared
// session needs exactly the same answer about a tool call's arguments, and two
// copies of a containment check is one copy too many.
package pathguard

import (
	"errors"
	"path/filepath"
	"strings"
)

// ErrOutside means the path resolved somewhere none of the roots contain.
var ErrOutside = errors.New("that path is outside the folders this session may touch")

// ErrNoRoot means there was nothing to check against, which is refused rather
// than treated as "anything goes".
var ErrNoRoot = errors.New("no folder was given to check against")

// Resolve returns the absolute, symlink-resolved form of rel if it lands inside
// root, and ErrOutside otherwise. A relative path is taken as relative to root.
//
// Symlinks are resolved BEFORE the containment check, on both sides, which is
// the point of the function: a link inside the folder pointing at /etc would
// otherwise pass a string comparison and then read /etc.
//
// A path that does not exist yet is still checked, by resolving the deepest
// parent that does. Without that, creating a file through a symlinked directory
// would be allowed because the leaf could not be resolved.
func Resolve(root, rel string) (string, error) {
	if root == "" {
		return "", ErrNoRoot
	}
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		base = filepath.Clean(root)
	}

	target := rel
	if !filepath.IsAbs(target) {
		target = filepath.Join(base, target)
	}
	target = filepath.Clean(target)
	target = resolveDeepest(target)

	if !within(base, target) {
		return "", ErrOutside
	}
	return target, nil
}

// ResolveAny is Resolve against several roots: a session may have been given
// more than one codebase, and a path inside any of them is inside the session.
// The first root that contains it wins.
func ResolveAny(roots []string, rel string) (string, error) {
	if len(roots) == 0 {
		return "", ErrNoRoot
	}
	var lastErr error = ErrOutside
	for _, root := range roots {
		got, err := Resolve(root, rel)
		if err == nil {
			return got, nil
		}
		if !errors.Is(err, ErrOutside) {
			lastErr = err
		}
	}
	return "", lastErr
}

// Inside reports whether rel lands inside any of roots. The boolean form, for
// callers that only need to decide rather than to use the resolved path.
func Inside(roots []string, rel string) bool {
	_, err := ResolveAny(roots, rel)
	return err == nil
}

// resolveDeepest resolves the longest existing prefix of p and re-appends the
// rest. A path being created does not exist yet, so EvalSymlinks fails on it
// whole; resolving only what is there still catches a symlinked parent, which is
// how a write escapes a folder that a read could not.
func resolveDeepest(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	dir, leaf := filepath.Split(p)
	dir = filepath.Clean(dir)
	// Stop at the filesystem root rather than recursing forever.
	if dir == p || dir == string(filepath.Separator) || dir == "." {
		return p
	}
	return filepath.Join(resolveDeepest(dir), leaf)
}

// within reports containment on already-resolved paths. Separator-aware, so
// "/repo-secrets" is not inside "/repo".
func within(base, target string) bool {
	if target == base {
		return true
	}
	return strings.HasPrefix(target, base+string(filepath.Separator))
}
