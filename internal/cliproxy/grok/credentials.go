package grok

// Credentials for something that is not the proxy. See the sibling file in
// internal/cliproxy/codex for why this exists: the quota reader was sending
// whatever auth.json held, so an expired token meant a 401 a minute forever
// while the refresh token needed to fix it sat in the same file.
//
// It matters more here than for Codex. xAI rotates refresh tokens and revokes
// the old one on each refresh, which is why the manager writes the rotated token
// back (persistLocked). A second, independent manager for the same file would
// therefore refresh with a token the first one has already had revoked -- so the
// manager is shared per path rather than created per call, and that is a
// correctness requirement here rather than an optimisation.

import (
	"context"
	"sync"
)

var (
	mgrMu sync.Mutex
	mgrs  = map[string]*tokenManager{}
)

func managerFor(path string) *tokenManager {
	mgrMu.Lock()
	defer mgrMu.Unlock()
	if m := mgrs[path]; m != nil {
		return m
	}
	m := newTokenManager(path)
	mgrs[path] = m
	return m
}

// Token returns a usable grok session token for the login at path, refreshing
// and persisting the rotated token first when the stored one has expired.
//
// A login that genuinely cannot refresh returns the actionable "run `grok` to
// sign in again" error rather than a bare 401, because that is the one thing the
// caller can do about it.
func Token(ctx context.Context, path string) (string, error) {
	return managerFor(path).token(ctx)
}
