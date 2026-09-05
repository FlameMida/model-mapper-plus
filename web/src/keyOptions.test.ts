import { describe, expect, it } from 'vitest'
import { buildKeyOptions } from './keyOptions'
import type { KeyBinding } from './api'

const binding = (key: string, alias: string): KeyBinding => ({ key, alias, enabled: true, blocked: false, rules: { global: '', claude: '', codex: '', openai: '' } })

describe('选择器优先级及搜索', () => {
  it('按本地、Keeper、脱敏 Key 显示，搜索保留两边别名', () => {
    const keys = ['sk-local-123456', 'sk-keeper-654321', 'sk-bare-abcdef']
    const options = buildKeyOptions(keys, [binding(keys[0], '本地')], [
      { key: keys[0], alias: '远端旧名' }, { key: keys[1], alias: '团队 & <主用>' },
      { key: 'sk-keeper-only', alias: '不应出现' },
    ])
    expect(options.map(o => o.label)).toEqual(['本地 · sk-loc…3456', '团队 & <主用> · sk-kee…4321', 'sk-bar…cdef'])
    expect(options.map(o => o.value)).toEqual(keys)
    expect(options[0].searchText).toContain('远端旧名')
    expect(options[0].searchText).toContain('本地')
    expect(options[0].searchText).toContain(keys[0])
  })
  it('同名别名和相同掩码不能合并不同 Key', () => {
    const keys = ['sk-aaa-middle-one-1234', 'sk-aaa-middle-two-1234']
    const options = buildKeyOptions(keys, [], keys.map(key => ({ key, alias: '同名' })))
    expect(options).toHaveLength(2)
    expect(options[0].label).toBe(options[1].label)
    expect(options.map(o => o.value)).toEqual(keys)
  })
  it('空白别名按空值处理，短 Key 和完整 Key 精确匹配', () => {
    expect(buildKeyOptions(['sk-a', 'sk-b'], [binding('sk-a', '  ')], [{ key: 'sk-a', alias: ' A ' }]).map(o => o.label)).toEqual(['A · sk-a', 'sk-b'])
  })
})
