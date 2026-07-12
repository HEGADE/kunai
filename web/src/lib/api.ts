import type { Attachment, HistoryEntry, Listing, MachineInfo, Meta, Stats } from './types'

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
  body: { cwd: string; title?: string; model?: string; effort?: string; resume?: string },
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

// setEffort relaunches a session at a new reasoning effort (server closes and
// resumes it; the id is unchanged). Returns the restarted session's Meta.
export function setEffort(base: string, id: string, effort: string): Promise<Meta> {
  return fetch(at(base, `/api/sessions/${id}/effort`), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ effort }),
  }).then((r) => json<Meta>(r))
}

export function browse(base: string, path: string): Promise<Listing> {
  const q = path ? `?path=${encodeURIComponent(path)}` : ''
  return fetch(at(base, `/api/browse${q}`)).then((r) => json<Listing>(r))
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

// updateMachine tells a machine to self-update: it downloads the latest release
// binary, verifies it, swaps it in, and restarts. The server exits mid-response
// as it restarts, so a dropped connection here is expected, not a failure.
export function updateMachine(base: string): Promise<void> {
  return fetch(at(base, '/api/update'), { method: 'POST' }).then((r) => json<unknown>(r)).then(() => undefined)
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
