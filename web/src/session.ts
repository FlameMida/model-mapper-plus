// Management key lives in memory only; a page refresh means logging in again
// (same rule as key-policy).
let managementKey = ''

export function getKey(): string {
  return managementKey
}
export function setKey(key: string): void {
  managementKey = key
}
export function clearKey(): void {
  managementKey = ''
}
export function hasKey(): boolean {
  return managementKey !== ''
}
