import { createKeyOptions } from '../test/keyOptions'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import KeysPanel from './KeysPanel'
import { api, KeyBinding, listCpaApiKeys, listCpaCredentials, StateResponse } from '../api'

vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>()
  return {
    ...actual,
    api: { ...actual.api, postKey: vi.fn(), patchKey: vi.fn(), deleteKey: vi.fn() },
    listCpaApiKeys: vi.fn().mockResolvedValue([]),
    listCpaCredentials: vi.fn(),
  }
})

const EMPTY_RULES = { global: '', claude: '', codex: '', openai: '' }
const BINDING: KeyBinding = {
  key: 'sk-k',
  alias: 'Target Key',
  enabled: true,
  blocked: false,
  rules: { ...EMPTY_RULES },
  channel_target: { enabled: true, suppliers: ['claude'], auth_ids: ['gemini-main'] },
  fast_allowed: false,
}
const STATE: StateResponse = {
  version: 1,
  rules: { ...EMPTY_RULES },
  key_bindings: [BINDING],
  persisted: true,
}

afterEach(() => {
  vi.clearAllMocks()
  vi.mocked(listCpaApiKeys).mockResolvedValue([])
})

describe('KeysPanel：渠道定向与 Fast', () => {
  it('勾选 AI Providers 凭据后保存 ID，重新打开仍回显且不存配置密钥', async () => {
    const credential = { id: 'codex:apikey:proof', provider: 'codex', source: 'ai-provider' as const,
      label: 'Key ••••1234', status: 'configured', disabled: false, base_url: 'https://proof.example/v1' }
    vi.mocked(listCpaCredentials).mockResolvedValue([credential])
    vi.mocked(api.postKey).mockResolvedValue(STATE)
    const user = userEvent.setup()
    const { unmount } = render(<KeysPanel keyOptions={createKeyOptions()} state={STATE} onSaved={vi.fn()} />)
    await user.click(screen.getByRole('button', { name: '编辑' }))
    await user.click(screen.getByRole('tab', { name: '渠道定向' }))
    await user.click(await screen.findByRole('button', { name: '查看供应商 codex' }))
    await user.click(screen.getByLabelText(`凭据 ${credential.id}`))
    await user.click(screen.getByRole('dialog').querySelector('.semi-modal-footer .semi-button-primary') as HTMLButtonElement)
    await waitFor(() => expect(api.postKey).toHaveBeenCalled())
    const saved = vi.mocked(api.postKey).mock.calls.at(-1)![0]
    expect(saved.channel_target).toEqual({ enabled: true, suppliers: ['claude'], auth_ids: [credential.id, 'gemini-main'] })
    expect(JSON.stringify(saved)).not.toContain('proof.example')
    expect(JSON.stringify(saved)).not.toContain('1234')
    unmount()
    render(<KeysPanel keyOptions={createKeyOptions()} state={{ ...STATE, key_bindings: [saved] }} onSaved={vi.fn()} />)
    await user.click(screen.getByRole('button', { name: '编辑' }))
    await user.click(screen.getByRole('tab', { name: '渠道定向' }))
    await user.click(await screen.findByRole('button', { name: '查看供应商 codex' }))
    expect(screen.getByLabelText(`凭据 ${credential.id}`)).toBeChecked()
  })
  it.each([
    [{ enabled: true }, '0 个供应商 · 0 个凭据'],
    [{ enabled: true, suppliers: ['gemini'] }, '1 个供应商 · 0 个凭据'],
    [{ enabled: true, auth_ids: ['f1'] }, '0 个供应商 · 1 个凭据'],
  ])('合法缺失数组的表格回显：%j', (wireTarget, summary) => {
    vi.mocked(listCpaCredentials).mockResolvedValue([])
    const binding = {
      ...BINDING,
      channel_target: wireTarget as KeyBinding['channel_target'],
    }
    expect(() => render(
      <KeysPanel keyOptions={createKeyOptions()} state={{ ...STATE, key_bindings: [binding] }} onSaved={vi.fn()} />,
    )).not.toThrow()
    expect(screen.getByText(summary)).toBeInTheDocument()
  })

  it('弹窗采用三页并回显 Fast', async () => {
    vi.mocked(listCpaCredentials).mockResolvedValue([
      { id: 'gemini-main', provider: 'gemini', status: 'active', disabled: false, label: 'Gemini Main' },
    ])
    const user = userEvent.setup()
    render(<KeysPanel keyOptions={createKeyOptions()} state={STATE} onSaved={vi.fn()} />)

    await user.click(screen.getByRole('button', { name: '编辑' }))
    expect(screen.getByRole('tab', { name: '基础' })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: '渠道定向' })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: '规则集' })).toBeInTheDocument()
    expect(screen.getByRole('switch', { name: '编辑绑定：Fast 允许' })).not.toBeChecked()
  })

  it('auth-files 加载失败', async () => {
    vi.mocked(listCpaCredentials).mockRejectedValue(new Error('读取 CPA auth-files 失败：HTTP 503'))
    const user = userEvent.setup()
    render(<KeysPanel keyOptions={createKeyOptions()} state={STATE} onSaved={vi.fn()} />)

    await user.click(screen.getByRole('button', { name: '编辑' }))
    await user.click(screen.getByRole('tab', { name: '渠道定向' }))
    expect(await screen.findByText('凭据加载失败，已选配置已保留。')).toBeInTheDocument()
    expect(screen.getByText('读取 CPA auth-files 失败：HTTP 503')).toBeInTheDocument()
    expect(screen.getByText('gemini-main')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '重新加载' }))
    await waitFor(() => expect(listCpaCredentials).toHaveBeenCalledTimes(2))
  })

  it('旧绑定默认 Fast 允许且表格显示定向摘要', async () => {
    vi.mocked(listCpaCredentials).mockResolvedValue([])
    const user = userEvent.setup()
    const oldBinding: KeyBinding = {
      key: 'sk-old', alias: 'Old', enabled: true, blocked: false, rules: { ...EMPTY_RULES },
      channel_target: { enabled: true, suppliers: ['gemini'], auth_ids: ['f1', 'f2'] },
    }
    render(<KeysPanel keyOptions={createKeyOptions()} state={{ ...STATE, key_bindings: [oldBinding] }} onSaved={vi.fn()} />)

    expect(screen.getByText('1 个供应商 · 2 个凭据')).toBeInTheDocument()
    expect(screen.getByText('允许')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '编辑' }))
    expect(screen.getByRole('switch', { name: '编辑绑定：Fast 允许' })).toBeChecked()
  })

  it('不操作渠道页直接保存时提交规范化渠道值', async () => {
    vi.mocked(listCpaCredentials).mockResolvedValue([])
    vi.mocked(api.postKey).mockResolvedValue(STATE)
    const user = userEvent.setup()
    const binding: KeyBinding = {
      ...BINDING,
      channel_target: {
        enabled: true,
        suppliers: [' Gemini ', 'gemini'],
        auth_ids: [' f1 ', 'f1'],
      },
    }
    render(<KeysPanel keyOptions={createKeyOptions()} state={{ ...STATE, key_bindings: [binding] }} onSaved={vi.fn()} />)

    await user.click(screen.getByRole('button', { name: '编辑' }))
    const dialog = screen.getByRole('dialog')
    const okButton = dialog.querySelector('.semi-modal-footer .semi-button-primary') as HTMLButtonElement
    await user.click(okButton)

    await waitFor(() => {
      expect(api.postKey).toHaveBeenCalledWith(expect.objectContaining({
        channel_target: {
          enabled: true,
          suppliers: ['Gemini'],
          auth_ids: ['f1'],
        },
      }))
    })
  })
})
