import type { KeyBinding, KeeperAlias } from './api'

export interface KeyOption {
  value: string
  label: string
  searchText: string
}

export function maskKey(key: string): string {
  return key.length <= 10 ? key : `${key.slice(0, 6)}…${key.slice(-4)}`
}

export function buildKeyOptions(keys: string[], bindings: KeyBinding[], aliases: KeeperAlias[]): KeyOption[] {
  const local = new Map(bindings.map(b => [b.key, b.alias.trim()]))
  const remote = new Map(aliases.map(a => [a.key, a.alias.trim()]))
  return [...new Set(keys)].map(key => {
    const localAlias = local.get(key) ?? ''
    const keeperAlias = remote.get(key) ?? ''
    const alias = localAlias || keeperAlias
    const masked = maskKey(key)
    return { value: key, label: alias ? `${alias} · ${masked}` : masked, searchText: [key, masked, localAlias, keeperAlias].join(' ').toLocaleLowerCase() }
  })
}
