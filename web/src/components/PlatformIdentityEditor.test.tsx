import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import PlatformIdentityEditor, { validatePlatforms } from './PlatformIdentityEditor'
import type { PlatformIdentity } from '../notifications'

vi.mock('../api', () => ({
  api: { notifications: { fetchMembers: vi.fn() } },
}))

import { api } from '../api'

const fetchMembers = vi.mocked(api.notifications.fetchMembers)

const full: PlatformIdentity[] = [
  { kind: 'wecom', enabled: false },
  { kind: 'feishu', enabled: true, webhook: 'https://open.feishu.cn/hook/x', user_ids: ['ou_a'], sign_secret: 'sec' },
]

describe('validatePlatforms', () => {
  it('flags enabled platform missing user ids', () => {
    const errs = validatePlatforms([{ kind: 'feishu', enabled: true, webhook: 'https://x' }])
    expect(errs.join()).toContain('用户唯一 ID')
  })
  it('passes full config', () => {
    expect(validatePlatforms(full)).toHaveLength(0)
  })
})

describe('PlatformIdentityEditor', () => {
  it('selects the first platform tab by default; wecom shows no-sign note and no secret input', () => {
    render(<PlatformIdentityEditor value={full} onChange={() => {}} />)
    // 默认打开第一个平台 Tab（企业微信），其余平台面板不渲染（keepDOM=false）
    expect(screen.getByRole('switch', { name: '启用企业微信通知' })).toBeInTheDocument()
    expect(screen.queryByRole('switch', { name: '启用飞书通知' })).not.toBeInTheDocument()
    expect(screen.getByText('此平台无签名密钥')).toBeInTheDocument()
    expect(screen.queryByLabelText('签名密钥')).not.toBeInTheDocument()
  })

  it('shows persistent field labels beside inputs and echoes plaintext webhook and secret', async () => {
    render(<PlatformIdentityEditor value={full} onChange={() => {}} />)
    await userEvent.click(screen.getByText('飞书 · 启用'))
    expect(screen.getByText('Webhook 地址')).toBeInTheDocument()
    expect(screen.getByText('用户唯一 ID')).toBeInTheDocument()
    expect(screen.getByText('签名密钥')).toBeInTheDocument()
    expect((screen.getByLabelText('Webhook 地址') as HTMLInputElement).value).toBe('https://open.feishu.cn/hook/x')
    expect((screen.getByLabelText('签名密钥') as HTMLInputElement).value).toBe('sec')
  })

  it('enabling a platform with missing identity surfaces inline error', async () => {
    const onChange = vi.fn()
    const missing: PlatformIdentity[] = [
      { kind: 'feishu', enabled: false, webhook: 'https://w' },
      { kind: 'wecom', enabled: false },
    ]
    render(<PlatformIdentityEditor value={missing} onChange={onChange} />)
    await userEvent.click(screen.getByText('飞书'))
    await userEvent.click(screen.getByRole('switch', { name: /启用飞书/ }))
    // 本地草稿态校验提示（保存拦截由 T13 用 validatePlatforms 兜底）
    expect(await screen.findByText('启用通知时用户唯一 ID 为必填项')).toBeInTheDocument()
    expect(onChange).toHaveBeenCalled()
  })
})

describe('PlatformIdentityEditor member picker', () => {
  beforeEach(() => {
    fetchMembers.mockReset()
  })

  it('renders per-platform credential fields', async () => {
    render(<PlatformIdentityEditor value={full} onChange={() => {}} />)
    await userEvent.click(screen.getByText('飞书 · 启用'))
    expect(screen.getByLabelText('飞书 App ID')).toBeInTheDocument()
    expect(screen.getByLabelText('飞书 App Secret')).toBeInTheDocument()
  })

  it('fetches members with draft credentials and appends selection without clobbering', async () => {
    fetchMembers.mockResolvedValue({ status: 'ready', members: [{ id: 'ou_b', name: 'Bob' }, { id: 'ou_c', name: 'Carol' }], fetched_at: '2026-09-14T00:00:00Z' })
    const onChange = vi.fn()
    render(<PlatformIdentityEditor value={[{ kind: 'feishu', enabled: false, user_ids: ['ou_a'] }]} onChange={onChange} />)
    await userEvent.click(screen.getByText('飞书'))
    await userEvent.type(screen.getByLabelText('飞书 App ID'), 'cli_x')
    await userEvent.type(screen.getByLabelText('飞书 App Secret'), 'sec')
    await userEvent.click(screen.getByRole('button', { name: '拉取成员' }))

    await waitFor(() => expect(fetchMembers).toHaveBeenCalledWith(
      { platform: 'feishu', credentials: { fetch_app_id: 'cli_x', fetch_app_secret: 'sec' } }))
    await screen.findByText(/已拉取 2 名成员/)

    // 搜索姓名选中 Bob：user_ids 追加而不覆盖已有的 ou_a。
    const combo = screen.getByRole('combobox')
    await userEvent.click(combo)
    await userEvent.type(within(combo).getByRole('textbox'), 'Bob')
    await userEvent.click(await screen.findByRole('option', { name: /Bob · ou_b/ }))
    await waitFor(() => {
      const last = onChange.mock.calls.at(-1)?.[0] as PlatformIdentity[]
      expect(last.find((p) => p.kind === 'feishu')?.user_ids).toEqual(['ou_a', 'ou_b'])
    })
  })

  it('keeps manual IDs as tags when they are absent from fetched members', async () => {
    fetchMembers.mockResolvedValue({ status: 'ready', members: [{ id: 'ou_b', name: 'Bob' }], fetched_at: 't' })
    render(<PlatformIdentityEditor value={[{ kind: 'feishu', enabled: false, user_ids: ['ou_ghost'] }]} onChange={() => {}} />)
    await userEvent.click(screen.getByText('飞书'))
    await userEvent.type(screen.getByLabelText('飞书 App ID'), 'cli_x')
    await userEvent.type(screen.getByLabelText('飞书 App Secret'), 'sec')
    await userEvent.click(screen.getByRole('button', { name: '拉取成员' }))
    await screen.findByText(/已拉取 1 名成员/)
    // 未知 ID 渲染为保留标签（未拉取到成员名），不被拉取结果清掉。
    expect(await screen.findByText(/ou_ghost/)).toBeInTheDocument()
  })

  it('offers a manual-add option when the search matches nothing', async () => {
    fetchMembers.mockResolvedValue({ status: 'ready', members: [], fetched_at: 't' })
    const onChange = vi.fn()
    render(<PlatformIdentityEditor value={[{ kind: 'feishu', enabled: false }]} onChange={onChange} />)
    await userEvent.click(screen.getByText('飞书'))
    await userEvent.type(screen.getByLabelText('飞书 App ID'), 'cli_x')
    await userEvent.type(screen.getByLabelText('飞书 App Secret'), 'sec')
    await userEvent.click(screen.getByRole('button', { name: '拉取成员' }))
    await screen.findByText(/已拉取 0 名成员/)

    const manualCombo = screen.getByRole('combobox')
    await userEvent.click(manualCombo)
    await userEvent.type(within(manualCombo).getByRole('textbox'), 'ou_manual')
    await userEvent.click(await screen.findByRole('option', { name: /添加手动 ID/ }))
    await waitFor(() => {
      const last = onChange.mock.calls.at(-1)?.[0] as PlatformIdentity[]
      expect(last.find((p) => p.kind === 'feishu')?.user_ids).toEqual(['ou_manual'])
    })
  })

  it('surfaces unavailable envelope as inline error without rejecting', async () => {
    fetchMembers.mockResolvedValue({ status: 'unavailable', members: [], error_code: 'authentication_failed', retry_after_seconds: 60 })
    render(<PlatformIdentityEditor value={[{ kind: 'feishu', enabled: false }]} onChange={() => {}} />)
    await userEvent.click(screen.getByText('飞书'))
    await userEvent.type(screen.getByLabelText('飞书 App ID'), 'cli_x')
    await userEvent.type(screen.getByLabelText('飞书 App Secret'), 'wrong')
    await userEvent.click(screen.getByRole('button', { name: '拉取成员' }))
    expect(await screen.findByText(/凭证被拒绝/)).toBeInTheDocument()
  })

  it('rejects the fetch button with incomplete credentials', async () => {
    render(<PlatformIdentityEditor value={[{ kind: 'feishu', enabled: false }]} onChange={() => {}} />)
    await userEvent.click(screen.getByText('飞书'))
    await userEvent.type(screen.getByLabelText('飞书 App ID'), 'cli_x')
    await userEvent.click(screen.getByRole('button', { name: '拉取成员' }))
    expect(await screen.findByText(/凭证不完整/)).toBeInTheDocument()
    expect(fetchMembers).not.toHaveBeenCalled()
  })
})
