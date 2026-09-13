import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import PlatformIdentityEditor, { validatePlatforms } from './PlatformIdentityEditor'
import type { PlatformIdentity } from '../notifications'

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
