// T13 步骤 1 测试（任务文件给定；一处授权例外：任务原稿第三例用 vi.doMock + 动态
// import，按任务步骤 4 注记与派发指示改为顶层 vi.mock 工厂 + vi.mocked 逐例桩定）。
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Toast } from '@douyinfe/semi-ui'
import { afterEach, describe, expect, it, vi } from 'vitest'
import NotificationEditor from './NotificationEditor'
import { api } from '../api'
import type { Notification } from '../notifications'

vi.mock('../api', () => ({
  api: { notifications: { preview: vi.fn(), fetchMembers: vi.fn() } },
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

afterEach(() => { Toast.destroyAll() })

describe('NotificationEditor', () => {
  it('blocks save when name collides with a sibling', async () => {
    const { onSaved } = renderEditor({ name: '周报用量' }, ['周报用量'])
    await userEvent.click(screen.getByRole('button', { name: '保存' }))
    // 行内错误与 Toast 弹窗都携带该文案
    expect(await screen.findAllByText(/同 Key 内已存在同名通知/)).not.toHaveLength(0)
    expect(onSaved).not.toHaveBeenCalled()
  })

  it('blocks save when enabled platform lacks identity (Toast + inline, not silent disable)', async () => {
    const toastSpy = vi.spyOn(Toast, 'error').mockReturnValue('toast-id')
    const { onSaved } = renderEditor({
      platforms: [{ kind: 'feishu', enabled: true, webhook: 'https://f' }],
    })
    const save = screen.getByRole('button', { name: '保存' })
    // 2026-09-14 起：保存键不再 disable，点击以 Toast 弹窗提示并切到平台 Tab（行内已有字段错误）
    expect(save).toBeEnabled()
    await userEvent.click(save)
    expect(toastSpy).toHaveBeenCalledWith(expect.stringContaining('feishu'))
    expect(onSaved).not.toHaveBeenCalled()
    toastSpy.mockRestore()
  })

  it('shows stale-preview banner after edit and previews saved entity', async () => {
    vi.mocked(api.notifications.preview).mockResolvedValue({
      text: '预览文本', warnings: [], bytes: 12,
      platforms: [{ kind: 'feishu', text: 'Codex\n用量 1 tokens' }],
    })
    render(<NotificationEditor visible originalName="" siblingNames={[]}
      initial={draft} onSaved={() => {}} onClose={() => {}} />)
    fireEvent.change(screen.getByLabelText('通知名称'), { target: { value: '改名' } })
    expect(screen.getByText(/预览.*过期/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '预览消息' }))
    await waitFor(() => expect(screen.getByText(/用量 1 tokens/)).toBeInTheDocument())
    expect(screen.getByText('飞书 · text')).toBeInTheDocument()
    expect(screen.queryByText('企业微信 · markdown')).not.toBeInTheDocument()
  })

  it('follow-global switches hide module editing', () => {
    renderEditor({ template_follows_global: true })
    // 跟随全局时模块编辑器不渲染（只读提示替代）
    expect(screen.queryByRole('checkbox', { name: '日统计' })).not.toBeInTheDocument()
    expect(screen.getByText(/跟随全局默认模板/)).toBeInTheDocument()
  })

  // 回归：跟随全局创建的通知 schedule 为空，关闭跟随后必须出现计划编辑器
  // （旧实现的空值守卫让编辑器永远不渲染）。
  it('renders schedule editor with a default when unfollowing global without a saved schedule', () => {
    renderEditor({ schedule_follows_global: false, schedule: null })
    expect(screen.getByText('每隔')).toBeInTheDocument()
    expect(screen.getByLabelText('间隔单位')).toBeInTheDocument()
  })
})

// 2026-09-14 quick-fix：字段级错误定位 + 提交 Toast + 发送计划标题 + key 级 at_all 剥离。
describe('NotificationEditor quick-fix', () => {
  it('renders a 发送计划 section label (key scope, not following)', () => {
    renderEditor({})
    expect(screen.getByText('发送计划')).toBeInTheDocument()
  })

  it('renders a 发送计划 section label (global scope)', () => {
    render(<NotificationEditor visible scope="global" originalName="每日通知" siblingNames={[]}
      initial={draft} onSaved={() => {}} onClose={() => {}} />)
    expect(screen.getByText('发送计划')).toBeInTheDocument()
  })

  it('shows the name error under the name field and the modules error under the modules editor', async () => {
    const user = userEvent.setup()
    renderEditor({ name: '', modules: [] })
    // 两个字段错误同时在场且各自定位到自己的字段：名称错误紧跟名称输入框，模块错误紧跟模块编辑器
    const nameErrorEl = screen.getByText('通知名称为必填项')
    expect(nameErrorEl.previousElementSibling?.contains(screen.getByLabelText('通知名称'))).toBe(true)
    const modulesErrorEl = screen.getByText('至少启用一个统计模块')
    expect(modulesErrorEl.previousElementSibling?.textContent).toContain('统计周期')
    await user.type(screen.getByLabelText('通知名称'), '周报用量')
    expect(screen.queryByText('通知名称为必填项')).not.toBeInTheDocument()
    expect(screen.getByText('至少启用一个统计模块')).toBeInTheDocument()
  })

  it('toasts the first error on save click instead of silently disabling', async () => {
    const toastSpy = vi.spyOn(Toast, 'error').mockReturnValue('toast-id')
    const user = userEvent.setup()
    renderEditor({ name: '' })
    const save = screen.getByRole('button', { name: '保存' })
    expect(save).toBeEnabled()
    await user.click(save)
    expect(toastSpy).toHaveBeenCalledWith('通知名称为必填项')
    toastSpy.mockRestore()
  })

  it('switches to the platforms tab when the first error is a platform error', async () => {
    const toastSpy = vi.spyOn(Toast, 'error').mockReturnValue('toast-id')
    const user = userEvent.setup()
    renderEditor({ platforms: [{ kind: 'wecom', enabled: true, webhook: '' }] })
    await user.click(screen.getByRole('button', { name: '保存' }))
    expect(toastSpy).toHaveBeenCalledWith(expect.stringContaining('wecom'))
    expect(await screen.findByText('Webhook 地址为必填项')).toBeVisible()
    toastSpy.mockRestore()
  })

  it('key scope save strips at_all from platforms', async () => {
    const onSaved = vi.fn()
    const user = userEvent.setup()
    render(<NotificationEditor visible scope="key" originalName="" siblingNames={[]}
      initial={{ ...draft, platforms: [{ kind: 'wecom', enabled: true, webhook: 'https://w', user_ids: ['maverick'], at_all: true }] }}
      onSaved={onSaved} onClose={() => {}} />)
    await user.click(screen.getByRole('button', { name: '保存' }))
    expect(onSaved).toHaveBeenCalledTimes(1)
    expect(onSaved.mock.calls[0][0].platforms[0].at_all).toBe(false)
  })

  it('keeps the drawer open when onSaved rejects (auto-persist failure)', async () => {
    const onClose = vi.fn()
    const user = userEvent.setup()
    render(<NotificationEditor visible originalName="日报用量" siblingNames={[]}
      initial={draft} onSaved={() => Promise.reject(new Error('boom'))} onClose={onClose} />)
    await user.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(screen.getByText(/编辑通知/)).toBeInTheDocument())
    expect(onClose).not.toHaveBeenCalled()
  })
})
