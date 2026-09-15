// 2026-09-15：通知 tab 加入后 860 放不下四个 tab；1000 仍挤换行操作列，加宽到 1280。
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
  it('加宽到 1280 使通知表操作列不再换行', async () => {
    const user = userEvent.setup()
    render(<KeysPanel keyOptions={createKeyOptions()} state={STATE} onSaved={vi.fn()} />)
    await user.click(screen.getByRole('button', { name: '编辑' }))
    const modal = await screen.findByText('编辑绑定：Width')
    const modalRoot = modal.closest('.semi-modal')
    expect(modalRoot).not.toBeNull()
    expect(modalRoot).toHaveStyle({ width: '1280px' })
  })
})
