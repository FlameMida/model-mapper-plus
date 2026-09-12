import { expect, it } from 'vitest'
import { findKeeperAuthName } from './keeperAuthNames'
import type { CpaAuthFile } from './api'

const FILE: CpaAuthFile = { id: 'cpa-id', auth_index: 'idx-a', provider: 'codex', type: 'codex',
  source: 'auth-file', name: 'same.json', label: '同名', status: 'active', disabled: false }
const NAME = { identity_id: '17', auth_index: 'idx-a', alias: '生产', display_name: '生产' }

it('同名文件只精确匹配认证索引，不把 Keeper ID 或 CPA ID 当索引', () => {
  expect(findKeeperAuthName(FILE, [NAME])).toEqual(NAME)
  for (const auth_index of ['idx-b', ' idx-a ', '17', 'cpa-id', '']) {
    expect(findKeeperAuthName({ ...FILE, auth_index }, [NAME])).toBeUndefined()
  }
})
it.each([
  { source: 'ai-provider' as const }, { source: undefined }, { provider: 'claude' },
  { type: 'unknown' }, { auth_index: undefined },
])('非 Codex auth-file 或缺失索引不可匹配：%j', overrides => {
  expect(findKeeperAuthName({ ...FILE, ...overrides }, [NAME])).toBeUndefined()
})
it('索引有多个身份时拒绝猜测', () => {
  expect(findKeeperAuthName(FILE, [NAME, { ...NAME, identity_id: '18' }])).toBeUndefined()
})
