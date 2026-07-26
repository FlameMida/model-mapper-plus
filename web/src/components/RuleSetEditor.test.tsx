import { describe, it, expect, vi } from 'vitest'
import { useState } from 'react'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import RuleSetEditor from './RuleSetEditor'
import { RuleSet } from '../api'

const EMPTY: RuleSet = { global: '', claude: '', codex: '', openai: '' }

// Semi Button 带 icon（aria-label="plus"），accessible name 为 "plus 添加映射"，用正则匹配。
describe('RuleSetEditor', () => {
  it('点击"添加映射"新增一个空映射行', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<RuleSetEditor value={EMPTY} onChange={onChange} />)

    expect(screen.queryAllByPlaceholderText(/find/)).toHaveLength(0)

    await user.click(screen.getByRole('button', { name: /添加映射/ }))

    expect(screen.getAllByPlaceholderText(/find/)).toHaveLength(1)
    expect(screen.getAllByPlaceholderText(/replace/)).toHaveLength(1)
  })

  it('点击"添加大小写操作"回写 \\a（回归保护）', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<RuleSetEditor value={EMPTY} onChange={onChange} />)

    await user.click(screen.getByRole('button', { name: /添加大小写操作/ }))

    const last = onChange.mock.calls.at(-1)![0] as RuleSet
    expect(last.global).toBe('\\a')
  })

  it('空映射行填入有效内容后，onChange 回写含映射的 DSL', async () => {
    const user = userEvent.setup()
    let captured: RuleSet = EMPTY
    // 受控 Harness：父组件回写 value（贴合 RulesPanel/KeysPanel 真实用法）。
    function Harness() {
      const [value, setValue] = useState<RuleSet>(EMPTY)
      return <RuleSetEditor value={value} onChange={(v) => { setValue(v); captured = v }} />
    }
    render(<Harness />)

    await user.click(screen.getByRole('button', { name: /添加映射/ }))
    await user.type(screen.getByPlaceholderText(/find/), 'gpt-4')
    await user.type(screen.getByPlaceholderText(/replace/), 'claude-opus')

    expect(captured.global).toBe('gpt-4=>claude-opus')
  })

  it('value 外部变化时丢弃本地空行草稿（切 key 场景）', async () => {
    const user = userEvent.setup()
    function Harness({ value }: { value: RuleSet }) {
      return <RuleSetEditor value={value} onChange={vi.fn()} />
    }
    const { rerender } = render(<Harness value={EMPTY} />)

    await user.click(screen.getByRole('button', { name: /添加映射/ }))
    expect(screen.getAllByPlaceholderText(/find/)).toHaveLength(1)

    // 外部重置 value（切换编辑对象）：本地空行草稿应被清除，仅显示外部值解析的行
    const reloaded: RuleSet = { global: 'foo=>bar', claude: '', codex: '', openai: '' }
    rerender(<Harness value={reloaded} />)

    expect(screen.getAllByPlaceholderText(/find/)).toHaveLength(1)
    expect((screen.getByPlaceholderText(/find/) as HTMLInputElement).value).toBe('foo')
  })
})
