# internal/cliproxy/grok — native Grok (xAI) proxy

A kunai-native proxy for a Grok provider, the sibling of internal/cliproxy/codex.
It accepts the Anthropic Messages API the `claude` CLI speaks, translates to xAI's
`/responses` format, and calls xAI's CLI chat-proxy with the grok CLI's session
token. No CLIProxyAPI sidecar.

## How it reuses Codex

xAI's `/responses` is the same OpenAI-Responses shape Codex uses, so the
Anthropic<->Responses translation is **reused verbatim** from the codex package
(`codex.ConvertClaudeRequestToCodex` / `ConvertCodexResponseToClaude`). Only three
things are Grok-specific and live here:

- **Endpoint**: `https://cli-chat-proxy.grok.com/v1/responses`.
- **Auth** (`auth.go`): reads the grok CLI login at `~/.grok/auth.json` (the session
  token under the single `<issuer>::<id>` key), refreshing against the OIDC issuer
  when near expiry.
- **Headers**: `Authorization: Bearer <key>`, `X-XAI-Token-Auth: xai-grok-cli`,
  `x-grok-client-version`, `User-Agent: xai-grok-workspace/<ver>`.

## Wired into the server

`-native-grok` (env `KUNAI_NATIVE_GROK=1`) routes a Grok provider through this proxy:
`nativeGrokManager` (internal/server/nativegrok.go) serves it on a localhost port and
`providerProfile` bakes it as the provider's `ANTHROPIC_BASE_URL`; the sidecar is
skipped when every provider is native or external. Off by default. Grok's login is
the grok CLI's own, so there is no separate login flow here (run `grok` to log in).

## Verification status

- Offline: `TestGrokTokenParse`, `TestGrokProxyRoundTrip` (mock upstream, auth headers
  reach it, Responses SSE -> Anthropic SSE), plus the server routing test.
- Live (KUNAI_GROK_LIVE=1, real Grok grok-4.5): single-turn `pong`, multi-turn
  tool-use (calls a tool, returns 391), real `claude` CLI end to end, and a full
  kunai WebSocket session on a Grok provider returning `pong` with no sidecar.

## Still to do

- Grok login is currently the grok CLI's own file; a native login flow could be
  added but is unnecessary while the grok CLI is present.
- Kimi K3 next (Anthropic-native endpoint, separate package).
