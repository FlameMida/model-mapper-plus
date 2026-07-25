// Management key lives in memory only; a page refresh means logging in again
// (same rule as key-policy).
let managementKey = ''

type AuthListener = (authed: boolean) => void
const listeners = new Set<AuthListener>()

function notify(authed: boolean): void {
  listeners.forEach((fn) => fn(authed))
}

export function onAuthChange(fn: AuthListener): () => void {
  listeners.add(fn)
  return () => { listeners.delete(fn) }
}

export function getKey(): string {
  return managementKey
}
export function setKey(key: string): void {
  managementKey = key
  notify(managementKey !== '')
}
export function clearKey(): void {
  managementKey = ''
  notify(false)
}
export function hasKey(): boolean {
  return managementKey !== ''
}
