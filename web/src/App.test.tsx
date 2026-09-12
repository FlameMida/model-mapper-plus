import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'

vi.mock('./api', () => ({
  listCpaApiKeys: vi.fn().mockResolvedValue([]),
  api: {
    getKeeperAliases: vi.fn().mockResolvedValue({ status: 'disabled', items: [] }),
    getState: vi.fn(),
    putRules: vi.fn(),
    postKey: vi.fn(),
    patchKey: vi.fn(),
    deleteKey: vi.fn(),
    preview: vi.fn(),
  },
}))
vi.mock('./session', () => ({
  hasKey: () => true,
  setKey: vi.fn(),
  clearKey: vi.fn(),
  getKey: () => 'k',
  onAuthChange: () => () => {},
}))
vi.mock('./panelAuth', () => ({ readPanelAuth: () => null }))
vi.mock('./panels/RulesPanel', () => ({ default: () => null }))
vi.mock('./panels/KeysPanel', () => ({ default: () => null }))
vi.mock('./panels/PreviewPanel', () => ({ default: () => null }))

import App from './App'
import { api, StateResponse } from './api'

describe('App 版本展示', () => {
  beforeEach(() => { vi.clearAllMocks() })

  it('S10 操作审计位于规则试跑之后', async () => {
    vi.mocked(api.getState).mockResolvedValue({ version: 1,
      rules: { global: '', claude: '', codex: '', openai: '' }, key_bindings: [], persisted: true })
    render(<App />)
    await screen.findByText('已连接')
    expect(screen.getByText('规则试跑').compareDocumentPosition(screen.getByText('操作审计')) &
      Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  // Scenario: 前端展示版本号
  it('/state 返回 plugin_version 时 footer 显示 v<version>', async () => {
    vi.mocked(api.getState).mockResolvedValue({
      version: 1,
      rules: { global: '', claude: '', codex: '', openai: '' },
      key_bindings: [],
      persisted: true,
      state_file: '/tmp/s.json',
      plugin_version: '0.0.0-dev.abc1234',
    } satisfies StateResponse)
    render(<App />)
    // 正向守卫：state 已加载（"已连接" Tag 出现），再断言版本段——避免 state=null 时空过
    await waitFor(() => expect(screen.getByText(/已连接/)).toBeInTheDocument())
    expect(screen.getByText(/v0\.0\.0-dev\.abc1234/)).toBeInTheDocument()
  })

  // Scenario: 旧 .so 无该字段时前端容错
  it('旧 .so 无 plugin_version 时不显示版本段且不报错', async () => {
    vi.mocked(api.getState).mockResolvedValue({
      version: 1,
      rules: { global: '', claude: '', codex: '', openai: '' },
      key_bindings: [],
      persisted: true,
      state_file: '/tmp/s.json',
    } satisfies StateResponse)
    render(<App />)
    // 正向守卫：state 已加载（footer 整块渲染），排除 state=null 的假阳性
    await waitFor(() => expect(screen.getByText(/已连接/)).toBeInTheDocument())
    // 锁定展示契约：" · v<数字>" 即版本段；此处应缺失（容错不显示）
    expect(screen.queryByText(/ · v\d/)).not.toBeInTheDocument()
  })
})
