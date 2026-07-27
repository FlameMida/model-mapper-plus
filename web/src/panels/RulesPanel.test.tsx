import { describe, it, expect, vi, afterEach } from 'vitest'
import { useState } from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import RulesPanel from './RulesPanel'
import { StateResponse } from '../api'

const EMPTY_STATE: StateResponse = {
  version: 1,
  rules: { global: '', claude: '', codex: '', openai: '' },
  key_bindings: [],
  persisted: true,
  state_file: '/tmp/s.json',
}

// 父组件持有 state，保存后用响应回写 —— 复刻 App.tsx 的真实用法。
function Harness() {
  const [state, setState] = useState<StateResponse>(EMPTY_STATE)
  return <RulesPanel state={state} onSaved={setState} />
}

afterEach(() => {
  vi.unstubAllGlobals()
})

/**
 * 宿主 CPA 会对插件 management 响应做 html.EscapeString，
 * 保存后回读到的 DSL 是 `a=&gt;b`。桩按真实链路回放这个形态。
 */
function stubSavedState(escapedGlobal: string): void {
  vi.stubGlobal('fetch', vi.fn(async () => ({
    ok: true,
    status: 200,
    text: async () => JSON.stringify({
      ...EMPTY_STATE,
      rules: { ...EMPTY_STATE.rules, global: escapedGlobal },
    }),
  })))
}

describe('RulesPanel：保存后回显', () => {
  it('全局段新增一条映射并保存后，规则行仍在（不被宿主转义吞掉）', async () => {
    const user = userEvent.setup()
    stubSavedState('a=&gt;b')
    render(<Harness />)

    await user.click(screen.getByRole('button', { name: /添加映射/ }))
    await user.type(screen.getByPlaceholderText(/find/), 'a')
    await user.type(screen.getByPlaceholderText(/replace/), 'b')
    await user.click(screen.getByRole('button', { name: '保存' }))

    await waitFor(() => {
      expect((screen.getByPlaceholderText(/find/) as HTMLInputElement).value).toBe('a')
    })
    expect((screen.getByPlaceholderText(/replace/) as HTMLInputElement).value).toBe('b')
    expect(screen.queryByText(/本段为空/)).not.toBeInTheDocument()
  })
})
