import type { AccountInfo, Attachment, CLIProfile, HistoryEntry, Job, Listing, MachineInfo, Meta, OlderTurns, Stats, ThermalConfig, Usage } from './types'

// Every call takes a `base` origin so the client can reach any machine directly
// over the tailnet. base === '' means the current origin (the hub), so the hub's
// own requests stay root-relative. Push (push.ts) is intentionally NOT here — it
// always targets the hub origin.

async function json<T>(res: Response): Promise<T> {
  if (!res.ok) {
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
  body: { cwd: string; title?: string; model?: string; effort?: string; resume?: string; cli?: string },
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

// updateSessionMeta renames and/or pins a session by id. Both fields are
// optional; the server leaves an omitted one unchanged. The id is shared by a
// live session and its resumable transcript, so this works in either list.
export function updateSessionMeta(
  base: string,
  id: string,
  patch: { name?: string; pinned?: boolean },
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

// usage reads the machine's default Claude account's quota windows (5-hour and
// weekly). The server caches it, so calling this per dashboard paint is fine.
export function usage(base: string): Promise<Usage> {
  return fetch(at(base, '/api/usage')).then((r) => json<Usage>(r))
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

// updateMachine tells a machine to self-update: it downloads the latest release
// binary, verifies it, swaps it in, and restarts. The server exits mid-response
// as it restarts, so a dropped connection here is expected, not a failure.
export function updateMachine(base: string): Promise<void> {
  return fetch(at(base, '/api/update'), { method: 'POST' }).then((r) => json<unknown>(r)).then(() => undefined)
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
