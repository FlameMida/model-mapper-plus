// 2026-09-15 quick-fix：通知 tab 加入后 860 宽度放不下四个 tab（通知标签换行），
// key 编辑 Modal 加宽到 1000。
import { createKeyOptions } from '../test/keyOptions'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import KeysPanel from './KeysPanel'
import { StateResponse } from '../api'

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

const STATE: StateResponse = {
  version: 1,
  rules: { global: '', claude: '', codex: '', openai: '' },
  key_bindings: [{ key: 'sk-w', alias: 'Width', enabled: true, blocked: false, rules: { global: '', claude: '', codex: '', openai: '' } }],
  persisted: true,
  state_file: '/tmp/width-test.json',
}

afterEach(() => {
  vi.clearAllMocks()
})

describe('KeysPanel 编辑 Modal 宽度', () => {
  it('加宽到 1000 使通知 tab 不再换行', async () => {
    const user = userEvent.setup()
    render(<KeysPanel keyOptions={createKeyOptions()} state={STATE} onSaved={vi.fn()} />)
    await user.click(screen.getByRole('button', { name: '编辑' }))
    const modal = await screen.findByText('编辑绑定：Width')
    const modalRoot = modal.closest('.semi-modal')
    expect(modalRoot).not.toBeNull()
    expect(modalRoot).toHaveStyle({ width: '1000px' })
  })
})
