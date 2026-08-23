import { ChannelTarget } from './api'

interface ChannelTargetInput {
  enabled?: boolean
  suppliers?: readonly string[]
  auth_ids?: readonly string[]
}

function normalizedUniqueStrings(values: readonly string[], foldCase: boolean): string[] {
  const seen = new Set<string>()
  const result: string[] = []
  for (const raw of values) {
    const value = raw.trim()
    if (!value) continue
    const key = foldCase ? value.toLowerCase() : value
    if (seen.has(key)) continue
    seen.add(key)
    result.push(value)
  }
  return result.sort((a, b) => a.localeCompare(b))
}

export function normalizeProviders(values: readonly string[]): string[] {
  return normalizedUniqueStrings(values, true)
}

export function normalizeAuthIDs(values: readonly string[]): string[] {
  return normalizedUniqueStrings(values, false)
}

export function providerKey(value: string): string {
  return value.trim().toLowerCase()
}

export function normalizeChannelTarget(value?: ChannelTargetInput): ChannelTarget {
  return {
    enabled: value?.enabled ?? false,
    suppliers: normalizeProviders(value?.suppliers ?? []),
    auth_ids: normalizeAuthIDs(value?.auth_ids ?? []),
  }
}
