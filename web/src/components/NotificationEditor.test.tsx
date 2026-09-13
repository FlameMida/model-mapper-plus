// T13 步骤 1 测试（任务文件给定；一处授权例外：任务原稿第三例用 vi.doMock + 动态
// import，按任务步骤 4 注记与派发指示改为顶层 vi.mock 工厂 + vi.mocked 逐例桩定）。
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import NotificationEditor from './NotificationEditor'
import { api } from '../api'
import type { Notification } from '../notifications'

vi.mock('../api', () => ({
  api: { notifications: { preview: vi.fn() } },
}))

const draft: Notification = {
  id: 'n1', name: '日报用量', enabled: true,
  template_follows_global: false, schedule_follows_global: false,
  modules: [{ kind: 'daily', period: 'current' }],
  schedule: { kind: 'interval', interval: 86400, time: '' },
  platforms: [{ kind: 'feishu', enabled: true, webhook: 'https://f', user_ids: ['ou_a'] }],
}

function renderEditor(over: Partial<Notification> = {}, names: string[] = []) {
  const onSaved = vi.fn()
  render(<NotificationEditor visible originalName="日报用量" siblingNames={names}
    initial={{ ...draft, ...over }} onSaved={onSaved} onClose={() => {}} />)
  return { onSaved }
}

describe('NotificationEditor', () => {
  it('blocks save when name collides with a sibling', async () => {
    const { onSaved } = renderEditor({ name: '周报用量' }, ['周报用量'])
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByText(/同 Key 内已存在同名通知/)).toBeInTheDocument()
    expect(onSaved).not.toHaveBeenCalled()
  })

  it('blocks save when enabled platform lacks identity', async () => {
    const { onSaved } = renderEditor({
      platforms: [{ kind: 'feishu', enabled: true, webhook: 'https://f' }],
    })
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByText(/用户唯一 ID/)).toBeInTheDocument()
    expect(onSaved).not.toHaveBeenCalled()
  })

  it('shows stale-preview banner after edit and previews saved entity', async () => {
    vi.mocked(api.notifications.preview).mockResolvedValue({ text: '预览文本', warnings: [], bytes: 12 })
    render(<NotificationEditor visible originalName="" siblingNames={[]}
      initial={draft} onSaved={() => {}} onClose={() => {}} />)
    fireEvent.change(screen.getByLabelText('通知名称'), { target: { value: '改名' } })
    expect(screen.getByText(/预览.*过期/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '预览消息' }))
    await waitFor(() => expect(screen.getByText('预览文本')).toBeInTheDocument())
  })

  it('follow-global switches hide module editing', () => {
    renderEditor({ template_follows_global: true })
    // 跟随全局时模块编辑器不渲染（只读提示替代）
    expect(screen.queryByRole('checkbox', { name: '日统计' })).not.toBeInTheDocument()
    expect(screen.getByText(/跟随全局默认模板/)).toBeInTheDocument()
  })
})
