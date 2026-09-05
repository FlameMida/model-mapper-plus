import { createKeyOptions } from '../test/keyOptions'
import { describe, it, expect, vi, afterEach } from 'vitest'
import { useState } from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import KeysPanel from './KeysPanel'
import { api, KeyBinding, StateResponse } from '../api'

vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>()
  return {
    ...actual,
    // 只接管删除链路与 CPA api-keys 拉取；其余 api 方法保留真实实现（本用例用不到）。
    api: { ...actual.api, deleteKey: vi.fn() },
    listCpaApiKeys: vi.fn().mockResolvedValue([]),
  }
})

const EMPTY_RULES = { global: '', claude: '', codex: '', openai: '' }

const BINDING: KeyBinding = {
  key: 'sk-del-me',
  alias: '删除测试',
  enabled: true,
  blocked: false,
  rules: { ...EMPTY_RULES },
}

const STATE_WITH_BINDING: StateResponse = {
  version: 1,
  rules: { ...EMPTY_RULES },
  key_bindings: [BINDING],
  persisted: true,
  state_file: '/tmp/s.json',
}

function Harness() {
  const [state, setState] = useState<StateResponse>(STATE_WITH_BINDING)
  return <KeysPanel keyOptions={createKeyOptions()} state={state} onSaved={setState} />
}

afterEach(() => {
  vi.clearAllMocks()
})

/**
 * Semi UI 的 Modal.confirm 是命令式 API，靠 createRoot 把弹窗挂到 document.body。
 * React 19 下 createRoot 不再从 react-dom 默认导出，Semi 找不到就静默不弹 ——
 * 表现为「点删除没反应」（既不弹确认框，也不发请求）。修复 = 入口注入
 * @douyinfe/semi-ui/react19-adapter。测试环境同样需要该注入（见 test/setup.ts）。
 */
describe('KeysPanel：删除绑定', () => {
  it('点删除 → 弹确认框 → 确定 → 调 deleteKey 并从列表移除', async () => {
    const user = userEvent.setup()
    vi.mocked(api.deleteKey).mockResolvedValue({
      ...STATE_WITH_BINDING,
      key_bindings: [],
    })

    render(<Harness />)

    expect(screen.getByText('删除测试')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '删除' }))

    // Modal.confirm 必须真的弹出来（这条断言在未注入 createRoot 时会 timeout = 复现 bug）。
    await waitFor(() => {
      expect(screen.getByText(/确认删除/)).toBeInTheDocument()
    })

    // Semi Modal.confirm 的确认/取消是 icon-only 按钮，accessible name 随 Semi icon
    // 命名而变、不稳；直接在弹窗容器里取 primary 按钮作为「确认」。
    const modalRoot = screen.getByText(/确认删除/).closest('[role="dialog"], .semi-modal') as HTMLElement
    const okButton = modalRoot.querySelector('.semi-button-primary') as HTMLButtonElement
    await user.click(okButton)

    await waitFor(() => expect(api.deleteKey).toHaveBeenCalledWith('sk-del-me'))
    // 确认后表格里的行消失。
    await waitFor(() => {
      expect(screen.queryByText('删除测试')).not.toBeInTheDocument()
    })
  })
})
