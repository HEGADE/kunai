import type { UsageReport } from './usage'
import type { AccountInfo, ChannelInfo, Attachment, CLIProfile, FunnelState, HistoryEntry, Job, Listing, MachineInfo, Meta, OlderTurns, PermissionMode, Provider, Share, ShareTier, Stats, ThermalConfig, Usage } from './types'

// Every call takes a `base` origin so the client can reach any machine directly
// over the tailnet. base === '' means the current origin (the hub), so the hub's
// own requests stay root-relative. Push (push.ts) is intentionally NOT here — it
// always targets the hub origin.

// Somebody to tell when the server says we are not signed in.
//
// Registered by the PIN store rather than imported from it, so this file stays
// free of Svelte and there is no cycle between the transport and the UI state.
let onUnauthorized: () => void = () => {}
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn
}

async function json<T>(res: Response): Promise<T> {
  if (!res.ok) {
    // A 401 only ever comes from the network listener's gate: nothing else in
    // kunai authenticates. Reacting to the status rather than asking up front is
    // what keeps the PIN screen away from loopback and the tailnet, where no gate
    // exists and asking would have shown a lock that is not there.
    if (res.status === 401) onUnauthorized()
    let msg = `HTTP ${res.status}`
    try {
      const body = await res.json()
      if (body?.error) msg = body.error
    } catch {
      /* ignore */
    }
    throw new Error(msg)
  }
  return res.json() as Promise<T>
}

const at = (base: string, path: string) => `${base}${path}`

export function listSessions(base: string): Promise<Meta[]> {
  return fetch(at(base, '/api/sessions')).then((r) => json<Meta[]>(r))
}

export function createSession(
  base: string,
  body: {
    cwd: string
    title?: string
    model?: string
    effort?: string
    resume?: string
    cli?: string
    // worktree is the path of a worktree created by POST /api/worktrees. The
    // server makes it the session's cwd, which is the whole isolation mechanism.
    worktree?: string
    // mode is the permission mode to spawn in. It has to be chosen here rather
    // than set on the running session: the CLI takes it as a spawn flag, so sent
    // afterwards it misses the first tool call.
    mode?: PermissionMode
    // provider_model is the real upstream model, when cli names a provider
    // rather than a Claude account. Distinct from model, which is a Claude tier:
    // this one is baked into the spawn env, so it has to be known before the
    // process starts.
    provider_model?: string
  },
): Promise<Meta> {
  return fetch(at(base, '/api/sessions'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  }).then((r) => json<Meta>(r))
}

export function closeSession(base: string, id: string): Promise<void> {
  return fetch(at(base, `/api/sessions/${id}`), { method: 'DELETE' }).then(() => undefined)
}

// updateSessionMeta renames, pins, and/or sets the workspace of a session by id.
// Every field is optional; the server leaves an omitted one unchanged. The id is
// shared by a live session and its resumable transcript, so this works in either
// list, and a workspace named now still groups the session once it is history.
export function updateSessionMeta(
  base: string,
  id: string,
  patch: { name?: string; pinned?: boolean; workspace?: string; snoozed_until?: number },
): Promise<void> {
  return fetch(at(base, `/api/sessions/${id}`), {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  }).then((r) => json<unknown>(r)).then(() => undefined)
}

// deleteHistory permanently removes a past session: its transcript on disk and
// any pin/rename. The server refuses (409) a session that is currently live.
export function deleteHistory(base: string, id: string): Promise<void> {
  return fetch(at(base, `/api/history/${id}`), { method: 'DELETE' }).then((r) => {
    if (!r.ok) throw new Error(r.status === 409 ? 'Close the session before deleting it.' : `HTTP ${r.status}`)
  })
}

// setEffort relaunches a session at a new reasoning effort (server closes and
// resumes it; the id is unchanged). Returns the restarted session's Meta.
export function setEffort(base: string, id: string, effort: string): Promise<Meta> {
  return fetch(at(base, `/api/sessions/${id}/effort`), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ effort }),
  }).then((r) => json<Meta>(r))
}

// A pre-turn working-tree snapshot the session can revert to. seq is the turn's
// user-message Seq (so a turn maps to its checkpoint).
export interface Checkpoint {
  seq: number
  ref: string
  captured_at: number
}

// listCheckpoints returns the turns that have a restorable pre-turn snapshot.
export function listCheckpoints(base: string, id: string): Promise<Checkpoint[]> {
  return fetch(at(base, `/api/sessions/${id}/checkpoints`)).then((r) => json<Checkpoint[]>(r))
}

// A revert returns the safety ref it captured first, so the revert can be undone by
// POSTing that ref back.
export interface RevertResult {
  reverted_to: string
  safety_ref: string
}

// One path a revert would alter. status is git's letter: M modified, A added
// since the snapshot (so the revert deletes it), D deleted since (restored).
export interface RevertChange {
  status: string
  path: string
}

// RevertPreview is exactly what a revert would do, asked of git rather than
// inferred from the turn's tool calls. It matters that this comes from the server:
// a revert resets the whole repository, so it also discards later turns' edits,
// anything changed in an editor since, and untracked files. A list built from the
// turn's own edits would be short, reassuring and wrong.
export interface RevertPreview {
  changed: RevertChange[]
  removed: string[]
}

export function revertPreview(base: string, id: string, seq: number): Promise<RevertPreview> {
  return fetch(at(base, `/api/sessions/${id}/revert?seq=${seq}`)).then((r) => json<RevertPreview>(r))
}

// revertTurn restores the working tree to a turn's pre-turn snapshot (undo the
// turn's file changes). The conversation is untouched.
export function revertTurn(base: string, id: string, seq: number): Promise<RevertResult> {
  return fetch(at(base, `/api/sessions/${id}/revert`), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ seq }),
  }).then((r) => json<RevertResult>(r))
}

// undoRevert restores the working tree to a safety ref captured by a prior revert.
export function undoRevert(base: string, id: string, ref: string): Promise<RevertResult> {
  return fetch(at(base, `/api/sessions/${id}/revert`), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ref }),
  }).then((r) => json<RevertResult>(r))
}

// setAccount switches a live session to a different Claude account, keeping its
// conversation (the server copies the transcript to the new account and resumes).
export function setAccount(base: string, id: string, name: string): Promise<Meta> {
  return fetch(at(base, `/api/sessions/${id}/account`), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  }).then((r) => json<Meta>(r))
}

export function browse(base: string, path: string): Promise<Listing> {
  const q = path ? `?path=${encodeURIComponent(path)}` : ''
  return fetch(at(base, `/api/browse${q}`)).then((r) => json<Listing>(r))
}

// --- Claude accounts (per machine) ---

export function fetchAccounts(base: string): Promise<AccountInfo[]> {
  return fetch(at(base, '/api/accounts')).then((r) => json<AccountInfo[]>(r))
}

// startAccountLogin spawns `claude auth login` for a new account and returns the
// OAuth URL to open plus a flow id to finish with.
export function startAccountLogin(base: string, name: string): Promise<{ login_id: string; url: string }> {
  return fetch(at(base, '/api/accounts/login/start'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  }).then((r) => json(r))
}

// finishAccountLogin submits the pasted code, completing and saving the account.
export function finishAccountLogin(base: string, loginId: string, code: string): Promise<{ name: string }> {
  return fetch(at(base, '/api/accounts/login/finish'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ login_id: loginId, code }),
  }).then((r) => json(r))
}

// accountLoginStatus reports whether a login finished on its own — the browser
// hit the CLI's localhost callback directly, so no code needs pasting. The
// Accounts view polls this so the local-browser case completes hands-free.
export function accountLoginStatus(
  base: string,
  loginId: string,
): Promise<{ done: boolean; name: string }> {
  return fetch(at(base, '/api/accounts/login/status'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ login_id: loginId }),
  }).then((r) => json(r))
}

export function cancelAccountLogin(base: string, loginId: string): Promise<void> {
  return fetch(at(base, '/api/accounts/login/cancel'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ login_id: loginId }),
  }).then(() => undefined)
}

export function removeAccount(base: string, name: string): Promise<void> {
  return fetch(at(base, `/api/accounts/${encodeURIComponent(name)}`), { method: 'DELETE' }).then(() => undefined)
}

// fetchOlderTurns pages in the transcript turns just older than `before` (a byte
// offset), for reverse infinite scroll. Returns them as app events plus the next
// older cursor (0 = start of transcript reached).
export function fetchOlderTurns(base: string, id: string, before: number): Promise<OlderTurns> {
  return fetch(at(base, `/api/sessions/${id}/history?before=${before}`)).then((r) => json<OlderTurns>(r))
}

export function uploadFile(base: string, file: File): Promise<Attachment> {
  const form = new FormData()
  form.append('file', file)
  return fetch(at(base, '/api/upload'), { method: 'POST', body: form }).then((r) =>
    json<Attachment>(r),
  )
}

export function history(base: string, limit?: number): Promise<HistoryEntry[]> {
  const q = limit ? `?limit=${limit}` : ''
  return fetch(at(base, `/api/history${q}`)).then((r) => json<HistoryEntry[]>(r))
}

export function stats(base: string): Promise<Stats> {
  return fetch(at(base, '/api/stats')).then((r) => json<Stats>(r))
}

// usage reads one Claude account's quota windows (5-hour and weekly). An empty
// cli means the machine's default account. Quota is per account, so a machine
// running several logins is asked once per account. The server caches each for a
// minute, so calling this per dashboard paint is fine.
export function usage(base: string, cli = ''): Promise<Usage> {
  const q = cli ? `?cli=${encodeURIComponent(cli)}` : ''
  return fetch(at(base, `/api/usage${q}`)).then((r) => json<Usage>(r))
}

// setKeepAwake toggles a machine's opt-in keep-awake (prevents idle sleep so a
// locked/idle machine stays reachable). Returns the machine's resolved state.
export function setKeepAwake(
  base: string,
  enabled: boolean,
): Promise<{ enabled: boolean; supported: boolean }> {
  return fetch(at(base, '/api/awake'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ enabled }),
  }).then((r) => json<{ enabled: boolean; supported: boolean }>(r))
}

// setFailover toggles a machine's opt-in account auto-failover (roll a walled
// session onto the account with the most headroom). Returns the resolved state.
// The lock on a machine's network listener. Owner-side: these are reachable from
// loopback and the tailnet, and on the network listener itself only once signed
// in, so a stranger can never read the state or change the PIN.
export interface LanPinState {
  set: boolean
  enabled: boolean
  urls: string[]
  min_len: number
  max_len: number
  // Present when a firewall on that machine defaults to dropping incoming
  // connections, which makes a bound listener unreachable while looking fine
  // from the machine itself.
  firewall?: { tool: string; command: string } | null
}
export interface LanDevice {
  label?: string
  created: number
  seen: number
}

export function getLanPin(base: string): Promise<LanPinState> {
  return fetch(at(base, '/api/lan/pin')).then((r) => json<LanPinState>(r))
}

export function setLanPin(base: string, pin: string): Promise<{ set: boolean }> {
  return fetch(at(base, '/api/lan/pin'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ pin }),
  }).then((r) => json<{ set: boolean }>(r))
}

export function clearLanPin(base: string): Promise<{ set: boolean }> {
  return fetch(at(base, '/api/lan/pin'), { method: 'DELETE' }).then((r) => json<{ set: boolean }>(r))
}

export function getLanDevices(base: string): Promise<LanDevice[]> {
  return fetch(at(base, '/api/lan/devices')).then((r) => json<LanDevice[]>(r))
}

export function forgetLanDevices(base: string): Promise<unknown> {
  return fetch(at(base, '/api/lan/devices'), { method: 'DELETE' }).then((r) => json<unknown>(r))
}

export function setFailover(base: string, enabled: boolean): Promise<{ enabled: boolean }> {
  return fetch(at(base, '/api/failover'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ enabled }),
  }).then((r) => json<{ enabled: boolean }>(r))
}

// setThermal updates a machine's thermal-guard policy. Returns the resolved
// config (the server clamps the thresholds).
export function setThermal(base: string, cfg: ThermalConfig): Promise<ThermalConfig> {
  return fetch(at(base, '/api/thermal'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(cfg),
  }).then((r) => json<ThermalConfig>(r))
}

// setLid toggles a machine's lid-closed hold (privileged; keeps working with the
// lid shut). Returns the resolved state.
export function setLid(base: string, enabled: boolean): Promise<{ enabled: boolean; supported: boolean }> {
  return fetch(at(base, '/api/lid'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ enabled }),
  }).then((r) => json<{ enabled: boolean; supported: boolean }>(r))
}

// getCLIs / setCLIs read and replace a machine's Claude accounts (applied live,
// no restart). The list is machine-local.
export function getCLIs(base: string): Promise<CLIProfile[]> {
  return fetch(at(base, '/api/clis')).then((r) => json<CLIProfile[]>(r))
}
export function setCLIs(base: string, clis: CLIProfile[]): Promise<CLIProfile[]> {
  return fetch(at(base, '/api/clis'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(clis),
  }).then((r) => json<CLIProfile[]>(r))
}

// getProviders / saveProvider / removeProvider manage a machine's proxy-backed
// model sources (Codex/Grok/Kimi). Machine-local, like clis; saving one upserts
// by name and creates its config dir server-side. Which proxy serves one is the
// server's business: kunai's own in-process proxy for Codex and Grok, the
// downloaded sidecar for Kimi or for a login that is missing.
export function getProviders(base: string): Promise<Provider[]> {
  return fetch(at(base, '/api/providers')).then((r) => json<Provider[]>(r))
}
export function saveProvider(base: string, p: Provider): Promise<Provider> {
  return fetch(at(base, '/api/providers'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(p),
  }).then((r) => json<Provider>(r))
}
export function removeProvider(base: string, name: string): Promise<void> {
  return fetch(at(base, `/api/providers/${encodeURIComponent(name)}`), { method: 'DELETE' }).then(
    (r) => {
      if (!r.ok) throw new Error(`HTTP ${r.status}`)
    },
  )
}

// setProviderModel changes which upstream model a provider session runs on. It
// respawns the session (the conversation resumes) and updates the provider's
// saved model, so the composer's model chip follows on the next stats refresh.
export function setProviderModel(base: string, id: string, model: string): Promise<Meta> {
  return fetch(at(base, `/api/sessions/${id}/provider-model`), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ model }),
  }).then((r) => json<Meta>(r))
}

// The models the managed sidecar can currently serve (after a provider login),
// so the UI offers real model strings instead of asking the owner to type them.
export function getProviderModels(base: string, cli = ''): Promise<string[]> {
  const q = cli ? `?cli=${encodeURIComponent(cli)}` : ''
  return fetch(at(base, `/api/providers/models${q}`)).then((r) => json<string[]>(r))
}

// In-app provider login: authorize a Codex/Grok/Kimi account into the managed
// sidecar. start returns the OAuth URL; the owner opens it and pastes the
// callback back (or a local browser completes it, which status reports).
export function startProviderLogin(
  base: string,
  provider: string,
): Promise<{ login_id: string; url: string }> {
  return fetch(at(base, '/api/providers/login/start'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ provider }),
  }).then((r) => json<{ login_id: string; url: string }>(r))
}
export function finishProviderLogin(
  base: string,
  loginId: string,
  code: string,
): Promise<{ ok: boolean }> {
  return fetch(at(base, '/api/providers/login/finish'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ login_id: loginId, code }),
  }).then((r) => json<{ ok: boolean }>(r))
}
export function providerLoginStatus(
  base: string,
  loginId: string,
): Promise<{ done: boolean; error?: string }> {
  return fetch(at(base, '/api/providers/login/status'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ login_id: loginId }),
  }).then((r) => json<{ done: boolean; error?: string }>(r))
}
export function cancelProviderLogin(base: string, loginId: string): Promise<void> {
  return fetch(at(base, '/api/providers/login/cancel'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ login_id: loginId }),
  }).then(() => {})
}

// updateMachine tells a machine to self-update: it downloads the latest release
// binary, verifies it, swaps it in, and restarts. The response streams NDJSON:
// {done,total} lines while the download runs (total is -1 when unknown), then a
// final {status} or {error}. The server exits mid-response as it restarts, so a
// dropped connection here is expected, not a failure.
export async function updateMachine(
  base: string,
  onProgress?: (done: number, total: number) => void,
): Promise<void> {
  const res = await fetch(at(base, '/api/update'), { method: 'POST' })
  if (!res.ok || !res.body) return json<unknown>(res).then(() => undefined)
  const reader = res.body.getReader()
  const dec = new TextDecoder()
  let buf = ''
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    buf += dec.decode(value, { stream: true })
    let nl
    while ((nl = buf.indexOf('\n')) >= 0) {
      const line = buf.slice(0, nl).trim()
      buf = buf.slice(nl + 1)
      if (!line) continue
      const msg = JSON.parse(line) as { done?: number; total?: number; error?: string }
      if (msg.error) throw new Error(msg.error)
      if (msg.done !== undefined) onProgress?.(msg.done, msg.total ?? -1)
    }
  }
}

// --- scheduler (per-machine: jobs live on the machine that runs them) ---

export function listSchedule(base: string): Promise<Job[]> {
  return fetch(at(base, '/api/schedule')).then((r) => json<Job[]>(r))
}
export function createSchedule(base: string, job: Partial<Job>): Promise<Job> {
  return fetch(at(base, '/api/schedule'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(job),
  }).then((r) => json<Job>(r))
}
export function replaceSchedule(base: string, id: string, job: Job): Promise<void> {
  return fetch(at(base, `/api/schedule/${id}`), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(job),
  }).then(() => undefined)
}
export function deleteSchedule(base: string, id: string): Promise<void> {
  return fetch(at(base, `/api/schedule/${id}`), { method: 'DELETE' }).then(() => undefined)
}

// --- machine registry (always the hub, base '') ---

export function listMachines(base: string): Promise<MachineInfo[]> {
  return fetch(at(base, '/api/machines')).then((r) => json<MachineInfo[]>(r))
}

export function addMachine(base: string, label: string, url: string): Promise<MachineInfo> {
  return fetch(at(base, '/api/machines'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ label, url }),
  }).then((r) => json<MachineInfo>(r))
}

export function removeMachine(base: string, id: string): Promise<void> {
  return fetch(at(base, `/api/machines/${id}`), { method: 'DELETE' }).then(() => undefined)
}

export function discoverMachines(base: string): Promise<MachineInfo[]> {
  return fetch(at(base, '/api/machines/discover')).then((r) => json<MachineInfo[]>(r))
}

// --- channels (Telegram, and whatever comes next) ---

export function listChannels(base: string): Promise<ChannelInfo[]> {
  return fetch(at(base, '/api/channels')).then((r) => json<ChannelInfo[]>(r))
}

// saveChannel stores a channel's secret and its redaction choice. An empty
// token disconnects it, which is how a channel is turned off from the app.
export function saveChannel(
  base: string,
  id: string,
  patch: { token?: string; detail?: boolean },
): Promise<ChannelInfo> {
  return fetch(at(base, `/api/channels/${id}`), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  }).then((r) => json<ChannelInfo>(r))
}

// answerChannelRequest approves or refuses someone's pairing code.
export function answerChannelRequest(
  base: string,
  id: string,
  code: string,
  approve: boolean,
): Promise<ChannelInfo> {
  return fetch(at(base, `/api/channels/${id}/requests/${code}`), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ approve }),
  }).then((r) => json<ChannelInfo>(r))
}

export function revokeChannelPerson(base: string, id: string, person: string): Promise<ChannelInfo> {
  return fetch(at(base, `/api/channels/${id}/people/${person}`), { method: 'DELETE' }).then((r) =>
    json<ChannelInfo>(r),
  )
}

// --- Sharing -----------------------------------------------------------------

// getShare returns the link for a session, or null when it is not shared. A 404
// is the ordinary answer here, not a failure, so it is not thrown.
export async function getShare(base: string, id: string): Promise<Share | null> {
  const r = await fetch(at(base, `/api/sessions/${id}/share`))
  if (r.status === 404) return null
  return json<Share>(r)
}

export interface ShareSpec {
  session_id: string
  tier: ShareTier
  ttl_secs: number
  detail: boolean
  from_now: boolean
  mode: string
  max_turns: number
}

export function createShare(base: string, spec: ShareSpec): Promise<Share> {
  return fetch(at(base, '/api/shares'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(spec),
  }).then((r) => json<Share>(r))
}

// approveShareGuest lets one person through, by the code they are looking at.
export function approveShareGuest(base: string, token: string, code: string): Promise<Share> {
  return fetch(at(base, `/api/shares/${token}/approve/${code}`), { method: 'POST' }).then((r) =>
    json<Share>(r),
  )
}

// denyShareGuest refuses the outstanding request, or removes the guest already
// paired when `unpair` is set. Both are the owner saying no.
export function denyShareGuest(base: string, token: string, unpair = false): Promise<Share> {
  const q = unpair ? '?unpair=1' : ''
  return fetch(at(base, `/api/shares/${token}/deny${q}`), { method: 'POST' }).then((r) =>
    json<Share>(r),
  )
}

export async function revokeShare(base: string, token: string): Promise<void> {
  const r = await fetch(at(base, `/api/shares/${token}`), { method: 'DELETE' })
  if (!r.ok) throw new Error(await r.text())
}

export function funnelStatus(base: string): Promise<FunnelState> {
  return fetch(at(base, '/api/funnel')).then((r) => json<FunnelState>(r))
}

// openFunnel points a public port at the share listener. Outward-facing, so the
// caller shows the exact command first.
export function openFunnel(base: string, port: number): Promise<FunnelState> {
  return fetch(at(base, '/api/funnel'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ port }),
  }).then((r) => json<FunnelState>(r))
}

export function closeFunnel(base: string, port: number): Promise<FunnelState> {
  return fetch(at(base, `/api/funnel?port=${port}`), { method: 'DELETE' }).then((r) =>
    json<FunnelState>(r),
  )
}

// --- pull-request review -----------------------------------------------------

// PullRequest is one open pull request on a repository this machine has checked
// out. reviewed_at is set when kunai has already reviewed THIS commit, which is
// what turns the Review button into "reviewed 2h ago" rather than spending
// somebody's quota on a review that already exists.
export interface PullRequest {
  number: number
  title: string
  author: string
  base_ref: string
  head_sha: string
  draft: boolean
  from_fork: boolean
  additions: number
  deletions: number
  reviewed_at?: string
  // The review THIS machine holds, if any. Distinct from reviewed_at, which
  // comes from GitHub and only knows about reviews that were posted. Without it
  // a row knew about a review only while the tab that started it stayed open, so
  // a refresh offered "Review" again on a pull request that already had one and
  // clicking it spent a fresh quota.
  review?: PullRequestReview
}

export interface PullRequestReview {
  session_id: string
  phase?: ReviewPhase
  running: boolean
  findings: number
  posted: boolean
  failed: boolean
  // The review read a commit that is no longer the head.
  stale: boolean
}

// How bad a finding is if it is true. Mirrors internal/review/severity.go, and
// the two must be changed together.
export type Severity = 'blocker' | 'major' | 'minor'

// How sure the review is that the finding is true at all, which is a separate
// question from how much it matters. Anything below 'high' was independently
// re-examined before it could reach here.
export type Confidence = 'high' | 'medium' | 'low'

// ReviewFinding is one row of the draft. `inline` is the promise the card makes:
// whether this lands on the line itself or in the summary, and `why` explains a
// demotion in words meant for a person.
// One row of what checked a claim. Labelled rather than prose, because the
// labels are the same three questions every time and a reader comparing two
// findings can only do that when both answer in the same shape.
export interface Ground {
  key: string
  value: string
}

// Who can reach a finding, what it reaches, and what fixing it costs. Together
// they are what turns a list of true statements into an order to work in.
export interface Impact {
  who?: string
  radius?: string
  size?: string
}

export interface PatchLine {
  sign: string
  text: string
}

// The fix as a diff. Built server-side from the lines the finding is anchored
// to and the suggestion it produced, so it is never a second opinion about
// itself; only the title is the model's.
export interface Patch {
  title: string
  lines: PatchLine[]
}

export interface ReviewFinding {
  hunk?: HunkLine[]
  index: number
  // The claim in a handful of words, for the queue rail.
  short?: string
  patch?: Patch | null
  grounds?: Ground[]
  impact?: Impact | null
  file: string
  line: number
  end_line?: number
  side: string
  title: string
  body: string
  severity: Severity
  confidence: Confidence
  category?: string
  // What the finding rests on, in the reviewer's own words. Shown so a claim can
  // be overruled quickly rather than taken on trust.
  evidence?: string
  // True when an independent pass tried to refute this and failed. Its absence
  // is not a black mark: a finding can skip verification by being demonstrated
  // in the first place. But "this was checked" and "this was asserted" must be
  // distinguishable, and only this can say which.
  verified?: boolean
  suggestion?: string
  inline: boolean
  why?: string
}

// severityRank moved to lib/severity.ts, which is where the rest of the severity
// vocabulary lives. It is also what makes lib/review.ts testable under plain
// node: this module imports fetch and a value imported from here would drag the
// whole API surface into the unit suite.

// A candidate the verification pass refuted, kept with its reason. Shown so the
// filtering can be audited: three findings from a reviewer that dropped four is
// a different thing from three findings from a reviewer that only found three.
export interface DroppedFinding {
  file: string
  line: number
  title: string
  severity: Severity
  why: string
}

// How far a review has got. Mirrors internal/review/phase.go.
export type ReviewPhase = 'survey' | 'find' | 'verify' | 'done'

export interface ReviewDraft {
  owner: string
  repo: string
  number: number
  title: string
  head_sha: string
  base_ref?: string
  from_fork: boolean
  requester?: string
  posted_url?: string
  parse_error?: string
  phase?: ReviewPhase
  // Whether this review has a survey step. A small change skips it, and the
  // progress display must not draw a step that will never light.
  surveyed?: boolean
  // What the reviewer decided to look at, before it looked. The only account of
  // where it thought the risk was, and the thing to read during the minutes the
  // find phase takes.
  survey?: ReviewSurvey
  // The change under review.
  files?: ReviewFile[]
  // When each phase began, so a wait has a shape rather than just a length.
  timeline?: { phase: ReviewPhase; at: string }[]
  // What the survey said to check that produced nothing. The other half of a
  // review: a reviewer that only ever lists problems is one you cannot tell
  // from a reviewer that stopped looking.
  clean?: string[]
  summary?: string
  findings?: ReviewFinding[]
  dropped?: DroppedFinding[]
  total?: number
  inline?: number
  summary_count?: number
}

// What the reviewer decided to look at, before it looked.
export interface ReviewSurvey {
  intent?: string
  areas?: { what: string; files?: string[]; why?: string }[]
}

// One changed file, as the running screen lists the change.
export interface ReviewFile {
  path: string
  status?: string
  additions?: number
  deletions?: number
}

// What each phase is called on screen, and what it is actually doing. A phased
// review takes longer than the single-shot one did, so a bare "Reviewing 4m"
// reads as a hang; naming the phase is what makes the wait legible.
export const PHASE_LABEL: Record<ReviewPhase, string> = {
  survey: 'Reading the change',
  find: 'Looking for problems',
  verify: 'Checking what it found',
  done: 'Done',
}

// What GitHub said when kunai asked whether the App actually works. Not a
// boolean: "the key is wrong", "the App is installed nowhere" and "GitHub did
// not answer" need three different sentences and lead to three different
// actions, and collapsing them is what made a broken setup report Configured.
export interface GitHubCheck {
  name?: string
  install_url?: string
  orgs?: string[]
  // The App covers only selected repositories somewhere, which is the setting
  // behind the confusing failure: everything reports configured and one
  // repository still refuses because it was never ticked.
  partial?: boolean
  warning?: string
}

export interface GitHubAppState {
  configured: boolean
  app_id?: string
  check?: GitHubCheck
  // Credentials that were fine when saved and are not any more: a revoked key,
  // a deleted App.
  error?: string
}

// `check` costs two round trips to github.com, so it is asked for only where
// somebody is looking at the setup, never by the dashboard's poll.
export function githubApp(base: string, check = false): Promise<GitHubAppState> {
  return fetch(at(base, `/api/github/app${check ? '?check=1' : ''}`)).then((r) =>
    json<GitHubAppState>(r),
  )
}

export function setGitHubApp(
  base: string,
  patch: { app_id?: string; private_key?: string; clear?: boolean },
): Promise<GitHubAppState> {
  return fetch(at(base, '/api/github/app'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  }).then((r) => json<GitHubAppState>(r))
}

export function listPullRequests(base: string, repo: string): Promise<PullRequest[]> {
  return fetch(at(base, `/api/github/pulls?repo=${encodeURIComponent(repo)}`)).then((r) =>
    json<PullRequest[]>(r),
  )
}

// startReview creates the review session. The caller opens it: from that moment
// it is an ordinary session you can watch and interrupt.
export function startReview(
  base: string,
  repo: string,
  number: number,
  requester: string,
): Promise<Meta> {
  return fetch(at(base, '/api/github/review'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ repo, number, requester }),
  }).then((r) => json<Meta>(r))
}

export function reviewDraft(base: string, sessionId: string): Promise<ReviewDraft> {
  return fetch(at(base, `/api/sessions/${sessionId}/review`)).then((r) => json<ReviewDraft>(r))
}

// One finding as the user rewrote it. Only the words: the file and line decide
// which line of somebody's pull request a comment lands on, and the server keeps
// those to itself rather than accepting them back from here.
export interface ReviewEdit {
  index: number
  title?: string
  body?: string
  severity?: Severity
}

// postReview sends the draft. `keep` is the findings the user did not drop: a
// nil list means all of them, an EMPTY list means they read everything and
// dropped it all, and the server tells those two apart.
export function postReview(
  base: string,
  sessionId: string,
  keep: number[],
  edits: ReviewEdit[] = [],
  summary = '',
): Promise<{ url: string }> {
  return fetch(at(base, `/api/sessions/${sessionId}/review/post`), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ keep, edits, summary }),
  }).then((r) => json<{ url: string }>(r))
}

// What applying a suggested change did, so the report can name the file and the
// size of the edit rather than saying "done".
export interface AppliedFix {
  file: string
  path: string
  line: number
  removed: number
  added: number
}

// applyReviewFix writes one finding's suggestion into the checkout the review
// read. The file is echoed back as a check on the index, because an index is a
// fragile way to name a finding and the failure it guards against writes to the
// wrong file in silence.
export function applyReviewFix(
  base: string,
  sessionId: string,
  index: number,
  file: string,
): Promise<AppliedFix> {
  return fetch(at(base, `/api/sessions/${sessionId}/review/apply`), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ index, file }),
  }).then((r) => json<AppliedFix>(r))
}

// reopenReview brings a finished review's session back so it can be asked
// something. Idempotent: a review that is still live answers with its own id and
// changes nothing, so a caller never has to work out which it is.
//
// It exists because the ordinary reopen cannot do this. A review runs in a
// throwaway checkout that is swept when it ends, so resuming it needs the
// commit that was read and the repository it came from, and only the review
// record has those.
export function reopenReview(base: string, sessionId: string): Promise<{ id: string }> {
  return fetch(at(base, `/api/sessions/${sessionId}/review/reopen`), { method: 'POST' }).then((r) =>
    json<{ id: string }>(r),
  )
}

// ReviewConfig is which account and model reviews run on. Its own setting
// because a review is chunky and unattended: pointed at a second account or a
// provider, it can never wall the session you are working in. Empty means the
// machine's default.
export interface ReviewConfig {
  cli?: string
  model?: string
}

export function reviewConfig(base: string): Promise<ReviewConfig> {
  return fetch(at(base, '/api/github/review-config')).then((r) => json<ReviewConfig>(r))
}

export function setReviewConfig(base: string, cfg: ReviewConfig): Promise<ReviewConfig> {
  return fetch(at(base, '/api/github/review-config'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(cfg),
  }).then((r) => json<ReviewConfig>(r))
}

// One line of diff evidence carried with a finding, so a card can show the code
// it is about without a second round trip.
export interface HunkLine {
  kind: string // " " | "+" | "-"
  old?: number
  new?: number
  text: string
  focus?: boolean
}

// Servers the agent started in a session, and forwarding one so another device
// can reach it. See internal/preview.
export interface PreviewServer {
  port: number
  command: string
  pid: number
  local: boolean
  url?: string
  forwarding: boolean
}

export function listPreviews(base: string, id: string): Promise<PreviewServer[]> {
  return fetch(at(base, `/api/sessions/${id}/previews`)).then((r) => json<PreviewServer[]>(r))
}

export function openPreview(base: string, id: string, port: number): Promise<PreviewServer> {
  return fetch(at(base, `/api/sessions/${id}/previews/${port}`), { method: 'POST' })
    .then((r) => json<PreviewServer>(r))
}

export function closePreview(base: string, id: string, port: number): Promise<unknown> {
  return fetch(at(base, `/api/sessions/${id}/previews/${port}`), { method: 'DELETE' })
    .then((r) => json<unknown>(r))
}

// Spend, priced from the transcripts. The whole history arrives in one payload
// and the client windows it (see lib/usage.ts), so this is fetched once per
// machine rather than per period change. `scanning` true means the first pass
// over the transcripts is still running and the numbers will grow.
export function fetchUsageStats(base: string): Promise<UsageReport> {
  return fetch(at(base, '/api/usage/stats')).then((r) => json<UsageReport>(r))
}
