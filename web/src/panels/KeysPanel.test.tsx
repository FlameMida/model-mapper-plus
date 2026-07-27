import { describe, it, expect } from 'vitest'
import { planKeySave } from './KeysPanel'
import { KeyBinding } from '../api'

const binding = (key: string): KeyBinding => ({
  key,
  alias: '别名',
  enabled: true,
  rules: { global: 'a=>b', claude: '', codex: '', openai: '' },
})

describe('审计 #12：编辑时改 key 应为重命名而非新增', () => {
  it('改了 key：写入新 key 并删除旧 key', () => {
    const plan = planKeySave('sk-old', binding('sk-new'))
    expect(plan?.binding.key).toBe('sk-new')
    expect(plan?.deleteKey).toBe('sk-old')
  })

  it('未改 key：不删除任何绑定', () => {
    const plan = planKeySave('sk-same', binding('sk-same'))
    expect(plan?.binding.key).toBe('sk-same')
    expect(plan?.deleteKey).toBe('')
  })

  it('新增（无 originalKey）：不删除任何绑定', () => {
    const plan = planKeySave('', binding('sk-new'))
    expect(plan?.binding.key).toBe('sk-new')
    expect(plan?.deleteKey).toBe('')
  })

  it('key 写入前 trim，且仅空白差异不算重命名', () => {
    const plan = planKeySave('sk-same', binding('  sk-same  '))
    expect(plan?.binding.key).toBe('sk-same')
    expect(plan?.deleteKey).toBe('')
  })

  it('key 为空白时拒绝保存', () => {
    expect(planKeySave('sk-old', binding('   '))).toBeNull()
  })

  it('保留除 key 外的其余字段', () => {
    const plan = planKeySave('sk-old', { ...binding('sk-new'), alias: 'X', enabled: false })
    expect(plan?.binding.alias).toBe('X')
    expect(plan?.binding.enabled).toBe(false)
    expect(plan?.binding.rules.global).toBe('a=>b')
  })
})
