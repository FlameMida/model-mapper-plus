import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'

vi.mock('./api', () => ({
  api: {
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
import { api } from './api'

describe('App 版本展示', () => {
  beforeEach(() => { vi.clearAllMocks() })

  // Scenario: 前端展示版本号
  it('/state 返回 plugin_version 时 footer 显示 v<version>', async () => {
    ;(api.getState as ReturnType<typeof vi.fn>).mockResolvedValue({
      version: 1,
      rules: { global: '', claude: '', codex: '', openai: '' },
      key_bindings: [],
      persisted: true,
      state_file: '/tmp/s.json',
      plugin_version: '0.0.0-dev.abc1234',
    })
    render(<App />)
    await waitFor(() => {
      expect(screen.getByText(/v0\.0\.0-dev\.abc1234/)).toBeInTheDocument()
    })
  })

  // Scenario: 旧 .so 无该字段时前端容错
  it('旧 .so 无 plugin_version 时不显示版本段且不报错', async () => {
    ;(api.getState as ReturnType<typeof vi.fn>).mockResolvedValue({
      version: 1,
      rules: { global: '', claude: '', codex: '', openai: '' },
      key_bindings: [],
      persisted: true,
      state_file: '/tmp/s.json',
    })
    render(<App />)
    await waitFor(() => {
      expect(screen.queryByText(/v0\.0\.0/)).not.toBeInTheDocument()
    })
  })
})
