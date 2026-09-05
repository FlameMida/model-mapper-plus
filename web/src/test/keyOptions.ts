import type { KeyOptionsState } from '../useKeyOptions'

export function createKeyOptions(overrides: Partial<KeyOptionsState> = {}): KeyOptionsState {
  return {
    options: [], keysLoading: false, keysError: '', keeper: { status: 'disabled', items: [] }, keeperLoading: false,
    refreshAliases: async () => ({ status: 'disabled', items: [] }),
    ...overrides,
  }
}
