import {
  addMachine as apiAddMachine,
  closeSession,
  createSession,
  deleteHistory,
  discoverMachines,
  history as fetchHistory,
  listMachines,
  listSessions,
  removeMachine as apiRemoveMachine,
  setEffort as apiSetEffort,
  setAccount as apiSetAccount,
  setProviderModel as apiSetProviderModel,
  stats as fetchStats,
  updateMachine as apiUpdateMachine,
  updateSessionMeta,
  listSchedule,
  createSchedule as apiCreateSchedule,
  replaceSchedule as apiReplaceSchedule,
  deleteSchedule as apiDeleteSchedule,
} from './api'
import { ChatConnection } from './chat.svelte'
import { DEFAULT_MODEL, DEFAULT_EFFORT } from './models'
import { learnModel, setDiscovered } from './modelVersions.svelte'
import {
  fetchLatestVersion,
  readCachedVersion,
  writeCachedVersion,
  VERSION_FLOOR,
  VERSION_TTL,
} from './update'
import { startWorktree, type WorktreeChoice } from './worktrees'
import { FleetSocket } from './fleet'
import type { Job, Machine, Meta, PermissionMode, Stats, TaggedHistoryEntry, TaggedJob, TaggedMeta } from './types'

// Top-level app state. One installed client can drive Claude sessions across
// several machines on the tailnet: the machine that served the PWA is the "hub"
// (its origin owns the registry + push), and the client talks DIRECTLY to every
// machine's origin for REST + WS. Sessions are tagged client-side with the
// machine they came from; wire types stay pure.
// Tab is one open session in the tab strip.
export interface Tab {
  machineId: string
  id: string
}
export const tabKey = (machineId: string, id: string) => `${machineId}:${id}`

// switchFailure turns a respawn failure into something worth reading.
//
// The common one is not a bug and not the account: kunai restarted (a self
// update does exactly this), which ends every ordinary session, and the tab you
// are looking at has been pointing at a session that no longer exists ever
// since. The server answers "session not found", which is true and tells you
// nothing about what to do, so the one case anybody actually hits gets said
// properly.
function switchFailure(account: string, e: Error): string {
  const msg = e.message || 'that did not work'
  if (/not found|no such session/i.test(msg)) {
    return 'This session has ended, so there is nothing to switch. Reopen it from Recent to carry on.'
  }
  if (/signed out/i.test(msg) && account) {
    return msg // the server already names the account and what to do
  }
  return msg
}

// StartSpec is everything about a new session other than where it runs and what
// it is asked to do. Every field is optional and an omitted one takes the app
// default, so a one-tap start passes nothing at all. It exists as one shape so
// the launcher, the new-session dialog and the sidebar cannot drift into
// offering different subsets of the same choice.
export interface StartSpec {
  // cli names the Claude account or the provider; the two share one list because
  // from here they are the same decision: which brain runs this.
  cli?: string
  model?: string
  // effort is spawn-time only, which is why it belongs to starting rather than to
  // the session.
  effort?: string
  // mode is the permission mode, also spawn-time: set afterwards it arrives too
  // late to govern the first tool call.
  mode?: PermissionMode
  // providerModel is the upstream model when cli names a provider. A Claude tier
  // (model, above) means nothing there: the provider serves one real model id,
  // baked into the spawn env.
  providerModel?: string
  wt?: WorktreeChoice
}

// How many recently-closed tabs to keep warm (connection alive, history already
// parsed) so reopening one is instant. Small, because each holds a live socket.
const WARM_TABS = 5

// Waiting for a machine to come back after an update: how often to look, and how
// long before saying it never did. A single check cannot answer this honestly.
// macOS runs kunai under launchd, whose default ThrottleInterval holds a respawn
// for ten seconds, so a healthy Mac is still down when a short fixed window
// expires and would be accused of never returning. Polling keeps a good restart
// fast (it clears the moment stats report the new build) and makes the failure
// verdict patient.
const RESTART_POLL_MS = 2_000
const RESTART_WAIT_MS = 45_000

class AppStore {
  machines = $state<Machine[]>([this.selfSeed()])
  sessions = $state<TaggedMeta[]>([])
  history = $state<TaggedHistoryEntry[]>([])
  stats = $state<Machine['stats']>(null) // hub/self stats, for the single-machine dashboard

  // Open session tabs. Every tab keeps its connection alive, not just the active
  // one: each session is an agent that goes on working while you look at another,
  // so the strip doubles as a live status board and switching is instant.
  // conns holds the connections outside $state (they are live objects, not data)
  // and connsVersion is what the deriveds below track.
  tabs = $state<Tab[]>([])
  activeKey = $state<string | null>(null)
  private conns = new Map<string, ChatConnection>()
  // Recently-closed tabs kept warm: the connection stays live and its history
  // stays parsed, so reopening one is instant instead of a fresh connect plus a
  // full backlog replay. Bounded LRU (insertion order is age); the oldest past
  // WARM_TABS is evicted and destroyed.
  private detached = new Map<string, ChatConnection>()
  private connsVersion = $state(0)

  chat = $derived.by(() => {
    void this.connsVersion
    return this.activeKey ? (this.conns.get(this.activeKey) ?? null) : null
  })
  activeTab = $derived(this.tabs.find((t) => tabKey(t.machineId, t.id) === this.activeKey) ?? null)
  activeId = $derived(this.activeTab?.id ?? null)
  activeMachineId = $derived(this.activeTab?.machineId ?? null)

  // connFor exposes a tab's live connection so the strip can read its state.
  connFor(t: Tab): ChatConnection | null {
    void this.connsVersion
    return this.conns.get(tabKey(t.machineId, t.id)) ?? null
  }

  // liveState is what a session's state actually is, as opposed to what the last
  // poll said it was.
  //
  // The session list is polled, deliberately slowly, because "an open session
  // reports its own state over its socket" -- so Meta.state can be a whole poll
  // behind, and anything that displays it prominently will show the wrong thing
  // for seconds at a time. That was fine while it only drove a small dot; it is
  // not fine now that a folder announces "1 needs you" from it. An open tab's
  // socket knows immediately, so prefer it and fall back to the polled value.
  liveState(m: { machineId: string; id: string; state?: string }): string {
    void this.connsVersion
    const conn = this.conns.get(tabKey(m.machineId, m.id))
    return conn?.sessionState ?? m.state ?? ''
  }

  // liveTurnStart is when the running turn began (unix ms), 0 when none is. The
  // same preference as liveState and for the same reason: an open tab's socket
  // stamps it immediately while the polled list is up to a cycle behind, and a
  // duration that starts a second late is a duration that is wrong.
  liveTurnStart(m: { machineId: string; id: string; turn_started_at?: number }): number {
    void this.connsVersion
    const conn = this.conns.get(tabKey(m.machineId, m.id))
    return conn?.turnStartedAt || m.turn_started_at || 0
  }

  // busy reports whether any session is mid-turn or waiting on an answer, by the
  // freshest reading available. It sets the polling cadence: a status board is
  // only worth having if it is roughly current, and the cost of asking more often
  // is only paid while there is something happening.
  get busy(): boolean {
    return this.sessions.some((m) => {
      const st = this.liveState(m)
      return st === 'running' || st === 'awaiting_permission'
    })
  }
  showNew = $state(false)
  showSettings = $state(false)
  showAccounts = $state(false)
  showChannels = $state(false)
  showProviders = $state(false)
  showAllSessions = $state(false)
  listError = $state('')
  // actionError is why the last thing you asked for did not happen: switching
  // account, changing effort, closing.
  //
  // Separate from listError because they are read in different places. listError
  // is about the machine list and belongs in the sidebar; this is about the
  // session in front of you and has to appear there. Routing these into
  // listError meant an account switch that the server refused reported itself in
  // a panel you cannot see from a chat, so the picker looked stuck and nothing
  // explained why. Cleared by the next action that succeeds.
  actionError = $state('')
  // Sidebar machine filter: 'all' or a machine id. Lets you focus one machine.
  machineFilter = $state('all')

  // Which release channel the build serving this app is on. Read from our own
  // machine's stats, and absent both before stats load and on stable. Lives here
  // rather than in each component so the sidebar's night-sky header and the
  // browser chrome behind the notch cannot disagree about which build this is.
  get isNightly(): boolean {
    return this.machines.find((m) => m.self)?.stats?.channel === 'nightly'
  }
  sidebarOpen = $state(localStorage.getItem('kunai-sidebar') !== '0')
  // Latest release tag from GitHub (client-side check), and per-machine
  // in-flight update flags so the dashboard can show "Updating…".
  latestVersion = $state<string | null>(null)
  // Why the last update check produced nothing, when it produced nothing. Empty
  // means the check is fine.
  versionCheckFailed = $state('')
  updating = $state<Record<string, boolean>>({})
  // Per-machine reason the last update attempt failed (server's error text).
  updateError = $state<Record<string, string>>({})
  // Per-machine download progress: 0..1 fraction, -1 while indeterminate, and
  // exactly 1 once the download is done and the machine is restarting.
  updateProgress = $state<Record<string, number>>({})
  schedules = $state<TaggedJob[]>([])

  toggleSidebar() {
    this.sidebarOpen = !this.sidebarOpen
    localStorage.setItem('kunai-sidebar', this.sidebarOpen ? '1' : '0')
  }

  // "This machine" seeded from the URL so the UI resolves before the registry
  // loads (and stays working if the hub omits itself, e.g. local dev).
  private selfSeed(): Machine {
    const slug = location.hostname.split('.')[0] || 'self'
    return { id: slug, label: slug, url: location.origin, self: true, online: true, stats: null }
  }

  baseForMachine(id: string): string {
    return this.machines.find((m) => m.id === id)?.url ?? ''
  }
  private selfId(): string {
    return this.machines.find((m) => m.self)?.id ?? this.machines[0]?.id ?? 'self'
  }

  // Distinct (machine, cwd) project dirs from sessions + history, for one-tap starts.
  projects = $derived.by(() => {
    const seen = new Set<string>()
    const key = (mid: string, cwd: string) => `${mid}\x00${cwd}`
    const out: { machineId: string; cwd: string; name: string }[] = []
    for (const m of this.sessions) seen.add(key(m.machineId, m.cwd))
    for (const h of this.history) {
      const k = key(h.machineId, h.cwd)
      if (seen.has(k)) continue
      seen.add(k)
      out.push({
        machineId: h.machineId,
        cwd: h.cwd,
        name: h.cwd.replace(/\/+$/, '').split('/').slice(-1)[0] || h.cwd,
      })
      if (out.length >= 8) break
    }
    return out
  })

  private poll?: ReturnType<typeof setInterval>
  private ticks = 0

  // --- registry ---

  async loadMachines() {
    try {
      const infos = await listMachines('')
      // An empty registry is the ordinary single-machine case, not a failure: the
      // client is already showing the seeded "self" entry. Returning here without
      // syncing left that machine with no socket, so the push never started and
      // everything quietly stayed on the poll.
      if (infos.length === 0) {
        this.syncFleetSockets()
        return
      }
      const prev = new Map(this.machines.map((m) => [m.id, m]))
      const next: Machine[] = infos.map((info) => {
        const old = prev.get(info.id)
        return { ...info, online: old?.online ?? info.self, stats: old?.stats ?? null }
      })
      // Guarantee a "self" entry even if the hub has no -public-url configured.
      if (!next.some((m) => m.self || sameOrigin(m.url, location.origin))) {
        next.unshift(this.selfSeed())
      }
      this.machines = next
      // A machine added or discovered gets a socket; one removed loses its.
      this.syncFleetSockets()
    } catch {
      /* keep whatever we have; retry next tick */
    }
  }

  async addMachine(label: string, url: string) {
    await apiAddMachine('', label, url)
    await this.loadMachines()
    this.refresh()
  }

  async removeMachine(id: string) {
    for (const t of this.tabs.filter((t) => t.machineId === id)) this.closeTab(t, { ended: true })
    for (const key of [...this.detached.keys()]) if (key.startsWith(`${id}:`)) this.dropWarm(key)
    await apiRemoveMachine('', id)
    await this.loadMachines()
    this.refresh()
  }

  async discover() {
    try {
      await discoverMachines('')
    } catch {
      /* ignore */
    }
    await this.loadMachines()
    this.refresh()
  }

  // --- data fan-out ---

  // refresh pulls the per-machine lists. `what` lets the poll fetch only what has
  // plausibly changed: the live session list moves often, while the resumable
  // history and the machine stats do not, and each one costs a request per
  // machine. Callers reacting to something the user just did pass nothing and
  // get the lot.
  async refresh(what: { history?: boolean; stats?: boolean } = { history: true, stats: true }) {
    const machines = this.machines
    const results = await Promise.allSettled(machines.map((m) => this.refreshMachine(m, what)))

    // A single blipped fetch must not blank a machine's rows: keeping the
    // last-known sessions/history for a machine that failed this tick is what
    // stops the sidebar flickering rows away and back ("dancing"). The offline
    // dot already tells you the machine is unreachable.
    const prevSessions = this.sessions
    const prevHistory = this.history
    const keptSessions = (id: string) => prevSessions.filter((s) => s.machineId === id)
    const keptHistory = (id: string) => prevHistory.filter((h) => h.machineId === id)

    const nextSessions: TaggedMeta[] = []
    const nextHistory: TaggedHistoryEntry[] = []
    const nextMachines = machines.map((m, i) => {
      const r = results[i]
      if (r.status === 'fulfilled') {
        nextSessions.push(...r.value.sessions)
        // History isn't fetched every tick; when skipped, carry the last set so it
        // never blanks between the slower history polls.
        nextHistory.push(...(r.value.history ?? keptHistory(m.id)))
        return { ...m, online: true, stats: r.value.stats ?? m.stats }
      }
      nextSessions.push(...keptSessions(m.id))
      nextHistory.push(...keptHistory(m.id))
      return { ...m, online: false }
    })

    // Model versions: the authoritative set each machine read from its claude
    // binary (so a new model shows the instant stats load), plus a fallback learned
    // from resolved session ids. Both feed the picker labels.
    for (const m of nextMachines) setDiscovered(m.stats?.model_versions)
    for (const s of nextSessions) learnModel(s.model)

    // Rune reactivity: build locally, assign once (never mutate in place).
    this.sessions = nextSessions
    if (what.history) this.history = nextHistory
    this.machines = nextMachines
    this.stats = nextMachines.find((m) => m.self)?.stats ?? this.stats
    this.listError = nextMachines.every((m) => !m.online) ? 'No machines reachable' : ''
  }

  private async refreshMachine(m: Machine, what: { history?: boolean; stats?: boolean }) {
    // Primary list is not caught here: a fully-down machine rejects and gets
    // marked offline without failing the others (Promise.allSettled). A skipped
    // fetch yields null, and the caller keeps the value it already had.
    const sessions = await listSessions(m.url)
    const [hist, st] = await Promise.all([
      // A failed history fetch yields null (not []) so the caller keeps the last
      // set rather than blanking Recent — an empty [] is a real "no history".
      what.history ? fetchHistory(m.url).catch(() => null) : Promise.resolve(null),
      what.stats ? fetchStats(m.url).catch(() => null) : Promise.resolve(null),
    ])
    return {
      sessions: sessions.map((s) => ({ ...s, machineId: m.id }) as TaggedMeta),
      history: hist ? hist.map((h) => ({ ...h, machineId: m.id }) as TaggedHistoryEntry) : null,
      stats: st,
    }
  }

  // --- pushed updates ---------------------------------------------------------

  // One socket per machine. A machine whose socket is live stops being polled for
  // its session list: its changes arrive in a round trip instead of on the next
  // beat, which is the difference between a status board that is current and one
  // that can be eight seconds behind. The poll stays as the fallback, so a machine
  // that cannot hold a socket (an old server without the route, a proxy in the
  // way) is exactly as good as it was before.
  private fleets = new Map<string, FleetSocket>()
  private pushed = $state<Record<string, boolean>>({})

  private syncFleetSockets() {
    const want = new Set(this.machines.map((m) => m.id))
    for (const [id, sock] of this.fleets) {
      if (!want.has(id)) {
        sock.stop()
        this.fleets.delete(id)
        const next = { ...this.pushed }
        delete next[id]
        this.pushed = next
      }
    }
    for (const m of this.machines) {
      if (this.fleets.has(m.id)) continue
      const sock = new FleetSocket(m.url, {
        onSessions: (list) => this.applyPushedSessions(m.id, list),
        onStats: (stats) => this.applyPushedStats(m.id, stats),
        onOpen: () => (this.pushed = { ...this.pushed, [m.id]: true }),
        onClose: () => (this.pushed = { ...this.pushed, [m.id]: false }),
      })
      this.fleets.set(m.id, sock)
      sock.start()
    }
  }

  // Replace only this machine's rows, tagging them the way refreshMachine does.
  // Wire types stay pure; the machine tag is added at the boundary.
  private applyPushedSessions(machineId: string, list: Meta[]) {
    const tagged = list.map((s) => ({ ...s, machineId }))
    for (const s of tagged) learnModel(s.model)
    this.sessions = [...this.sessions.filter((s) => s.machineId !== machineId), ...tagged]
    // A machine that just told us something is reachable, whatever the last poll
    // concluded.
    this.machines = this.machines.map((m) => (m.id === machineId ? { ...m, online: true } : m))
    this.listError = this.machines.every((m) => !m.online) ? 'No machines reachable' : ''
  }

  private applyPushedStats(machineId: string, stats: Stats) {
    setDiscovered(stats.model_versions)
    this.machines = this.machines.map((m) =>
      m.id === machineId ? { ...m, online: true, stats } : m,
    )
    if (this.machines.find((m) => m.id === machineId)?.self) this.stats = stats
  }

  startPolling() {
    this.loadMachines().then(() => {
      // Version check rides after refresh: it needs our own stats (hence channel)
      // loaded first, or it would default to the stable release on a nightly box.
      this.refresh().then(() => this.loadLatestVersion(true))
      this.drainDeepLink()
    })
    this.watchVisibility()
    clearInterval(this.poll)
    // One beat, with each thing on the cadence it actually changes at — every
    // fetch here costs a request per machine, so polling the lot every tick was
    // mostly re-asking questions whose answers had not moved.
    this.poll = setInterval(() => {
      // Nothing is on screen, so nothing needs fetching. A locked phone used to
      // keep polling every tick for a dashboard nobody was looking at; App
      // refreshes on the way back in.
      if (typeof document !== 'undefined' && document.visibilityState !== 'visible') return
      const t = ++this.ticks
      if (t % 4 === 0) this.loadMachines() // new/discovered machines (~16s)
      if (t % 4 === 0) this.loadSchedules() // next-fire times (~16s)
      if (t % 75 === 0) this.loadLatestVersion() // re-check GitHub ~every 5 min
      // The session list doubles as the per-machine liveness probe, so it sets
      // the base beat. Every tick while anything is working, every other tick
      // when nothing is: the sidebar now reports each folder's state from this
      // list, and a status board that is eight seconds stale is a status board
      // that tells you an agent is working after it stopped to ask you something.
      // Idle machines pay nothing for this, because idle is when it backs off.
      // Every machine is pushing, so the only thing left on the timer is history,
      // which no socket carries and which only moves when a session ends. When a
      // socket is down for any machine, the old cadence comes straight back for
      // everything: the push is an accelerator, never the only source.
      const allPushed = this.machines.length > 0 && this.machines.every((m) => this.pushed[m.id])
      if (allPushed) {
        if (t % 10 === 0) this.refresh({ history: true, stats: false })
        return
      }
      if (t % 2 === 0 || this.busy) {
        this.refresh({
          stats: t % 4 === 0, // gauges (~16s)
          history: t % 10 === 0, // resumable list; only moves when a session ends (~40s)
        })
      }
    }, 4000)
    this.loadSchedules()
  }

  // Re-check for a newer release whenever the app returns to the foreground, so
  // the "Update available" banner appears on your next open instead of needing a
  // hard refresh. The throttle in loadLatestVersion keeps this within GitHub's
  // unauthenticated rate limit.
  private versionWatchInstalled = false
  private watchVisibility() {
    if (this.versionWatchInstalled || typeof document === 'undefined') return
    this.versionWatchInstalled = true
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'visible') this.loadLatestVersion()
    })
  }

  // Scheduler jobs live on the machine that runs them; fan out and tag by machine.
  async loadSchedules() {
    const results = await Promise.allSettled(
      this.machines.map((m) =>
        listSchedule(m.url).then((jobs) => jobs.map((j) => ({ ...j, machineId: m.id }) as TaggedJob)),
      ),
    )
    const all: TaggedJob[] = []
    for (const r of results) if (r.status === 'fulfilled') all.push(...r.value)
    all.sort((a, b) => (a.next_fire ?? '').localeCompare(b.next_fire ?? ''))
    this.schedules = all
  }

  async createSchedule(machineId: string, job: Partial<Job>) {
    await apiCreateSchedule(this.baseForMachine(machineId), job)
    this.loadSchedules()
  }
  async toggleSchedule(job: TaggedJob) {
    await apiReplaceSchedule(this.baseForMachine(job.machineId), job.id, { ...job, enabled: !job.enabled })
    this.loadSchedules()
  }
  async removeSchedule(job: TaggedJob) {
    await apiDeleteSchedule(this.baseForMachine(job.machineId), job.id)
    this.loadSchedules()
  }

  // loadLatestVersion refreshes the newest release tag from GitHub. It is
  // throttled so foreground re-checks can't exceed GitHub's unauthenticated rate
  // limit (60/hr); `force` bypasses the throttle for the initial load.
  private lastVersionCheck = 0
  private async loadLatestVersion(force = false) {
    // Must know our own channel first, or a nightly build would be compared
    // against the stable `latest` release and offered a cross-channel "update".
    // stats.channel is absent both before stats load and on the stable channel,
    // so gate on stats being present, not on the channel value.
    const self = this.machines.find((m) => m.self)
    if (!self?.stats) return // retry next tick, once we know what we are
    const channel = self.stats.channel ?? ''

    // Reuse a recent answer, from this tab or any other. Without this the check
    // ran on every load, every five minutes, and every time a tab regained
    // focus, which spends GitHub's whole unauthenticated allowance of sixty an
    // hour: a phone and a laptop on one connection exhaust it between them, and
    // the app then shows no update banner at all, which is indistinguishable
    // from being up to date. That is how a machine sat a release behind with
    // nothing on screen to say so.
    const cached = readCachedVersion(channel)
    const now = Date.now()
    const age = cached ? now - cached.at : Infinity
    if (age < (force ? VERSION_FLOOR : VERSION_TTL)) {
      this.latestVersion = cached!.tag
      this.versionCheckFailed = ''
      return
    }
    if (now - this.lastVersionCheck < VERSION_FLOOR) return
    this.lastVersionCheck = now

    const res = await fetchLatestVersion(channel)
    if (res.tag) {
      this.latestVersion = res.tag
      this.versionCheckFailed = ''
      writeCachedVersion(channel, res.tag, now)
      return
    }
    // Keep whatever was last known; say why there is no answer rather than
    // letting silence read as "nothing new".
    this.versionCheckFailed = res.rateLimited
      ? 'GitHub is rate limiting update checks from this network; it clears within the hour'
      : 'could not reach GitHub to check for updates'
  }

  // updateMachine asks a machine to self-update (download latest release,
  // verify, swap, restart). The server exits mid-response, so a dropped
  // connection is expected. We poll a couple of times to catch the restart;
  // once its stats report the new version the "Update available" badge clears
  // on its own. The spinner is a fixed-window fallback so it never sticks.
  async updateMachine(machineId: string) {
    const before = this.machines.find((m) => m.id === machineId)?.stats?.kunai_version
    this.updating = { ...this.updating, [machineId]: true }
    this.updateError = { ...this.updateError, [machineId]: '' }
    this.updateProgress = { ...this.updateProgress, [machineId]: -1 }
    const clearFlags = () => {
      const upd = { ...this.updating }
      delete upd[machineId]
      this.updating = upd
      const prog = { ...this.updateProgress }
      delete prog[machineId]
      this.updateProgress = prog
    }
    try {
      await apiUpdateMachine(this.baseForMachine(machineId), (done, total) => {
        this.updateProgress = {
          ...this.updateProgress,
          [machineId]: total > 0 ? done / total : -1,
        }
      })
    } catch (e) {
      // A dropped connection (fetch rejects with a TypeError) is the expected
      // mid-restart disconnect. An HTTP error or a streamed {error} line means
      // the server said why it could NOT update (unwritable install dir,
      // download failed): surface that instead of pretending it is in flight.
      if (!(e instanceof TypeError)) {
        this.updateError = {
          ...this.updateError,
          [machineId]: e instanceof Error ? e.message : String(e),
        }
        clearFlags()
        return
      }
    }
    // Download done; what is left is the swap and the restart.
    this.updateProgress = { ...this.updateProgress, [machineId]: 1 }
    await this.awaitRestart(machineId, before)
    clearFlags()
  }

  // awaitRestart watches a machine come back after an update and says what
  // happened, because silence is a lie either way. It polls rather than deciding
  // once: a machine that returns quickly clears the banner as soon as its stats
  // report the new build, and only a machine that is still missing at the
  // deadline gets a verdict.
  private async awaitRestart(machineId: string, before?: string) {
    const deadline = Date.now() + RESTART_WAIT_MS
    for (;;) {
      await new Promise((r) => setTimeout(r, RESTART_POLL_MS))
      await this.refresh()
      const m = this.machines.find((x) => x.id === machineId)
      const v = m?.online ? m.stats?.kunai_version : undefined
      // Success is being on the build we were trying to reach, not merely having
      // changed. A machine already running the newest build reports the same
      // version afterwards, and calling that a failure told people to reinstall a
      // service that was working: observed on a real machine, on the exact build
      // it was supposed to be on.
      if (v && (v === this.latestVersion || v !== before)) return
      if (Date.now() < deadline) continue

      // Before saying anything went wrong, make sure what we are comparing
      // against is current. The version check is throttled to a minute, which is
      // exactly the window an update happens in, so the target can easily be a
      // build older than the one now running: judged against that, a machine
      // that updated perfectly looks like it failed.
      await this.loadLatestVersion(true)
      if (v && v === this.latestVersion) return

      // Out of patience. Whatever is wrong is on the machine, not here, and its
      // OS is known from the last stats we saw, so name the exact command.
      const restartHint =
        m?.stats?.os === 'darwin'
          ? 'on the machine, run: launchctl kickstart -k gui/$UID/com.kunai.agent · log: ~/.kunai/kunai.log'
          : m?.stats?.os === 'linux'
            ? 'on the machine, run: systemctl --user restart kunai · log: journalctl --user -u kunai'
            : "check the machine's service and log"
      // Say what was seen, not what it must mean. The old wording named one cause
      // (a service running a different binary) as though it were the finding, and
      // sent people to reinstall on the strength of it.
      this.updateError = {
        ...this.updateError,
        [machineId]: v
          ? `still on ${v} after restarting${this.latestVersion ? `, not ${this.latestVersion}` : ''}; ${restartHint}`
          : `not back after ${Math.round(RESTART_WAIT_MS / 1000)}s; ${restartHint}`,
      }
      return
    }
  }

  // --- navigation ---

  // open focuses a session, adding a tab for it the first time. An already-open
  // session is just re-focused, keeping its connection and history.
  open(machineId: string, id: string) {
    const key = tabKey(machineId, id)
    if (!this.conns.has(key)) {
      // Reopen a recently-closed tab from the warm cache: its connection is still
      // live and its history already parsed, so there is nothing to reconnect or
      // replay and the view paints at once. Otherwise open a fresh connection.
      const warm = this.detached.get(key)
      if (warm) this.detached.delete(key)
      this.conns.set(key, warm ?? new ChatConnection(this.baseForMachine(machineId), id))
      this.connsVersion++
      this.tabs = [...this.tabs, { machineId, id }]
    }
    this.activeKey = key
    this.showNew = false
    this.syncUrl()
  }

  // closeTab detaches the view only: the session keeps running on its machine and
  // stays in the sidebar. Ending a session is a separate, explicit action. The
  // connection is parked warm (still live) for an instant reopen unless the
  // session actually ended (`ended`), in which case it is torn down.
  closeTab(t: Tab, opts: { ended?: boolean } = {}) {
    const key = tabKey(t.machineId, t.id)
    const i = this.tabs.findIndex((x) => tabKey(x.machineId, x.id) === key)
    if (i < 0) return
    const conn = this.conns.get(key)
    this.conns.delete(key)
    if (conn) {
      if (opts.ended) conn.destroy()
      else this.warm(key, conn)
    }
    this.connsVersion++
    this.tabs = this.tabs.filter((_, k) => k !== i)
    if (this.activeKey !== key) return
    const next = this.tabs[i] ?? this.tabs[i - 1] ?? null
    this.activeKey = next ? tabKey(next.machineId, next.id) : null
    this.syncUrl()
    if (!next) this.refresh()
  }

  closeTabFor(machineId: string, id: string, opts: { ended?: boolean } = {}) {
    const t = this.tabs.find((x) => x.machineId === machineId && x.id === id)
    if (t) this.closeTab(t, opts)
    else if (opts.ended) this.dropWarm(tabKey(machineId, id))
  }

  // warm parks a detached connection in the LRU, re-inserting at the most-recent
  // end and evicting (destroying) the oldest past the cap.
  private warm(key: string, conn: ChatConnection) {
    this.detached.delete(key)
    this.detached.set(key, conn)
    while (this.detached.size > WARM_TABS) {
      const oldest = this.detached.keys().next().value as string
      this.dropWarm(oldest)
    }
  }

  private dropWarm(key: string) {
    this.detached.get(key)?.destroy()
    this.detached.delete(key)
  }

  // back leaves the session view without closing the tab, so the agent keeps
  // running and you can step straight back into it.
  back() {
    this.activeKey = null
    this.syncUrl()
    this.refresh()
  }

  newSession() {
    this.showSettings = false
    this.showNew = true
  }
  closeNew() {
    this.showNew = false
  }
  openSettings() {
    this.showNew = false
    this.showSettings = true
  }
  closeSettings() {
    this.showSettings = false
  }
  openAccounts() {
    this.showNew = false
    this.showSettings = false
    this.showChannels = false
    this.showProviders = false
    this.showAccounts = true
  }
  closeAccounts() {
    this.showAccounts = false
  }
  openChannels() {
    this.showNew = false
    this.showSettings = false
    this.showAccounts = false
    this.showProviders = false
    this.showChannels = true
  }
  closeChannels() {
    this.showChannels = false
  }
  openProviders() {
    this.showNew = false
    this.showSettings = false
    this.showAccounts = false
    this.showChannels = false
    this.showProviders = true
  }
  closeProviders() {
    this.showProviders = false
  }
  openAllSessions() {
    this.showNew = false
    this.showSettings = false
    this.showAllSessions = true
  }
  closeAllSessions() {
    this.showAllSessions = false
  }

  // loadAllHistory fetches the full resumable-session history across every
  // machine (well beyond the sidebar's small poll), tagged and newest-first.
  // Used by the "All sessions" view for its own search + pagination.
  async loadAllHistory(): Promise<TaggedHistoryEntry[]> {
    const results = await Promise.allSettled(
      this.machines.map((m) =>
        fetchHistory(m.url, 1000).then((hist) =>
          hist.map((h) => ({ ...h, machineId: m.id }) as TaggedHistoryEntry),
        ),
      ),
    )
    const all: TaggedHistoryEntry[] = []
    for (const r of results) if (r.status === 'fulfilled') all.push(...r.value)
    all.sort((a, b) => (a.mtime < b.mtime ? 1 : a.mtime > b.mtime ? -1 : 0))
    return all
  }

  async closeSessionActive() {
    const t = this.activeTab
    if (!t) return
    await closeSession(this.baseForMachine(t.machineId), t.id)
    this.closeTab(t, { ended: true }) // the session is gone, so its tab goes with it
    this.refresh()
  }

  // endSession closes a live session (its tab goes with it) but keeps its
  // transcript, so it stays resumable in Recent. This is "close", not "delete".
  async endSession(machineId: string, id: string) {
    await closeSession(this.baseForMachine(machineId), id)
    this.closeTabFor(machineId, id, { ended: true })
    this.refresh()
  }

  // renameSession gives a session a custom title that overrides the derived one.
  // An empty name clears the override (back to the derived title). Applies to a
  // live session or a Recent one, keyed by the shared id.
  async renameSession(machineId: string, id: string, name: string) {
    const trimmed = name.trim()
    await updateSessionMeta(this.baseForMachine(machineId), id, { name: trimmed })
    // Reflect it at once when we know the new title; refresh confirms (and a
    // cleared name needs the server's derived title, so pull that either way).
    if (trimmed) {
      this.sessions = this.sessions.map((s) => (s.machineId === machineId && s.id === id ? { ...s, title: trimmed } : s))
      this.history = this.history.map((h) => (h.machineId === machineId && h.id === id ? { ...h, title: trimmed } : h))
    }
    this.refresh({ history: true })
  }

  // setWorkspace groups a session under a name of your choosing instead of the
  // directory it started in, which is what you want once it holds more than one
  // codebase. An empty name clears it, dropping the session back under its
  // directory. Keyed by the shared id, so the grouping outlives the process.
  async setWorkspace(machineId: string, id: string, workspace: string) {
    const trimmed = workspace.trim()
    await updateSessionMeta(this.baseForMachine(machineId), id, { workspace: trimmed })
    const apply = <T extends { machineId: string; id: string }>(x: T) =>
      x.machineId === machineId && x.id === id ? { ...x, workspace: trimmed } : x
    this.sessions = this.sessions.map(apply)
    this.history = this.history.map(apply)
    this.refresh({ history: true })
  }

  // setPinned pins or unpins a session so it sticks to the top of the sidebar.
  async setPinned(machineId: string, id: string, pinned: boolean) {
    await updateSessionMeta(this.baseForMachine(machineId), id, { pinned })
    this.sessions = this.sessions.map((s) => (s.machineId === machineId && s.id === id ? { ...s, pinned } : s))
    this.history = this.history.map((h) => (h.machineId === machineId && h.id === id ? { ...h, pinned } : h))
    this.refresh({ history: true })
  }

  // deleteSession permanently removes a past session's transcript (and its pin/
  // rename). The server refuses a running session, so only Recent rows offer it.
  async deleteSession(machineId: string, id: string) {
    await deleteHistory(this.baseForMachine(machineId), id)
    this.history = this.history.filter((h) => !(h.machineId === machineId && h.id === id))
    this.refresh({ history: true })
  }

  // quickStart opens a fresh session in a known project dir on a given machine.
  // startWork is the home screen's one action: create a session in cwd and send the
  // first prompt straight into it. Starting work used to take three steps (pick a
  // folder, land in a session, type), which is two more than the thought deserves.
  // The prompt waits for the connection's backlog rather than firing blind, since a
  // send on a socket that has not opened yet is silently dropped.
  async startWork(machineId: string, cwd: string, prompt: string, spec: StartSpec = {}) {
    const text = prompt.trim()
    if (!text) return
    try {
      const base = this.baseForMachine(machineId)
      // A worktree is made first and handed to the session as a path. It has to
      // exist before the CLI is spawned, since the whole isolation mechanism is
      // that the session's cwd is the worktree.
      // The brief names the branch when the picker's name was left empty, which
      // it usually is: you have already said what the work is.
      const worktree = spec.wt ? await startWorktree(base, cwd, spec.wt, text) : ''
      const meta = await createSession(base, {
        cwd,
        model: spec.model || DEFAULT_MODEL,
        effort: spec.effort || DEFAULT_EFFORT,
        // Which account or provider runs it. Omitted means the machine's default,
        // which is what a single-account machine always wants.
        cli: spec.cli || undefined,
        worktree: worktree || undefined,
        // Omitted means the server's default. Sent, it is a spawn flag, which is
        // the only way it can govern the first tool call.
        mode: spec.mode,
        provider_model: spec.providerModel,
      })
      this.open(machineId, meta.id)
      const conn = this.conns.get(tabKey(machineId, meta.id))
      if (conn) {
        await conn.whenReady()
        conn.sendPrompt(text)
      }
      this.refresh()
    } catch (e) {
      this.listError = (e as Error).message
    }
  }

  // quickStart is the sidebar's one-tap "work here now": no prompt, no questions.
  // The spec is optional so the same path serves the worktree button beside it,
  // which is the same start with somewhere else to run and something to say
  // about how.
  async quickStart(machineId: string, cwd: string, spec: StartSpec = {}) {
    try {
      const base = this.baseForMachine(machineId)
      const worktree = spec.wt ? await startWorktree(base, cwd, spec.wt) : ''
      const meta = await createSession(base, {
        cwd,
        model: spec.model || DEFAULT_MODEL,
        effort: spec.effort || DEFAULT_EFFORT,
        cli: spec.cli || undefined,
        worktree: worktree || undefined,
        mode: spec.mode,
        provider_model: spec.providerModel,
      })
      this.open(machineId, meta.id)
      this.refresh()
    } catch (e) {
      this.listError = (e as Error).message
      // Rethrown as well as reported, because the caller may need to act on the
      // reason rather than only show it: the sidebar's worktree button stops
      // offering itself for a folder that turns out not to be a repository.
      throw e
    }
  }

  // restartWithEffort relaunches the active session at a new reasoning effort.
  // The server closes and resumes it under the same id, so we rebuild the
  // connection from scratch (its seqs reset, so the existing socket would miss
  // the replayed history).
  // reopenActive resumes the session the active tab is pointing at, after the
  // server lost it.
  //
  // Nothing is actually lost when kunai restarts: the conversation is on disk in
  // the transcript, and resuming reads it back. Only the process is gone. So the
  // one useful thing to offer beside "this session has ended" is the one tap that
  // brings it back, on the same account and in the same folder, rather than
  // sending somebody to hunt for it in Recent.
  async reopenActive() {
    const t = this.activeTab
    const conn = this.chat
    if (!t || !conn) return
    this.actionError = ''
    try {
      const meta = await createSession(this.baseForMachine(t.machineId), {
        cwd: conn.cwd,
        resume: t.id,
        title: conn.title,
        cli: conn.cli || undefined,
      })
      this.closeTabFor(t.machineId, t.id, { ended: true }) // drop the dead tab
      this.open(t.machineId, meta.id)
      this.refresh()
    } catch (e) {
      this.actionError = (e as Error).message
    }
  }

  async restartWithEffort(effort: string) {
    const t = this.activeTab
    if (!t) return
    this.actionError = ''
    try {
      await apiSetEffort(this.baseForMachine(t.machineId), t.id, effort)
      await this.swapConnection(t, (c) => {
        c.effort = effort // reflect the new effort at once; hello confirms it
      })
      this.refresh()
    } catch (e) {
      this.actionError = switchFailure('', e as Error)
    }
  }


  // swapConnection rebuilds a tab's connection after the server respawned the
  // session under the same id (an effort change, an account switch), whose seqs
  // reset so the live socket cannot simply carry on.
  //
  // Swapping an empty replacement in straight away made the conversation blank
  // and then repopulate, which reads as a flicker. Freeze the outgoing
  // connection instead: destroy stops its socket and its reconnects but leaves
  // its rendered turns untouched, so the view keeps showing them while the
  // replacement fills in the background. Only once that one has its backlog do
  // they trade places, and the change lands in a single paint.
  private async swapConnection(t: Tab, apply: (c: ChatConnection) => void) {
    const key = tabKey(t.machineId, t.id)
    this.conns.get(key)?.destroy()
    const next = new ChatConnection(this.baseForMachine(t.machineId), t.id)
    apply(next)
    await next.whenReady()
    this.conns.set(key, next)
    this.connsVersion++
  }

  // switchAccount moves the active session to a different Claude account, keeping
  // its conversation (the server copies the transcript and resumes). Like an
  // effort change, the session respawns under the same id, so the connection is
  // rebuilt.
  async switchAccount(name: string) {
    const t = this.activeTab
    if (!t) return
    this.actionError = ''
    try {
      await apiSetAccount(this.baseForMachine(t.machineId), t.id, name)
      await this.swapConnection(t, (c) => {
        c.cli = name // reflect the new account at once; hello confirms it
      })
      this.refresh()
    } catch (e) {
      this.actionError = switchFailure(name, e as Error)
    }
  }

  // switchProviderModel moves the active session to another model on the provider
  // it already runs on (gpt-5.4 -> gpt-5.5, say). The model is baked into the
  // spawn env, so the server respawns the session exactly as an account switch
  // does, which means the connection has to be rebuilt for the same reason: the
  // new process's sequence numbers start again and the live socket would ignore
  // every event below its old high-water mark.
  //
  // This used to call refresh() and nothing else, so the chip label followed
  // while the socket stayed attached to a process that no longer existed, and a
  // failure was swallowed whole. Between them that is a switch that reports
  // nothing and appears to do nothing.
  async switchProviderModel(model: string) {
    const t = this.activeTab
    if (!t) return
    this.actionError = ''
    try {
      await apiSetProviderModel(this.baseForMachine(t.machineId), t.id, model)
      await this.swapConnection(t, () => {})
      this.refresh()
    } catch (e) {
      this.actionError = switchFailure('', e as Error)
    }
  }

  // --- URL routing: /m/<machineSlug>/<sessionId>, legacy /<sessionId> = self ---

  private navigating = false
  private pendingDeepLink: { machineId: string; id: string } | null = null

  private syncUrl() {
    if (this.navigating) return
    const want =
      this.activeId && this.activeMachineId ? `/m/${this.activeMachineId}/${this.activeId}` : '/'
    if (location.pathname !== want) history.pushState({}, '', want)
  }

  private currentPath(): { machineId: string | null; id: string } {
    const parts = location.pathname.replace(/^\/+/, '').split('/').filter(Boolean)
    if (parts[0] === 'm' && parts.length >= 3) return { machineId: parts[1], id: parts[2] }
    if (parts.length >= 1) return { machineId: null, id: parts[0] }
    return { machineId: null, id: '' }
  }

  initRouting() {
    window.addEventListener('popstate', () => this.applyPath())
    this.applyPath()
  }

  private applyPath() {
    // /resume/<id> is the handoff link a terminal session hands out. The resume
    // happens HERE, on open, rather than when the link was minted: the terminal's
    // own claude is still running at that moment, and two processes appending to
    // one transcript is how a conversation gets corrupted. By the time somebody
    // clicks, that terminal has exited.
    const handoff = location.pathname.match(/^\/resume\/([^/?#]+)/)
    if (handoff) {
      // The folder rides on the link, because a conversation handed off minutes
      // after it started may not be in Recent yet and one handed off during its
      // first turn is not on disk at all when the link is minted.
      const cwd = new URLSearchParams(location.search).get('cwd') ?? ''
      void this.resumeById(decodeURIComponent(handoff[1]), cwd)
      return
    }
    const { machineId, id } = this.currentPath()
    this.navigating = true
    try {
      if (id) {
        const mid = machineId ?? this.selfId()
        if (this.activeId !== id || this.activeMachineId !== mid) {
          if (machineId && !this.machines.some((m) => m.id === machineId)) {
            this.pendingDeepLink = { machineId, id } // machine not loaded yet
          } else {
            this.open(mid, id)
          }
        }
      } else if (this.activeId) {
        this.back()
      }
    } finally {
      this.navigating = false
    }
  }

  // resumeById continues a conversation this machine has on disk, by its id.
  //
  // The id comes from a terminal session that has since exited, so there is no
  // live session to open: the transcript is looked up in Recent for the folder,
  // title and account it belongs to, and the session is created resuming it. An
  // id that is already live just opens, so clicking the link twice is harmless.
  async resumeById(id: string, cwd = '') {
    this.actionError = ''
    history.replaceState({}, '', '/') // the /resume link is single-use; do not leave it in the bar
    const live = this.sessions.find((s) => s.id === id)
    if (live) {
      this.open(live.machineId, live.id)
      return
    }
    // Recent is refreshed rather than trusted: this link was minted seconds ago
    // by a terminal that has only just exited, and a stale list is the common
    // case rather than the exception.
    await this.refresh()
    const h = this.history.find((r: TaggedHistoryEntry) => r.id === id)
    // The link's own folder is the fallback, so a conversation too young to be
    // in Recent still resumes. Without it the first-turn handoff -- the one
    // somebody actually reaches for, having just started work -- was the one
    // case that could not work.
    const target = h ?? (cwd ? { machineId: this.selfId(), id, cwd, title: '', cli: '' } : null)
    if (!target) {
      this.actionError =
        'That conversation is not on this machine. A handoff link only works on the machine the terminal was running on.'
      return
    }
    try {
      const meta = await createSession(this.baseForMachine(target.machineId), {
        cwd: target.cwd,
        resume: id,
        title: target.title,
        cli: target.cli, // reopen on the account it belongs to
      })
      this.open(target.machineId, meta.id)
      this.refresh()
    } catch (e) {
      this.actionError = switchFailure('', e as Error)
    }
  }

  private drainDeepLink() {
    const p = this.pendingDeepLink
    if (p && this.machines.some((m) => m.id === p.machineId)) {
      this.pendingDeepLink = null
      this.open(p.machineId, p.id)
    }
  }
}

function sameOrigin(a: string, b: string): boolean {
  try {
    return new URL(a).origin === new URL(b).origin
  } catch {
    return false
  }
}

export const app = new AppStore()
