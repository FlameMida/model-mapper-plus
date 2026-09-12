import type { CpaAuthFile, KeeperAuthName } from './api'

export function findKeeperAuthName(file: CpaAuthFile, names: readonly KeeperAuthName[]): KeeperAuthName | undefined {
  if (file.source !== 'auth-file' || file.provider !== 'codex' || !file.auth_index ||
    (file.type !== undefined && file.type !== 'codex')) return undefined
  const matches = names.filter(name => name.auth_index === file.auth_index)
  return matches.length === 1 ? matches[0] : undefined
}
