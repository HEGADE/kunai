# internal/cliproxy/codex — native Codex proxy

A kunai-native replacement for the CLIProxyAPI sidecar, for a **Codex (ChatGPT)**
provider. It speaks the Anthropic Messages API the `claude` CLI calls, translates
to Codex's `/responses` API, calls ChatGPT's backend with the account's OAuth
token, and stream-translates the reply back to Anthropic SSE.

## Why this exists

The managed sidecar works but costs 40 MB downloaded per machine, plus supervision,
codesigning, and async-port fragility. Embedding CLIProxyAPI's SDK instead is worse
for size (measured: kunai 9.3 MB -> 37.7 MB). This package is the third option the
owner asked for: **build our own, using CLIProxyAPI only as a reference for the wire
formats.**

Measured cost: **+0.41 MB** to kunai's stripped binary (9.33 -> 9.74 MB), versus
40 MB for the sidecar. ~100x smaller, no separate process, no download, no signing.

## What is ported vs written

- `translate_request.go`, `translate_response.go`, `translate_response_websearch.go`
  — the Anthropic<->Codex translator, **ported verbatim** from CLIProxyAPI
  (`internal/translator/codex/claude`), MIT. Proven by its own golden tests
  (`translate_*_test.go`, 42 cases: thinking signatures, tool-call streaming,
  stop-reason mapping, web search).
- `sig_*.go` — the signature-compatibility validators the translator needs, ported
  whole from CLIProxyAPI `internal/signature` (adds one dep: `protowire`).
- `helpers_*.go` — the ~15 small helpers the translator calls, ported from
  CLIProxyAPI `internal/translator/common`, `internal/util`, `internal/thinking`.
- `auth.go` — **written for kunai**: loads the Codex OAuth token the sidecar login
  wrote (or `~/.codex/auth.json`) and refreshes it against `auth.openai.com`.
- `proxy.go` — **written for kunai**: the HTTP handler, upstream call with the
  Codex headers, and the streaming pump. Proven by `proxy_test.go` (round-trip
  against a mock upstream: auth headers reach upstream, Codex SSE becomes valid
  Anthropic SSE, errors pass through).

## What is NOT done yet (honest gaps)

1. **Reasoning-replay / signature cache.** CLIProxyAPI's executor caches encrypted
   reasoning content and replays it with signatures across turns; some multi-turn
   reasoning sequences need this or Codex rejects them with "invalid signature".
   This package does not reproduce that cache yet. Single-turn and ordinary tool
   use go through the same translator and should work; the replay edge cases need a
   **live token** to exercise and fix, which could not be done offline.
2. **Not wired into the server.** kunai still uses the sidecar. Swapping in this
   proxy means: a manager that starts a `*codex.Proxy` on a localhost port (in
   place of `cliproxyManager`), pointing a provider's `ANTHROPIC_BASE_URL` at it,
   and keeping the sidecar only for provider **login** (the OAuth flows are still
   sidecar-shelled; porting login is a separate step).
3. **Codex only.** Grok/Kimi ride different upstreams; the same approach applies but
   is not built here.

## Verification status

- Offline: 45 tests pass (42 translator golden + 3 proxy round-trip). `go build`,
  `go test ./...`, `-race` green.
- Live: NOT yet run against a real Codex account (needs the owner's token; would
  consume quota). The reasoning-replay gap in particular can only be confirmed live.
