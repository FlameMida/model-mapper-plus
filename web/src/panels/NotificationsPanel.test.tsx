import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { Toast } from '@douyinfe/semi-ui'
import { api } from '../api'
import NotificationsPanel from './NotificationsPanel'

vi.mock('../api', () => ({
  api: {
    notifications: {
      getStatus: vi.fn().mockResolvedValue({ running: true, revision: 3, pending_jobs: 2, next_fire: '2026-09-14 09:00:00' }),
      getSettings: vi.fn().mockResolvedValue({
        enabled: true,
        global_default: { id: 'global', name: '用量通知', enabled: true,
          modules: [{ kind: 'daily', period: 'current' }],
          schedule: { kind: 'interval', interval: 86400, time: '09:00:00' },
          platforms: [{ kind: 'feishu', enabled: true, webhook: 'https://f', user_ids: ['ou_a'] }],
          next_fire: '2026-09-15 08:44:47' },
      }),
      putSettings: vi.fn().mockImplementation((s) => Promise.resolve(s)),
      deliveries: vi.fn().mockResolvedValue({ items: [{
        id: 'd1', job_id: 'j1', key_fingerprint: 'fp1', notification_id: 'global',
        platform: 'dingtalk', period_key: 'interval:2026-09-15T17:31:54+08:00', outcome: 'failed',
        error_code: 'rate_limited', created_at: '2026-08-31T23:59:00Z' }] }),
      retryDelivery: vi.fn().mockResolvedValue({ job_id: 'j2' }),
      fetchMembers: vi.fn(),
    },
  },
}))

// Semi Toast 挂在 document.body 门户上，React cleanup 不会移除，逐用例销毁避免跨用例串扰。
afterEach(() => { Toast.destroyAll() })

describe('NotificationsPanel', () => {
  it('renders service status and the global notification list', async () => {
    render(<NotificationsPanel />)
    expect(await screen.findByText('运行中')).toBeInTheDocument()
    // 全局通知列表化（v0.6.0）：条目名在表格中，新增/保存按钮随卡片展示。
    expect(await screen.findByText('用量通知')).toBeInTheDocument()
    expect(screen.getByText('全局通知')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '＋ 新增通知' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存全局通知' })).toBeInTheDocument()
    expect(screen.getByText(/待发任务/)).toBeInTheDocument()
    expect(screen.getByText('2026-09-15 08:44:47')).toBeInTheDocument()
    expect(screen.getByText(/下次触发 2026-09-14 09:00:00/)).toBeInTheDocument()
    expect(await screen.findByText('2026-09-01 07:59:00')).toBeInTheDocument()
    expect(await screen.findByText('interval:2026-09-15 17:31:54')).toBeInTheDocument()
    expect(screen.queryByText('2026-08-31T23:59:00Z')).not.toBeInTheDocument()
    expect(screen.queryByText('interval:2026-09-15T17:31:54+08:00')).not.toBeInTheDocument()
  })

  it('saves global entity via putSettings', async () => {
    render(<NotificationsPanel />)
    const save = await screen.findByRole('button', { name: '保存全局通知' })
    await userEvent.click(save)
    await waitFor(() => expect(screen.getByText(/已保存/)).toBeInTheDocument())
  })

  it('delivery query table exposes retry on failed rows', async () => {
    render(<NotificationsPanel />)
    await userEvent.click(await screen.findByRole('button', { name: '重试' }))
    await waitFor(() => expect(screen.getByText(/已重新入队/)).toBeInTheDocument())
  })

  // 全局列表化后，计划编辑在编辑器内：schedule 为空的旧数据在表格显示 '—'，
  // 打开编辑器时以默认计划（每天 09:00）兜底渲染计划编辑器。
  it('opens the editor with a default schedule when the entry has none', async () => {
    vi.mocked(api.notifications.getSettings).mockResolvedValueOnce({
      enabled: true,
      global_default: { id: 'global', name: '用量通知', enabled: true,
        modules: [{ kind: 'daily', period: 'current' }],
        platforms: [] },
    })
    render(<NotificationsPanel />)
    await screen.findByText('用量通知')
    await userEvent.click(await screen.findByRole('button', { name: '编辑' }))
    expect(await screen.findByText('每隔')).toBeInTheDocument()
    expect(screen.getByLabelText('间隔单位')).toBeInTheDocument()
  })
})

// 2026-09-14 quick-fix：抽屉保存即落库——行内测试发送始终对已保存实体操作。
describe('NotificationsPanel 抽屉保存即落库', () => {
  it('drawer save auto-persists via putSettings', async () => {
    vi.mocked(api.notifications.putSettings).mockClear()
    render(<NotificationsPanel />)
    await screen.findByText('用量通知')
    await userEvent.click(screen.getByRole('button', { name: '编辑' }))
    const name = await screen.findByLabelText('通知名称')
    await userEvent.clear(name)
    await userEvent.type(name, '每日通知')
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(api.notifications.putSettings).toHaveBeenCalledTimes(1))
    const sent = vi.mocked(api.notifications.putSettings).mock.calls[0][0]
    expect(sent.notifications?.[0]?.name).toBe('每日通知')
    await waitFor(() => expect(screen.getByText(/已保存/)).toBeInTheDocument())
  })
})
