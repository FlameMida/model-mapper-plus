import { createKeyOptions } from '../test/keyOptions'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import KeysPanel from './KeysPanel'
import { api, KeyBinding, StateResponse } from '../api'

vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>()
  return {
    ...actual,
    api: {
      ...actual.api,
      postKey: vi.fn(),
      patchKey: vi.fn(),
    },
    listCpaApiKeys: vi.fn().mockResolvedValue([]),
  }
})

const EMPTY_RULES = { global: '', claude: '', codex: '', openai: '' }
const BINDING: KeyBinding = {
  key: 'sk-block-test',
  alias: 'Blocked Test',
  enabled: true,
  blocked: false,
  rules: { ...EMPTY_RULES },
}
const STATE: StateResponse = {
  version: 1,
  rules: { ...EMPTY_RULES },
  key_bindings: [BINDING],
  persisted: true,
  state_file: '/tmp/key-access-block-test.json',
}

afterEach(() => {
  vi.clearAllMocks()
})

describe('KeysPanel：禁止访问', () => {
  it('新增绑定默认不禁止访问', async () => {
    const user = userEvent.setup()
    render(<KeysPanel keyOptions={createKeyOptions()} state={STATE} onSaved={vi.fn()} />)

    await user.click(screen.getByRole('button', { name: /新增绑定/ }))

    expect(await screen.findByRole('switch', { name: '编辑绑定：禁止访问' })).not.toBeChecked()
  })

  it('编辑窗打开禁止访问并保存时 POST blocked=true', async () => {
    const user = userEvent.setup()
    vi.mocked(api.postKey).mockResolvedValue({
      ...STATE,
      key_bindings: [{ ...BINDING, blocked: true }],
    })
    render(<KeysPanel keyOptions={createKeyOptions()} state={STATE} onSaved={vi.fn()} />)
    await user.click(screen.getByRole('button', { name: '编辑' }))
    await user.click(await screen.findByRole('switch', { name: '编辑绑定：禁止访问' }))

    const dialog = screen.getByRole('dialog')
    const okButton = dialog.querySelector('.semi-modal-footer .semi-button-primary') as HTMLButtonElement
    await user.click(okButton)

    await waitFor(() => {
      expect(api.postKey).toHaveBeenCalledWith(expect.objectContaining({
        key: 'sk-block-test',
        blocked: true,
      }))
    })
  })

  it('编辑窗关闭禁止访问并保存时 POST blocked=false', async () => {
    const user = userEvent.setup()
    vi.mocked(api.postKey).mockResolvedValue({
      ...STATE,
      key_bindings: [{ ...BINDING, blocked: false }],
    })
    render(<KeysPanel keyOptions={createKeyOptions()} state={{ ...STATE, key_bindings: [{ ...BINDING, blocked: true }] }} onSaved={vi.fn()} />)
    await user.click(screen.getByRole('button', { name: '编辑' }))
    const blockedSwitch = await screen.findByRole('switch', { name: '编辑绑定：禁止访问' })
    expect(blockedSwitch).toBeChecked()
    await user.click(blockedSwitch)

    const dialog = screen.getByRole('dialog')
    const okButton = dialog.querySelector('.semi-modal-footer .semi-button-primary') as HTMLButtonElement
    await user.click(okButton)

    await waitFor(() => {
      expect(api.postKey).toHaveBeenCalledWith(expect.objectContaining({
        key: 'sk-block-test',
        blocked: false,
      }))
    })
  })

  it('列表禁止访问 Switch 直接 PATCH blocked=true', async () => {
    const user = userEvent.setup()
    vi.mocked(api.patchKey).mockResolvedValue({
      ...STATE,
      key_bindings: [{ ...BINDING, blocked: true }],
    })
    render(<KeysPanel keyOptions={createKeyOptions()} state={STATE} onSaved={vi.fn()} />)
    await user.click(screen.getByRole('switch', { name: '禁止访问：Blocked Test' }))

    await waitFor(() => {
      expect(api.patchKey).toHaveBeenCalledWith('sk-block-test', { blocked: true })
    })
  })
})
