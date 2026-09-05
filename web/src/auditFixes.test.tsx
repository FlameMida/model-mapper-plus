import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'

// api 与 panelAuth 被 mock；session 用真实实现，让 clearKey → onAuthChange
// 的完整回路跑起来（回路本身正是被测对象）。
vi.mock('./api', () => ({
  listCpaApiKeys: vi.fn().mockResolvedValue([]),
  api: {
    getState: vi.fn(),
    getKeeperAliases: vi.fn().mockResolvedValue({ status: 'disabled', items: [] }),
    putRules: vi.fn(),
    postKey: vi.fn(),
    patchKey: vi.fn(),
    deleteKey: vi.fn(),
    preview: vi.fn(),
  },
}))
const readPanelAuth = vi.fn()
vi.mock('./panelAuth', () => ({ readPanelAuth: () => readPanelAuth() }))
vi.mock('./panels/RulesPanel', () => ({ default: () => null }))
vi.mock('./panels/KeysPanel', () => ({ default: () => null }))
vi.mock('./panels/PreviewPanel', () => ({ default: () => null }))

import App from './App'
import { api, StateResponse } from './api'
import { clearKey } from './session'

const OK_STATE: StateResponse = {
  version: 1,
  rules: { global: '', claude: '', codex: '', openai: '' },
  key_bindings: [],
  persisted: true,
  state_file: '/tmp/s.json',
}

describe('审计 #5：面板 key 失效不得触发重试回路', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    clearKey()
  })

  it('失效的面板 key 只尝试一次，随后落回登录表单', async () => {
    readPanelAuth.mockReturnValue({ apiBase: '/', managementKey: 'stale-key' })
    // 复刻 api.ts 在 401 时的行为：clearKey() 后抛错。
    vi.mocked(api.getState).mockImplementation(async () => {
      clearKey()
      throw new Error('认证失败，请重新登录')
    })

    render(<App />)

    // 回路若存在，这 300ms 内会累积几十次请求。
    await new Promise((r) => setTimeout(r, 300))

    expect(vi.mocked(api.getState).mock.calls.length).toBe(1)
    expect(screen.getByPlaceholderText('CPA management key')).toBeInTheDocument()
  })

  it('面板 key 有效时正常进入主界面', async () => {
    readPanelAuth.mockReturnValue({ apiBase: '/', managementKey: 'good-key' })
    vi.mocked(api.getState).mockResolvedValue(OK_STATE)

    render(<App />)

    await waitFor(() => expect(screen.getByText(/已连接/)).toBeInTheDocument())
    expect(vi.mocked(api.getState).mock.calls.length).toBe(1)
  })
})

describe('审计 #19：空 management key 不得进入主界面', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    clearKey()
    readPanelAuth.mockReturnValue(null)
  })

  it('输入为空时登录按钮禁用且不发请求', async () => {
    vi.mocked(api.getState).mockResolvedValue(OK_STATE)
    render(<App />)

    const button = screen.getByRole('button', { name: '登录' })
    expect(button).toBeDisabled()
    expect(vi.mocked(api.getState)).not.toHaveBeenCalled()
  })
})

describe('审计 #7：state_file 被拒收时前端告警', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    clearKey()
    readPanelAuth.mockReturnValue({ apiBase: '/', managementKey: 'good-key' })
  })

  it('load_error 存在时展示横幅与 .corrupt 提示', async () => {
    vi.mocked(api.getState).mockResolvedValue({
      ...OK_STATE,
      persisted: false,
      load_error: 'rules.global: invalid character',
    })
    render(<App />)

    await waitFor(() =>
      expect(screen.getByText(/state_file 未被采用/)).toBeInTheDocument(),
    )
    expect(screen.getByText(/rules\.global: invalid character/)).toBeInTheDocument()
    expect(screen.getByText(/\/tmp\/s\.json\.corrupt/)).toBeInTheDocument()
  })

  it('load_error 缺失时不展示横幅', async () => {
    vi.mocked(api.getState).mockResolvedValue(OK_STATE)
    render(<App />)

    await waitFor(() => expect(screen.getByText(/已连接/)).toBeInTheDocument())
    expect(screen.queryByText(/state_file 未被采用/)).not.toBeInTheDocument()
  })
})
