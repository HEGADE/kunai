// The rules a "start a session" control follows, given what the machine has.
//
// Three places ask the same questions: the home launcher, the New Session dialog
// and the sidebar's worktree dialog. The answers are not obvious -- a provider
// serves one real upstream model and knows nothing about Claude's tiers, and
// reasoning effort is a Claude flag its model never sees -- and writing them
// inline is exactly how the worktree dialog came to handle providers correctly
// while the New Session dialog went on offering Opus/Sonnet/Haiku beside a Codex
// account: four buttons that all did the same nothing.
//
// Deliberately dependency-free. The model, effort and permission catalogues live
// in their own modules and one of them reaches into a runes store, so importing
// them here would make these rules unloadable outside a bundler and therefore
// untestable. This module decides; the caller supplies the lists. What is shared
// is the part that was actually getting decided differently.

// isProvider answers from the provider->model map alone, which is keyed by
// provider name and already on the wire. Nothing extra has to be sent to tell a
// Codex account from a Claude one.
export function isProvider(cli: string, providerModels: Record<string, string>): boolean {
  return !!cli && cli in providerModels
}

// chosenCli resolves '' to the machine's default, which is the first entry.
export function chosenCli(cli: string, clis: string[]): string {
  return cli || clis[0] || ''
}

// providerModelChoices is what to offer for a provider: whatever the proxy said
// it can serve, plus the model it is currently on when the list came back without
// it. An empty list is what a lapsed login or a still-starting proxy looks like,
// and an empty control is the worst outcome there: this is the moment you most
// need to see what you are about to run.
export function providerModelChoices(current: string, served: string[]): string[] {
  if (!current) return served
  return served.includes(current) ? served : [current, ...served]
}

// showEffort is false on a provider. Effort is a Claude reasoning level; the
// provider's model never sees the flag, so the control would change nothing.
export function showEffort(cli: string, providerModels: Record<string, string>): boolean {
  return !isProvider(cli, providerModels)
}

// providerModelToSend is the value a create should carry: only on a provider, and
// only when it differs from what that provider is already mapped to. Sending it
// pins the mapping for that provider's NEXT session too, so it must not be
// written back when nothing was actually chosen.
export function providerModelToSend(
  cli: string,
  providerModels: Record<string, string>,
  chosen: string,
): string | undefined {
  if (!isProvider(cli, providerModels) || !chosen) return undefined
  return chosen === providerModels[cli] ? undefined : chosen
}
