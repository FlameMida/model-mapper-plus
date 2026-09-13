import { afterEach, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import AuditPanel from './AuditPanel'
import { arrayDiff, diffLines, resolveName, summarizeChanges } from '../auditDisplay'

const keyOperation = {
  operation_id: 'op-key', actor: 'management_api', action: 'update', module: 'key_binding',
  object_type: 'key_binding', object_ref: 'key:••••8f2a', object_label: 'prod-deepseek',
  started_at: '2026-09-13T14:32:05+08:00', finished_at: '2026-09-13T14:32:05.458+08:00',
  outcome: 'succeeded', changed: true,
  changes: {
    'channel_target.enabled': { before: false, after: true },
    'channel_target.suppliers': {
      before: ['openai-compatible-deepseek', 'openai-compatible-siliconflow'],
      after: ['openai-compatible-volc', 'openai-compatible-siliconflow'],
    },
    'channel_target.auth_ids': { before: [] as string[], after: ['codex:auth1'] },
  },
  labels: { 'openai-compatible-volc': '火山方舟' },
}
const rulesOperation = {
  operation_id: 'op-rules', actor: 'management_api', action: 'update', module: 'rules',
  object_type: 'rules', object_ref: 'rules', object_label: '全局规则',
  started_at: '2026-09-13T13:58:47+08:00', finished_at: '2026-09-13T13:58:47.100+08:00',
  outcome: 'succeeded', changed: true,
  changes: { 'rules.claude': { before: 'a=>b\nc=>d', after: 'a=>b\nc=>e\nf=>g' } },
}
const v1Operation = {
  operation_id: 'op-v1', actor: 'management_api', action: 'update',
  object_type: 'key_binding', object_ref: 'sha256:ca97 masked:***',
  started_at: '2026-09-13T09:12:40+08:00', outcome: 'succeeded', changed: true,
  changes: { global: { before: 'old=&gt;model', after: '&lt;script&gt;=&gt;new' } },
}
const page = (items: unknown[], over: Record<string, unknown> = {}) => ({
  date: '2026-09-13', timezone: 'Asia/Shanghai', page: 1, page_size: 20, total: items.length,
  module_counts: { rules: 1, key_binding: 3, notifications: 2, keeper_auth_name: 1 },
  items, warnings: [] as string[], ...over,
})
const response = (body: unknown, status = 200) => ({
  ok: status === 200, status,
  text: async () => JSON.stringify(body),
  json: async () => body,
})
const providerEndpoints = ['gemini-api-key', 'interactions-api-key', 'claude-api-key',
  'codex-api-key', 'xai-api-key', 'openai-compatibility', 'vertex-api-key']
// 审计富化链：CPA auth-files + 各 provider endpoint + 插件 channel-credentials + Keeper 名称。
const enrichStubs = {
  authFiles: { files: [{ id: 'codex:auth1', provider: 'codex', status: 'active', disabled: false, label: 'auth.pem', source: 'auth-file', auth_index: '3' }] },
  keeper: { status: 'ready', items: [{ identity_id: '9', auth_index: '3', alias: '主力', display_name: 'Codex 主力' }] },
}
afterEach(() => vi.unstubAllGlobals())

it('模块 chips 展示全日计数，点击后请求携带 module 参数', async () => {
  const requests: string[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    requests.push(url)
    return response(page([keyOperation, rulesOperation]))
  }))
  const user = userEvent.setup()
  render(<AuditPanel />)
  expect(await screen.findByRole('button', { name: /prod-deepseek/ })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /全局规则.*1/ })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /Key 绑定.*3/ })).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: /Key 绑定.*3/ }))
  expect(requests.at(-1)).toContain('module=key_binding')
  await user.click(screen.getByRole('button', { name: /Key 绑定.*3/ }))
  expect(requests.at(-1)).not.toContain('module=')
})

it('行内展开渠道定向三字段 diff，事件 labels 快照优先于实时富化，未命中回落原始 ID', async () => {
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    if (url.includes('/audit')) return response(page([keyOperation]))
    if (url.includes('/v0/management/auth-files')) return response(enrichStubs.authFiles)
    if (url.includes('/channel-credentials')) return response([])
    if (url.includes('/keeper/auth-names')) return response(enrichStubs.keeper)
    const endpoint = providerEndpoints.find(item => url.includes(`/v0/management/${item}`))
    if (endpoint) return response({ [endpoint]: [] })
    return response({})
  }))
  const user = userEvent.setup()
  render(<AuditPanel />)
  const row = await screen.findByRole('button', { name: /prod-deepseek/ })
  // 收起态摘要 chips：对象显示别名为主、尾号 ref 为次。
  expect(row.textContent).toContain('prod-deepseek')
  expect(row.textContent).toContain('key:••••8f2a')
  expect(row.textContent).toContain('供应商 +1 −1')
  expect(row.textContent).toContain('认证 +1')
  await user.click(row)
  const expanded = screen.getByRole('button', { name: /prod-deepseek/ })
  expect(expanded).toHaveAttribute('aria-expanded', 'true')
  // 总开关 关→开；供应商增删行：快照名/原始 ID 兜底；认证走实时富化的 Keeper 名称。
  expect(screen.getByText('渠道定向总开关', { selector: 'h4' })).toBeInTheDocument()
  expect(screen.getAllByText('关').length).toBeGreaterThan(0)
  expect(screen.getByText('火山方舟')).toBeInTheDocument()
  expect(screen.getByText('openai-compatible-siliconflow')).toBeInTheDocument()
  expect(screen.getByText('openai-compatible-deepseek')).toBeInTheDocument()
  expect(await screen.findByText('Codex 主力')).toBeInTheDocument()
  expect(screen.getByText(/操作 ID/)).toBeInTheDocument()
  expect(screen.getByText(/耗时 458 ms/)).toBeInTheDocument()
  await user.click(expanded)
  expect(screen.getByRole('button', { name: /prod-deepseek/ })).toHaveAttribute('aria-expanded', 'false')
})

it('富化请求全部失败时静默降级为原始 ID，不阻塞渲染', async () => {
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    if (url.includes('/audit')) return response(page([keyOperation]))
    throw new Error('network down')
  }))
  render(<AuditPanel />)
  const row = await screen.findByRole('button', { name: /prod-deepseek/ })
  expect(row.textContent).toContain('供应商 +1 −1')
  await userEvent.setup().click(row)
  // 事件快照命中 '火山方舟'，未命中的认证在富化失败后显示原始 ID。
  expect(screen.getByText('火山方舟')).toBeInTheDocument()
  expect(await screen.findByText('codex:auth1')).toBeInTheDocument()
})

it('v1 历史项按 object_type 推导模块并回落 object_ref，规则段渲染行级 diff', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => response(page([v1Operation, rulesOperation]))))
  const user = userEvent.setup()
  render(<AuditPanel />)
  const v1Row = await screen.findByRole('button', { name: /sha256:ca97/ })
  expect(v1Row.textContent).toContain('Key 绑定')
  expect(v1Row.textContent).not.toContain('undefined')
  await user.click(screen.getByRole('button', { name: /^13:58:47/ }))
  // 通用字段走 JSON/文本兜底，规则段走行级 diff（宿主转义已由 api 层还原）。
  await screen.findByText(/rules\.claude/)
  const ruleDiff = document.querySelector('.audit-rule-diff')
  expect(ruleDiff).not.toBeNull()
  expect(ruleDiff!.textContent).toContain('+ c=>e')
  expect(ruleDiff!.textContent).toContain('− c=>d')
  expect(ruleDiff!.textContent).toContain('a=>b')
})

it('脱敏值显示专属样式，失败项行内展示错误文案映射', async () => {
  const redacted = {
    ...keyOperation, operation_id: 'op-red', object_label: undefined,
    changes: { alias: { before: '[REDACTED]', after: '[REDACTED]' } }, labels: undefined,
  }
  const failed = {
    ...rulesOperation, operation_id: 'op-fail', outcome: 'failed', changed: false,
    error_code: 'invalid_request', changes: {},
  }
  vi.stubGlobal('fetch', vi.fn(async () => response(page([redacted, failed]))))
  render(<AuditPanel />)
  const redRow = await screen.findByRole('button', { name: /sha256|key:/ })
  expect(redRow.textContent).toContain('别名')
  expect(redRow.textContent).toContain('已脱敏')
  await userEvent.setup().click(redRow)
  expect(screen.getByText('变更前：已脱敏')).toBeInTheDocument()
  expect(screen.getByText('变更后：已脱敏')).toBeInTheDocument()
  expect(screen.getByText('错误：参数非法')).toBeInTheDocument()
})

it('S10 默认服务端日期、分页与迟到响应隔离保留', async () => {
  const requests: string[] = []
  let resolveOld!: (value: ReturnType<typeof response>) => void
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    requests.push(url)
    if (url.includes('date=2026-09-11')) return new Promise<ReturnType<typeof response>>(resolve => { resolveOld = resolve })
    if (url.includes('date=2026-09-10')) return response(page([], { date: '2026-09-10', total: 0 }))
    if (url.includes('page=2')) return response(page([{ ...keyOperation, operation_id: 'op-two', object_label: 'second-page-object' }], { page: 2, total: 21 }))
    return response(page([keyOperation], { total: 21 }))
  }))
  render(<AuditPanel />)
  expect(await screen.findByLabelText('审计日期')).toHaveValue('2026-09-13')
  expect(requests[0]).not.toContain('date=')
  fireEvent.change(screen.getByLabelText('审计日期'), { target: { value: '2026-09-11' } })
  fireEvent.change(screen.getByLabelText('审计日期'), { target: { value: '2026-09-10' } })
  expect(await screen.findByText('当天暂无操作记录')).toBeInTheDocument()
  await act(async () => resolveOld(response(page([keyOperation], { date: '2026-09-11' }))))
  expect(screen.getByLabelText('审计日期')).toHaveValue('2026-09-10')
  expect(screen.queryByRole('button', { name: /prod-deepseek/ })).not.toBeInTheDocument()
  fireEvent.change(screen.getByLabelText('审计日期'), { target: { value: '2026-09-13' } })
  await screen.findByRole('button', { name: /prod-deepseek/ })
  await userEvent.setup().click(screen.getByRole('button', { name: '下一页' }))
  expect(await screen.findByRole('button', { name: /second-page-object/ })).toBeInTheDocument()
  expect(requests.at(-1)).toContain('date=2026-09-13&page=2&page_size=20')
})

it('outcome 语义与损坏行警告可辨认；读取失败与仅损坏数据不伪报空日', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => response(page(
    ['running', 'unknown', 'failed', 'succeeded'].map((outcome, index) => ({
      ...rulesOperation, operation_id: `op-${index}`, outcome,
      changed: index < 2 ? null : false, changes: {}, error_code: index === 2 ? 'invalid_rule' : undefined,
    })),
    { warnings: ['line 8: truncated_line'] }))))
  render(<AuditPanel />)
  for (const label of ['进行中', '结果未确认', '失败', '成功']) expect(await screen.findByText(label)).toBeInTheDocument()
  expect(screen.getByRole('alert')).toHaveTextContent('line 8: truncated_line')
  const failedRow = document.querySelector('[data-operation-id="op-2"]')!
  expect(failedRow.textContent).toContain('错误：规则非法')
  await userEvent.setup().click(failedRow)
  expect(screen.getByText('无变化（changed=false）')).toBeInTheDocument()
  const unknownRow = document.querySelector('[data-operation-id="op-1"]')!
  await userEvent.setup().click(unknownRow)
  expect(screen.getByText('变更未确认')).toBeInTheDocument()
})

it.each([false, true])('读取失败与只有损坏数据都不伪报正常空日：%s', async warning => {
  vi.stubGlobal('fetch', vi.fn(async () => warning
    ? response(page([], { total: 0, warnings: ['line 1: invalid_event'] }))
    : response({ error: 'audit_unavailable' }, 503)))
  render(<AuditPanel />)
  expect(await screen.findByRole('alert')).toHaveTextContent(warning ? 'invalid_event' : 'audit_unavailable')
  expect(screen.queryByText('当天暂无操作记录')).not.toBeInTheDocument()
})

it('展示层纯函数：数组 diff、行级 diff 与名称解析优先级', () => {
  expect(arrayDiff(['a', 'b'], ['b', 'c'])).toEqual({ added: ['c'], removed: ['a'], kept: ['b'] })
  expect(diffLines('a\nb\nc', 'a\nc\nd').map(line => `${line.type}:${line.text}`)).toEqual(['keep:a', 'del:b', 'keep:c', 'add:d'])
  expect(resolveName('x', { x: '快照' }, { x: '实时' })).toEqual({ name: '快照', source: 'snapshot' })
  expect(resolveName('x', undefined, { x: '实时' })).toEqual({ name: '实时', source: 'live' })
  expect(resolveName('x')).toEqual({ source: 'raw' })
  const chips = summarizeChanges({
    blocked: { before: false, after: true },
    alias: { before: 'a', after: '新名字' },
  })
  expect(chips[0].parts.map(part => part.text).join('')).toBe('封禁 关→开')
  expect(chips[0].parts[3].tone).toBe('neg')
  expect(chips[1].parts[0].text).toBe('别名 →「新名字」')
})
