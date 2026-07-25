// Worktrees: the client half of internal/worktree and internal/server/worktrees*.
//
// A worktree is a second checkout of the same repository on its own branch, so
// two agents can work on one codebase without overwriting each other. Starting a
// session in one is a single substitution on the server (its path becomes the
// cwd), which is why nothing here has to touch the session API beyond passing a
// path along.
//
// Wire types mirror the Go structs; keep them in step by hand, as with types.ts.

const at = (base: string, path: string) => `${base}${path}`

async function json<T>(r: Response): Promise<T> {
  if (!r.ok) {
    // The server puts a sentence in {"error": ...}; it is more use than the code.
    const body = await r.json().catch(() => null)
    throw new Error((body as { error?: string } | null)?.error || `HTTP ${r.status}`)
  }
  return r.json() as Promise<T>
}

export type SetupState = 'none' | 'running' | 'ok' | 'failed' | 'timed_out' | 'skipped'

export interface SetupResult {
  state: SetupState
  command?: string
  output?: string
  exit_code?: number
  duration_s?: number
}

export interface WorktreeStatus {
  ahead: number
  behind: number
  dirty: number
  // Nullable in principle: Go marshals a nil slice as null, and a wire type that
  // says string[] while the wire says null is how a card ends up throwing on a
  // worktree nobody has edited yet. The server sends [] now; this stays honest.
  files: string[] | null
  base_moved?: boolean
}

export interface Worktree {
  path: string
  repo: string
  branch: string
  base: string
  base_sha: string
  created_at: number
  setup: SetupResult
  shared?: string[]
  // sessions and status are filled in by the list endpoint only.
  sessions?: string[]
  status?: WorktreeStatus
}

export interface BranchRef {
  name: string
  remote: boolean
  current?: boolean
  default?: boolean
  // in_use names the worktree already holding this branch. git refuses to check
  // one branch out twice, so the picker has to be able to say why.
  in_use?: string
}

export interface BranchList {
  refs: BranchRef[]
  default: string
  from_origin: boolean
  repo: string
}

// SetupProposal is what would run in a new worktree, and where that came from.
// A suggestion is shown before it runs and never inferred-and-executed silently:
// it is arbitrary shell with the server's privileges.
export interface SetupProposal {
  command: string
  source: 'project' | 'suggested' | 'none'
  why?: string
}

export interface MergeResult {
  branch: string
  base: string
  fast_forward: boolean
  commits: number
  already_merged?: boolean
}

export function listWorktrees(base: string): Promise<Worktree[]> {
  return fetch(at(base, '/api/worktrees')).then((r) => json<Worktree[]>(r))
}

export function worktreeBranches(base: string, repo: string): Promise<BranchList> {
  return fetch(at(base, `/api/worktrees/branches?repo=${encodeURIComponent(repo)}`)).then((r) =>
    json<BranchList>(r),
  )
}

export function worktreeSetup(base: string, repo: string): Promise<SetupProposal> {
  return fetch(at(base, `/api/worktrees/setup?repo=${encodeURIComponent(repo)}`)).then((r) =>
    json<SetupProposal>(r),
  )
}

// setup is deliberately optional and distinct from an empty string: omitted
// means "run whatever this repository resolves to", which is what a one-tap
// start needs since it never showed anyone a command; an explicit '' means the
// user looked and chose none.
export function createWorktree(
  base: string,
  body: {
    repo: string
    name?: string
    base?: string
    setup?: string
    remember?: boolean
    // prompt is the task this worktree is for. The server names the branch from
    // it when no name was given, so nobody has to name a branch before they have
    // described the work.
    prompt?: string
  },
): Promise<Worktree> {
  return fetch(at(base, '/api/worktrees'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  }).then((r) => json<Worktree>(r))
}

export function mergeWorktree(base: string, path: string): Promise<MergeResult> {
  return fetch(at(base, '/api/worktrees/merge'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path }),
  }).then((r) => json<MergeResult>(r))
}

export function pullRequestWorktree(base: string, path: string): Promise<{ url: string }> {
  return fetch(at(base, '/api/worktrees/pr'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path }),
  }).then((r) => json<{ url: string }>(r))
}

// deleteWorktree removes the worktree and, unless keepBranch, its branch. force
// is needed when there are uncommitted changes; the caller is responsible for
// having said what that would destroy.
export function deleteWorktree(
  base: string,
  path: string,
  opts: { force?: boolean; keepBranch?: boolean } = {},
): Promise<void> {
  const q = new URLSearchParams({ path })
  if (opts.force) q.set('force', '1')
  if (opts.keepBranch) q.set('keep_branch', '1')
  return fetch(at(base, `/api/worktrees?${q}`), { method: 'DELETE' }).then((r) =>
    json<unknown>(r).then(() => undefined),
  )
}

// --- the choice a launcher makes ---------------------------------------------

// WorktreeChoice is what every place that starts a session carries: whether to
// isolate the work, and if so from where and under what name. One shape shared by
// the home launcher, the new-session dialog and the sidebar, so the three cannot
// drift into asking the same question three different ways.
export interface WorktreeChoice {
  // on is false for the current checkout, which stays the default everywhere.
  on: boolean
  base: string
  name: string
  // setup undefined means the repository's own command, which is what a one-tap
  // start wants; a string is what the picker showed and the user accepted.
  setup?: string
}

export function noWorktree(): WorktreeChoice {
  return { on: false, base: '', name: '' }
}

// justAWorktree is the one-tap choice: isolate the work, take every default.
export function justAWorktree(): WorktreeChoice {
  return { on: true, base: '', name: '' }
}

// startWorktree creates the worktree a choice describes and returns its path, to
// be handed to createSession. Returns '' when the choice is off, so callers read
// as one line rather than a branch.
export async function startWorktree(
  base: string,
  repo: string,
  choice: WorktreeChoice,
  prompt = '',
): Promise<string> {
  if (!choice.on) return ''
  const wt = await createWorktree(base, {
    repo,
    name: choice.name,
    base: choice.base,
    prompt,
    setup: choice.setup,
    // Remember the command per repo once someone has accepted it, so the next
    // worktree of this repository does not ask again. Never for a one-tap start,
    // which chose nothing.
    remember: choice.setup !== undefined,
  })
  return wt.path
}

// --- presentation helpers (pure, so they can be tested without a DOM) ---------

// branchName is what to call a worktree in one word: the part after kunai/.
export function branchName(branch: string): string {
  return branch.replace(/^kunai\//, '')
}

// summarise is the one line a card or a pill shows about where a worktree stands.
// It leads with whatever most needs acting on, because a status line that reads
// "3 ahead" while the setup is broken has buried the thing you needed to know.
export function summarise(wt: Worktree): string {
  if (wt.setup.state === 'running') return 'Preparing…'
  if (wt.setup.state === 'failed' || wt.setup.state === 'timed_out') return 'Setup failed'
  const st = wt.status
  if (!st) return branchName(wt.branch)
  const parts: string[] = []
  if (st.ahead) parts.push(`${st.ahead} ahead`)
  if (st.behind) parts.push(`${st.behind} behind`)
  if (st.dirty) parts.push(`${st.dirty} uncommitted`)
  return parts.length ? parts.join(' · ') : 'No changes yet'
}

// canLand reports whether there is anything to merge or open a pull request for.
export function canLand(wt: Worktree): boolean {
  return (wt.status?.ahead ?? 0) > 0
}
