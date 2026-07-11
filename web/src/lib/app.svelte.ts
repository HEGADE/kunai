import { history as fetchHistory, listSessions, stats as fetchStats } from './api'
import { ChatConnection } from './chat.svelte'
import type { HistoryEntry, Meta, Stats } from './types'

// Top-level app state. The UI is a two-pane shell (sessions + conversation) on
// wide screens and a stacked flow on phones; both share this state. "New session"
// is a modal overlay in both layouts.
class AppStore {
  sessions = $state<Meta[]>([])
  history = $state<HistoryEntry[]>([])
  stats = $state<Stats | null>(null)
  chat = $state<ChatConnection | null>(null)
  activeId = $state<string | null>(null)
  showNew = $state(false)
  listError = $state('')

  // Distinct project directories from past sessions, for one-tap starts.
  projects = $derived.by(() => {
    const seen = new Set<string>()
    const out: { cwd: string; name: string }[] = []
    for (const m of this.sessions) seen.add(m.cwd)
    for (const h of this.history) {
      if (seen.has(h.cwd)) continue
      seen.add(h.cwd)
      out.push({ cwd: h.cwd, name: h.cwd.replace(/\/+$/, '').split('/').slice(-1)[0] || h.cwd })
      if (out.length >= 6) break
    }
    return out
  })

  private poll?: ReturnType<typeof setInterval>

  async refresh() {
    try {
      this.sessions = await listSessions()
      this.listError = ''
    } catch (e) {
      this.listError = (e as Error).message
      return
    }
    // Secondary data is best-effort and parallel.
    fetchHistory().then((h) => (this.history = h)).catch(() => {})
    fetchStats().then((s) => (this.stats = s)).catch(() => {})
  }

  startPolling() {
    this.refresh()
    clearInterval(this.poll)
    this.poll = setInterval(() => this.refresh(), 4000)
  }

  open(id: string) {
    if (this.activeId === id) {
      this.showNew = false
      return
    }
    this.chat?.destroy()
    this.chat = new ChatConnection(id)
    this.activeId = id
    this.showNew = false
  }

  back() {
    this.chat?.destroy()
    this.chat = null
    this.activeId = null
    this.refresh()
  }

  newSession() {
    this.showNew = true
  }
  closeNew() {
    this.showNew = false
  }

  async closeSessionActive() {
    const id = this.activeId
    if (!id) return
    const { closeSession } = await import('./api')
    await closeSession(id)
    this.back()
  }

  // quickStart opens a fresh session in a known project directory. Session
  // creation is async server-side, so this is effectively instant.
  async quickStart(cwd: string) {
    const { createSession } = await import('./api')
    try {
      const meta = await createSession({ cwd })
      this.open(meta.id)
      this.refresh()
    } catch (e) {
      this.listError = (e as Error).message
    }
  }
}

export const app = new AppStore()
