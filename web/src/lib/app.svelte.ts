import { listSessions } from './api'
import { ChatConnection } from './chat.svelte'
import type { Meta } from './types'

// Top-level app state: which screen is showing and the live session list.
class AppStore {
  view = $state<'list' | 'new' | 'chat'>('list')
  sessions = $state<Meta[]>([])
  chat = $state<ChatConnection | null>(null)
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
    this.poll = setInterval(() => {
      if (this.view !== 'chat') this.refresh()
    }, 4000)
  }

  open(id: string) {
    this.chat?.destroy()
    this.chat = new ChatConnection(id)
    this.view = 'chat'
  }

  back() {
    this.chat?.destroy()
    this.chat = null
    this.view = 'list'
    this.refresh()
  }

  newSession() {
    this.view = 'new'
  }
}

export const app = new AppStore()
