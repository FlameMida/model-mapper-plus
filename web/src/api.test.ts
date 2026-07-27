import { describe, it, expect, vi, afterEach } from 'vitest'
import { api } from './api'

/**
 * CPA 宿主对插件 management 响应统一跑 html.EscapeString
 * （internal/pluginhost/management.go:267 → htmlsanitize.JSONBody），
 * JSON 里每个字符串值的 `& ' < > "` 都会变成 HTML 实体。
 * 规则 DSL 的分隔符是 `=>`，到达浏览器时成了 `=&gt;`，
 * dsl.ts 的 splitEntries 匹配不到就整条丢弃 —— 表现为「保存后不回显」。
 * 前端必须在 API 层对称还原。
 */
function stubFetch(payload: unknown, status = 200): void {
  vi.stubGlobal('fetch', vi.fn(async () => ({
    ok: status >= 200 && status < 300,
    status,
    text: async () => JSON.stringify(payload),
  })))
}

const BASE_STATE = {
  version: 1,
  rules: { global: '', claude: '', codex: '', openai: '' },
  key_bindings: [],
  persisted: true,
  state_file: '/tmp/s.json',
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('api：还原 CPA 宿主的 HTML 实体转义', () => {
  it('规则 DSL 的 =&gt; 还原为 =>（本 bug 的直接成因）', async () => {
    stubFetch({
      ...BASE_STATE,
      rules: { global: 'g-src=&gt;g-dst', claude: 'c-src=&gt;c-dst', codex: '', openai: '' },
    })

    const state = await api.getState()

    expect(state.rules.global).toBe('g-src=>g-dst')
    expect(state.rules.claude).toBe('c-src=>c-dst')
  })

  it('putRules 的响应同样还原（保存后的回显走这条路径）', async () => {
    stubFetch({ ...BASE_STATE, rules: { global: 'a=&gt;b', claude: '', codex: '', openai: '' } })

    const state = await api.putRules({ global: 'a=>b', claude: '', codex: '', openai: '' })

    expect(state.rules.global).toBe('a=>b')
  })

  it('还原 html.EscapeString 的全部五个实体', async () => {
    stubFetch({
      ...BASE_STATE,
      rules: { global: '&lt;x&gt;=&gt;&amp;y', claude: '&#39;q&#34;', codex: '', openai: '' },
    })

    const state = await api.getState()

    expect(state.rules.global).toBe('<x>=>&y')
    expect(state.rules.claude).toBe("'q\"")
  })

  it('只解一层：&amp;gt; 还原为 &gt; 而非 >', async () => {
    // 原文里字面量的 `&gt;` 被宿主转义成 `&amp;gt;`，还原必须停在 `&gt;`。
    stubFetch({ ...BASE_STATE, rules: { global: 'a&amp;gt;b', claude: '', codex: '', openai: '' } })

    const state = await api.getState()

    expect(state.rules.global).toBe('a&gt;b')
  })

  it('嵌套的 key 绑定（数组 + 对象）里的字符串也还原', async () => {
    stubFetch({
      ...BASE_STATE,
      key_bindings: [
        {
          key: 'sk-1',
          alias: 'Tom &amp; Jerry',
          enabled: true,
          rules: { global: 'k-src=&gt;k-dst', claude: '', codex: '', openai: '' },
        },
      ],
    })

    const state = await api.getState()

    expect(state.key_bindings[0].alias).toBe('Tom & Jerry')
    expect(state.key_bindings[0].rules.global).toBe('k-src=>k-dst')
  })

  it('非字符串值（数字 / 布尔）原样保留', async () => {
    stubFetch({
      ...BASE_STATE,
      version: 1,
      persisted: true,
      key_bindings: [{ key: 'sk-1', alias: '', enabled: false, rules: { global: '', claude: '', codex: '', openai: '' } }],
    })

    const state = await api.getState()

    expect(state.version).toBe(1)
    expect(state.persisted).toBe(true)
    expect(state.key_bindings[0].enabled).toBe(false)
  })

  it('试跑结果里的模型名也还原', async () => {
    stubFetch({ m1: 'a&amp;b', m2: 'c&gt;d', routed: true, final: 'c&gt;d' })

    const resp = await api.preview({ format: 'openai', model: 'x' })

    expect(resp.m1).toBe('a&b')
    expect(resp.final).toBe('c>d')
  })

  it('错误响应的提示文案也还原', async () => {
    stubFetch({ error: 'rules.global: invalid rule &quot;a=&gt;&quot;' }, 400)

    await expect(api.getState()).rejects.toThrow('rules.global: invalid rule &quot;a=>&quot;')
  })
})
