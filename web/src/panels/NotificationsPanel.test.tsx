import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { Toast } from '@douyinfe/semi-ui'
import NotificationsPanel from './NotificationsPanel'

vi.mock('../api', () => ({
  api: {
    notifications: {
      getStatus: vi.fn().mockResolvedValue({ running: true, revision: 3, pending_jobs: 2, next_fire: '2026-09-14T09:00:00+08:00' }),
      getSettings: vi.fn().mockResolvedValue({
        enabled: true,
        global_default: { id: 'global', name: '用量通知', enabled: true,
          modules: [{ kind: 'daily', period: 'current' }],
          schedule: { kind: 'interval', interval: 86400, time: '09:00:00' },
          platforms: [{ kind: 'feishu', enabled: true, webhook: 'https://f', user_ids: ['ou_a'] }] },
      }),
      putSettings: vi.fn().mockImplementation((s) => Promise.resolve(s)),
      deliveries: vi.fn().mockResolvedValue({ items: [{
        id: 'd1', job_id: 'j1', key_fingerprint: 'fp1', notification_id: 'global',
        platform: 'dingtalk', period_key: 'monthly:2026-08', outcome: 'failed',
        error_code: 'rate_limited', created_at: '2026-08-31T23:59:00Z' }] }),
      retryDelivery: vi.fn().mockResolvedValue({ job_id: 'j2' }),
      fetchMembers: vi.fn(),
    },
  },
}))

// Semi Toast 挂在 document.body 门户上，React cleanup 不会移除，逐用例销毁避免跨用例串扰。
afterEach(() => { Toast.destroyAll() })

describe('NotificationsPanel', () => {
  it('renders service status and global entity editor', async () => {
    render(<NotificationsPanel />)
    expect(await screen.findByText('运行中')).toBeInTheDocument()
    expect(await screen.findByDisplayValue('用量通知')).toBeInTheDocument()
    // 全局通知名称输入框左侧常显标题（不依赖占位符）
    expect(screen.getByText('全局通知名称')).toBeInTheDocument()
    expect(screen.getByText(/待发任务/)).toBeInTheDocument()
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
})
