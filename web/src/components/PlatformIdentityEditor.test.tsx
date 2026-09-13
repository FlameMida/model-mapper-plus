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
  it('wecom tab shows no-sign note and renders no secret input', async () => {
    render(<PlatformIdentityEditor value={full} onChange={() => {}} />)
    await userEvent.click(screen.getByText('企业微信'))
    expect(screen.getByText('此平台无签名密钥')).toBeInTheDocument()
    expect(screen.queryByLabelText('签名密钥')).not.toBeInTheDocument()
  })

  it('echoes plaintext webhook and secret', () => {
    render(<PlatformIdentityEditor value={full} onChange={() => {}} />)
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
    await userEvent.click(screen.getByRole('switch', { name: /启用飞书/ }))
    // 本地草稿态校验提示（保存拦截由 T13 用 validatePlatforms 兜底）
    expect(await screen.findByText(/用户唯一 ID/)).toBeInTheDocument()
    expect(onChange).toHaveBeenCalled()
  })
})
