import { createKeyOptions } from '../test/keyOptions'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import KeysPanel from './KeysPanel'
import { api, KeyBinding, listCpaApiKeys, listCpaAuthFiles, StateResponse } from '../api'

vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>()
  return {
    ...actual,
    api: { ...actual.api, postKey: vi.fn(), patchKey: vi.fn(), deleteKey: vi.fn() },
    listCpaApiKeys: vi.fn().mockResolvedValue([]),
    listCpaAuthFiles: vi.fn(),
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
  it.each([
    [{ enabled: true }, '0 个供应商 · 0 个认证文件'],
    [{ enabled: true, suppliers: ['gemini'] }, '1 个供应商 · 0 个认证文件'],
    [{ enabled: true, auth_ids: ['f1'] }, '0 个供应商 · 1 个认证文件'],
  ])('合法缺失数组的表格回显：%j', (wireTarget, summary) => {
    vi.mocked(listCpaAuthFiles).mockResolvedValue([])
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
    vi.mocked(listCpaAuthFiles).mockResolvedValue([
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
    vi.mocked(listCpaAuthFiles).mockRejectedValue(new Error('读取 CPA auth-files 失败：HTTP 503'))
    const user = userEvent.setup()
    render(<KeysPanel keyOptions={createKeyOptions()} state={STATE} onSaved={vi.fn()} />)

    await user.click(screen.getByRole('button', { name: '编辑' }))
    await user.click(screen.getByRole('tab', { name: '渠道定向' }))
    expect(await screen.findByText('认证文件加载失败，已选配置已保留。')).toBeInTheDocument()
    expect(screen.getByText('读取 CPA auth-files 失败：HTTP 503')).toBeInTheDocument()
    expect(screen.getByText('gemini-main')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '重新加载' }))
    await waitFor(() => expect(listCpaAuthFiles).toHaveBeenCalledTimes(2))
  })

  it('旧绑定默认 Fast 允许且表格显示定向摘要', async () => {
    vi.mocked(listCpaAuthFiles).mockResolvedValue([])
    const user = userEvent.setup()
    const oldBinding: KeyBinding = {
      key: 'sk-old', alias: 'Old', enabled: true, blocked: false, rules: { ...EMPTY_RULES },
      channel_target: { enabled: true, suppliers: ['gemini'], auth_ids: ['f1', 'f2'] },
    }
    render(<KeysPanel keyOptions={createKeyOptions()} state={{ ...STATE, key_bindings: [oldBinding] }} onSaved={vi.fn()} />)

    expect(screen.getByText('1 个供应商 · 2 个认证文件')).toBeInTheDocument()
    expect(screen.getByText('允许')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '编辑' }))
    expect(screen.getByRole('switch', { name: '编辑绑定：Fast 允许' })).toBeChecked()
  })

  it('不操作渠道页直接保存时提交规范化渠道值', async () => {
    vi.mocked(listCpaAuthFiles).mockResolvedValue([])
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
