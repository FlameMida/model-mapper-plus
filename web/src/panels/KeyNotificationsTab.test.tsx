// T14「Key 通知」页签测试。相对任务样例的三处微调（以实际环境为准）：
// 1. userEvent v14 必须先 setup()（任务样例的直接 click 在 v14 不是函数）；
// 2. Semi Modal.confirm 的确认按钮沿用 KeysPanel.delete.test.tsx 的容器查询模式
//    （Semi 弹窗按钮 accessible name 随 icon 命名漂移，role+name 定位不可靠）；
// 3. 展开投递表断言用仅展开表渲染的 period_key（「最近投递」列与展开表都会出现
//    error_code 文本，getByText 单元素断言会因 multiple elements 失败）。
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import KeyNotificationsTab from './KeyNotificationsTab'
import type { KeyBinding } from '../api'

const binding: KeyBinding = {
  key: 'sk-k1', alias: '研发主账号', enabled: true, blocked: false,
  rules: { global: '', claude: '', codex: '', openai: '' },
  notifications: [{
    id: 'n1', name: '日报用量', enabled: true,
    modules: [{ kind: 'daily', period: 'current' }],
    schedule: { kind: 'interval', interval: 86400, time: '' },
    platforms: [{ kind: 'feishu', enabled: true, webhook: 'https://f', user_ids: ['ou_a'] }],
  }],
}

vi.mock('../api', () => ({
  api: {
    notifications: {
      testSend: vi.fn().mockResolvedValue({ delivery_ids: ['d1'] }),
      deliveries: vi.fn().mockResolvedValue({
        items: [{ id: 'd1', job_id: 'j1', key_fingerprint: 'fp', notification_id: 'n1',
          platform: 'feishu', period_key: 'daily:2026-09-13', outcome: 'failed',
          error_code: 'rate_limited', created_at: '2026-09-13T09:00:00Z' }],
      }),
      retryDelivery: vi.fn().mockResolvedValue({ job_id: 'j2' }),
    },
  },
}))

describe('KeyNotificationsTab', () => {
  it('empty state announces the active global notification', () => {
    render(<KeyNotificationsTab binding={{ ...binding, notifications: undefined }}
      siblingNames={[]} onChange={() => {}} globalName="用量通知" globalSchedule="每隔 1 天 09:00" />)
    expect(screen.getByText(/当前按全局默认通知「用量通知」发送/)).toBeInTheDocument()
    expect(screen.getByText(/新增第一条专属通知后/)).toBeInTheDocument()
  })

  it('lists notifications with inline toggle, test-send and expandable deliveries', async () => {
    const user = userEvent.setup()
    render(<KeyNotificationsTab binding={binding} siblingNames={[]} onChange={() => {}} />)
    expect(screen.getByText('日报用量')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '测试发送' }))
    await waitFor(() => expect(screen.getByText('测试发送已受理')).toBeInTheDocument())
    await user.click(screen.getByRole('button', { name: /投递记录/ }))
    expect(await screen.findByText('daily:2026-09-13')).toBeInTheDocument()
    expect(screen.getAllByText('rate_limited').length).toBeGreaterThan(0)
  })

  it('delete removes from list via onChange', async () => {
    const onChange = vi.fn()
    const user = userEvent.setup()
    render(<KeyNotificationsTab binding={binding} siblingNames={[]} onChange={onChange} />)
    await user.click(screen.getByRole('button', { name: '删除' }))
    // Semi Modal.confirm 在测试环境沿用 KeysPanel.delete.test.tsx 的确认按钮定位方式
    await waitFor(() => expect(screen.getByText(/确认删除/)).toBeInTheDocument())
    const modalRoot = screen.getByText(/确认删除/).closest('[role="dialog"], .semi-modal') as HTMLElement
    const okButton = modalRoot.querySelector('.semi-button-primary') as HTMLButtonElement
    await user.click(okButton)
    expect(onChange).toHaveBeenCalledWith([])
  })
})
