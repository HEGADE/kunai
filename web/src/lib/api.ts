import type { Attachment, Listing, Meta } from './types'

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

export function listSessions(): Promise<Meta[]> {
  return fetch('/api/sessions').then((r) => json<Meta[]>(r))
}

export function createSession(body: {
  cwd: string
  title?: string
  model?: string
  resume?: string
}): Promise<Meta> {
  return fetch('/api/sessions', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  }).then((r) => json<Meta>(r))
}

export function closeSession(id: string): Promise<void> {
  return fetch(`/api/sessions/${id}`, { method: 'DELETE' }).then(() => undefined)
}

export function browse(path: string): Promise<Listing> {
  const q = path ? `?path=${encodeURIComponent(path)}` : ''
  return fetch(`/api/browse${q}`).then((r) => json<Listing>(r))
}

export function uploadFile(file: File): Promise<Attachment> {
  const form = new FormData()
  form.append('file', file)
  return fetch('/api/upload', { method: 'POST', body: form }).then((r) => json<Attachment>(r))
}
