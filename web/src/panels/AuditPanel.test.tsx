import { afterEach, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import AuditPanel from './AuditPanel'

const operation = { operation_id: 'op-one', actor: 'management_api', action: 'update', object_type: 'rules', object_ref: 'rules',
  started_at: '2026-09-12T00:03:00+08:00', finished_at: '2026-09-12T00:03:01+08:00', outcome: 'succeeded', changed: true,
  changes: { global: { before: 'old=&gt;model', after: '&lt;script&gt;=&gt;new' } } }
const page = { date: '2026-09-12', timezone: 'Asia/Shanghai', page: 1, page_size: 20, total: 21,
  items: [operation], warnings: [] as string[] }
const response = (body: unknown, status = 200) => ({ ok: status === 200, status, text: async () => JSON.stringify(body) })
afterEach(() => vi.unstubAllGlobals())

it('S10 默认采用服务端北京时间日期，分页、刷新并安全展示差异', async () => {
  const requests: string[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    requests.push(url)
    return response(url.includes('page=2') ? { ...page, page: 2, items: [{ ...operation, operation_id: 'op-two', object_ref: 'second-page' }] } : page)
  }))
  const user = userEvent.setup()
  render(<AuditPanel />)
  expect(await screen.findByLabelText('审计日期')).toHaveValue('2026-09-12')
  expect(requests[0]).not.toContain('date=')
  expect(screen.getByText(/北京时间/)).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: '查看详情' }))
  const details = screen.getByRole('dialog')
  expect(within(details).getByText('old=>model')).toBeInTheDocument()
  expect(within(details).getByText('<script>=>new')).toBeInTheDocument()
  expect(details.querySelector('script')).toBeNull()
  await user.click(within(details).getByRole('button', { name: '关闭' }))
  await user.click(screen.getByRole('button', { name: '下一页' }))
  expect(await screen.findByText(/second-page/)).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: '刷新' }))
  expect(requests.at(-1)).toContain('date=2026-09-12&page=2&page_size=20')
})

it('S10 日期切换隔离迟到响应，空日与异常不混淆', async () => {
  let resolveOld!: (value: ReturnType<typeof response>) => void
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    if (url.includes('date=2026-09-11')) return new Promise<ReturnType<typeof response>>(resolve => { resolveOld = resolve })
    if (url.includes('date=2026-09-10')) return response({ ...page, date: '2026-09-10', items: [], total: 0 })
    return response(page)
  }))
  render(<AuditPanel />)
  await screen.findByText('成功')
  fireEvent.change(screen.getByLabelText('审计日期'), { target: { value: '2026-09-11' } })
  fireEvent.change(screen.getByLabelText('审计日期'), { target: { value: '2026-09-10' } })
  expect(await screen.findByText('当天暂无操作记录')).toBeInTheDocument()
  await act(async () => resolveOld(response({ ...page, date: '2026-09-11' })))
  expect(screen.getByLabelText('审计日期')).toHaveValue('2026-09-10')
  expect(screen.queryByText('成功')).not.toBeInTheDocument()
})

it('S9/S10 running、unknown、failed、无变化和损坏行警告可辨认', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => response({ ...page, total: 4, warnings: ['line 8: truncated_line'],
    items: ['running', 'unknown', 'failed', 'succeeded'].map((outcome, index) => ({ ...operation,
      operation_id: `op-${index}`, outcome, changed: index < 2 ? null : false, changes: {}, error_code: index === 2 ? 'invalid_rule' : undefined })) })))
  render(<AuditPanel />)
  for (const label of ['进行中', '结果未确认', '失败', '成功']) expect(await screen.findByText(label)).toBeInTheDocument()
  expect(screen.getByRole('alert')).toHaveTextContent('line 8: truncated_line')
  await userEvent.setup().click(screen.getAllByRole('button', { name: '查看详情' })[1])
  expect(within(screen.getByRole('dialog')).getByText('变更未确认')).toBeInTheDocument()
  await userEvent.setup().click(within(screen.getByRole('dialog')).getByRole('button', { name: '关闭' }))
  await userEvent.setup().click(screen.getAllByRole('button', { name: '查看详情' })[2])
  expect(within(screen.getByRole('dialog')).getByText('无变化（changed=false）')).toBeInTheDocument()
  expect(within(screen.getByRole('dialog')).getByText('错误：invalid_rule')).toBeInTheDocument()
})

it.each([false, true])('S10 读取失败与只有损坏数据都不伪报正常空日：%s', async warning => {
  vi.stubGlobal('fetch', vi.fn(async () => warning ? response({ ...page, total: 0, items: [], warnings: ['line 1: invalid_event'] }) :
    response({ error: 'audit_unavailable' }, 503)))
  render(<AuditPanel />)
  expect(await screen.findByRole('alert')).toHaveTextContent(warning ? 'invalid_event' : 'audit_unavailable')
  expect(screen.queryByText('当天暂无操作记录')).not.toBeInTheDocument()
})
