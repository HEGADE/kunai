# Can we drop the 40 MB CLIProxyAPI binary and vendor the code into kunai?

Spike on branch `explore/embed-cliproxy`. Not pushed, not merged. The branch diff
is empty on purpose: the go.mod experiment was reverted after measuring. This file
is the record; everything below is reproducible.

## Short answer

We **can** run CLIProxyAPI in-process (its `sdk/cliproxy` is built to be embedded,
MIT-licensed, and it works). But it **does not save the 40 MB** and it is **not
smaller** — the 40 MB *is* the provider/translator matrix, and any path that
actually proxies a Codex/Grok/Kimi request pulls essentially all of it. Embedding
*relocates* the 40 MB from a download-on-demand sidecar into kunai's always-shipped
binary, and makes every user (even provider-less ones, every platform, every
release) carry it. Recommendation: **keep the managed sidecar as-is.**

## The numbers (measured on this Linux box, stripped `-s -w`, like a release)

| Build                                   | Size      |
| --------------------------------------- | --------- |
| kunai today (stripped)                  | **9.3 MB** |
| minimal SDK embedder alone              | 37.4 MB   |
| kunai **with** `sdk/cliproxy` linked in | **37.7 MB** |

So embedding is **+28 MB, a 4x bigger kunai binary**, shipped to every platform in
every nightly/stable release, whether or not the user ever configures a provider.
Today those 40 MB are downloaded once, on demand, only when a provider is added.

Dependency blast radius of importing `sdk/cliproxy`: **94 CLIProxyAPI packages**
compiled in, **454 total packages** in the closure (kunai today: 216).

## Why "just vendor the Codex translator" doesn't work

- The Codex translator (`internal/translator/codex`, ~10k LOC) is **format
  conversion only** — Anthropic JSON <-> Codex JSON. It cannot talk to Codex.
- The thing that actually makes the request (OAuth token refresh, SSE streaming,
  cooldown, retries, websockets) is the **executor** (`internal/runtime/executor`,
  ~26k LOC), which transitively pulls **48 CLIProxyAPI internal packages + 132
  third-party modules**. Plus OAuth (`internal/auth/{codex,xai,kimi}`, ~2.6k LOC),
  the token store, the config layer, and the interface glue.
- Vendoring that = forking ~half of a 162k-LOC, fast-moving project (HEAD commit
  the day of this spike was "add support for Codex Alpha Search model routing").
  We'd own every upstream wire-format fix by hand. No size win over embedding.

## What actually works (proven, not asserted)

- `github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy` is importable from kunai
  (a foreign module). It legally uses the project's own `internal/*` packages; Go's
  internal rule only blocks *direct* foreign imports, not a public SDK package that
  wraps them.
- `sdk/config.Config` is a public alias of the internal config type, so kunai can
  construct a config without touching internals.
- Embed API (what the binary's own `StartServiceBackground` does):
  ```go
  cfg := &config.Config{Port: p, AuthDir: authDir}
  cfg.APIKeys = []string{key}
  svc, _ := cliproxy.NewBuilder().WithConfig(cfg).WithConfigPath(cfgPath).Build()
  go svc.Run(ctx)   // returns cancel via ctx; same HTTP server the sidecar runs
  ```
  `Build()` requires **both** `WithConfig` and `WithConfigPath` (the path feeds its
  config file-watcher). Confirmed: an in-process instance started and served
  `GET /v1/models -> 200 {"data":[],"object":"list"}` with no subprocess. Populate
  the auth-dir and it loads credentials exactly like the sidecar (same code).

## The blocker even if we wanted to embed: login is internal-only

The OAuth login orchestration (`DoCodexLogin`/`DoXAILogin`/`DoKimiLogin`) lives in
`internal/cmd` — **not reachable** from outside the module. kunai's provider login
today shells the sidecar binary's `-codex-login`/`-xai-login`/`-kimi-login` and
scrapes the URL. If we embedded the proxy and deleted the binary, we'd lose the
login path and have to reimplement each provider's OAuth flow by hand. So a *pure*
embed can't even drop the binary — it'd stay just for login, and we'd ship 37.7 MB
*and* still download it.

## The one honest counter-argument (not about size)

Embedding would remove real operational fragility we hit repeatedly: the async port
assignment, the macOS codesign/dequarantine dance, subprocess supervision, and the
send-on-closed-channel respawn class of bugs. If the pain is *robustness*, not size,
embedding is a legitimate trade — but it costs +28 MB for everyone and pulls a huge
fast-moving dependency into kunai's build and CVE surface. Not worth it today; the
sidecar is contained, verified by sha256, and only present when a provider is used.

## Reproduce

```sh
git clone --depth 1 https://github.com/router-for-me/CLIProxyAPI   # v7.2.95
# minimal embedder -> 37.4 MB stripped; serves /v1/models in-process
# add sdk/cliproxy to kunai go.mod (replace -> local clone), link it, build:
go build -tags embedprobe -ldflags='-s -w' -o kunai-withsdk ./cmd/kunai  # 37.7 MB
```
