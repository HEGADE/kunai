import { listSessions } from './api'
import { ChatConnection } from './chat.svelte'
import type { Meta } from './types'

// Top-level app state. The UI is a two-pane shell (sessions + conversation) on
// wide screens and a stacked flow on phones; both share this state. "New session"
// is a modal overlay in both layouts.
class AppStore {
  sessions = $state<Meta[]>([])
  chat = $state<ChatConnection | null>(null)
  activeId = $state<string | null>(null)
  showNew = $state(false)
  listError = $state('')

  private poll?: ReturnType<typeof setInterval>

  async refresh() {
    try {
      this.sessions = await listSessions()
      this.listError = ''
    } catch (e) {
      this.listError = (e as Error).message
    }
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
}

export const app = new AppStore()
