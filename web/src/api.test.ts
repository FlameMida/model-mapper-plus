import { describe, it, expect, vi, afterEach } from 'vitest'
import { api, listCpaAuthFiles, StateResponse } from './api'

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

const BASE_STATE: StateResponse = {
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
          blocked: false,
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
      key_bindings: [{ key: 'sk-1', alias: '', enabled: false, blocked: false, rules: { global: '', claude: '', codex: '', openai: '' } }],
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

describe('api：渠道定向与 Fast', () => {
  it('auth-files 解包 files 并携带管理密钥', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => ({
      ok: true,
      status: 200,
      json: async () => ({
        files: [{ id: 'f1', provider: 'gemini', status: 'active', disabled: false, label: 'Gemini Main' }],
      }),
    }))
    vi.stubGlobal('fetch', fetchMock)

    await expect(listCpaAuthFiles()).resolves.toEqual([
      { id: 'f1', provider: 'gemini', status: 'active', disabled: false, label: 'Gemini Main' },
    ])
    expect(fetchMock).toHaveBeenCalledWith('/v0/management/auth-files', expect.objectContaining({
      headers: expect.objectContaining({ Authorization: expect.stringMatching(/^Bearer /) }),
    }))
  })

  it('auth-files 非 2xx 返回可显示错误', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: false, status: 503, json: async () => ({}) })))
    await expect(listCpaAuthFiles()).rejects.toThrow('读取 CPA auth-files 失败：HTTP 503')
  })

  it('PATCH 可发送 false 与完整渠道定向对象', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => ({
      ok: true,
      status: 200,
      text: async () => JSON.stringify(BASE_STATE),
    }))
    vi.stubGlobal('fetch', fetchMock)
    await api.patchKey('sk-k', {
      fast_allowed: false,
      channel_target: { enabled: true, suppliers: ['gemini'], auth_ids: ['f1'] },
    })
    const init = fetchMock.mock.calls[0][1] as RequestInit
    expect(JSON.parse(String(init.body))).toEqual({
      fast_allowed: false,
      channel_target: { enabled: true, suppliers: ['gemini'], auth_ids: ['f1'] },
    })
  })
})
