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

## Live validation (done)

Run against a real Codex (ChatGPT Go) account, all passing:

1. **Single-turn** — `pong` round-trips through the proxy (status 200).
2. **Multi-turn tool-use** — the model calls a tool, the tool_result is sent back,
   and the final answer returns (status 200).
3. **Reasoning replay** — a turn producing a thinking block with a 1100-char
   signature is replayed in the next turn; **Codex accepts it (status 200)**. The
   reference's reasoning-replay *cache* exists for clients that DROP reasoning
   between turns; the `claude` CLI replays the full assistant message itself and the
   ported translator preserves the encrypted content, so **no server-side cache is
   needed for kunai's client.** This was the main risk and it does not bite.
4. **Real `claude` CLI end-to-end** — `claude -p` with `ANTHROPIC_BASE_URL` pointed
   at the proxy returns `pong` from real Codex.
5. **Full kunai session** — a session on a Codex provider, driven over the real
   WebSocket with `-native-codex`, returns `pong`, and the 40MB sidecar is **never
   downloaded**.

(The live tests are gated on `KUNAI_CODEX_LIVE=1 KUNAI_CODEX_TOKEN=<path>`; the proxy
reads the token file itself.)

## Wired into the server

`-native-codex` (env `KUNAI_NATIVE_CODEX=1`) routes a Codex provider through this
proxy: `nativeCodexManager` (internal/server/nativecodex.go) serves it on a localhost
port, `providerProfile` bakes that as the provider's `ANTHROPIC_BASE_URL`, and the
sidecar is skipped entirely (boot and create paths) when every provider is native or
external. Off by default.

## Still to do

- **Login.** Provider OAuth login still shells the sidecar (`cliproxy_login.go`).
  Porting the Codex device/loopback login natively would let the sidecar be dropped
  completely; until then it is fetched only for the login step.
- **Grok/Kimi.** Different upstreams; the same pattern applies, not built here.
- Default-on once it has run a while in nightly.

## Verification status

- Offline: 47 tests pass (42 translator golden + 3 proxy round-trip + 2 server
  wiring). `go build`, `go test ./...`, `-race` green.
- Live: 5 scenarios above pass against a real Codex account.
