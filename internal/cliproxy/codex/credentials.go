package codex

// Credentials for something that is not the proxy.
//
// The quota reader (internal/server/codexusage.go) needs exactly what a request
// needs: a token that works right now. It used to read auth.json and send
// whatever was in it, which is fine until the access token lapses -- and then it
// posts a dead token every minute forever, with a live refresh token sitting in
// the same file it just read. That is what put "Codex: no quota" on the
// dashboard while kunai held everything required to fix it.
//
// So the refresh lives here, once, and both callers use it.

import (
	"context"
	"sync"
)

var (
	mgrMu sync.Mutex
	mgrs  = map[string]*tokenManager{}
)

// managerFor keeps one manager per token file.
//
// Per path rather than per call, because a manager is what makes concurrent
// refreshes collapse into one: a fresh manager each time would mean each caller
// refreshing separately, and a provider that rotates refresh tokens would
// invalidate its own siblings mid-flight.
func managerFor(path string, owns bool) *tokenManager {
	mgrMu.Lock()
	defer mgrMu.Unlock()
	if m := mgrs[path]; m != nil {
		return m
	}
	m := newTokenManager(path, owns)
	mgrs[path] = m
	return m
}

// Credentials returns a usable access token and ChatGPT account id for the login
// at path, refreshing first when the stored token has expired or is about to.
//
// owns says whether kunai may write the refreshed token back, and it is false
// for the codex CLI's own ~/.codex/auth.json: that file belongs to the CLI, and
// stomping it would be kunai reaching into another tool's state. The cost of not
// owning it is that the refresh is held in memory only, so a kunai restart
// starts again from whatever the CLI last wrote.
func Credentials(ctx context.Context, path string, owns bool) (access, account string, err error) {
	return managerFor(path, owns).creds(ctx)
}
