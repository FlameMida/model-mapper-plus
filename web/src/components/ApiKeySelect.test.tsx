import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import ApiKeySelect from './ApiKeySelect'
import { buildKeyOptions } from '../keyOptions'
import type { KeyOptionsState } from '../useKeyOptions'

const source = (): KeyOptionsState => ({ options: buildKeyOptions(['sk-a'], [], [{ key: 'sk-a', alias: '生产' }]), keysLoading: false, keysError: '', keeper: { status: 'ready', items: [{ key: 'sk-a', alias: '生产' }], fetched_at: '2026-09-05T00:00:00Z' }, keeperLoading: false, refreshAliases: vi.fn() })

describe('API Key 选择器', () => {
 it('搜索别名后选中完整 Key', async () => {
  const user = userEvent.setup(); const onChange = vi.fn()
  render(<ApiKeySelect source={source()} value="" onChange={onChange} />)
  await user.click(screen.getByRole('combobox'))
  await user.type(screen.getByRole('textbox'), '生产')
  await user.click(await screen.findByRole('option', { name: /生产/ }))
  await waitFor(() => expect(onChange).toHaveBeenCalledWith('sk-a'))
 })
 it('清空选择得到空串，不会变为 undefined 文本', async () => {
  const user = userEvent.setup(); const onChange = vi.fn()
  const { container } = render(<ApiKeySelect source={source()} value="sk-a" onChange={onChange} showClear />)
  await user.hover(screen.getByRole('combobox'))
  const clear = container.querySelector('.semi-select-clear') as HTMLElement
  await user.click(clear)
  await waitFor(() => expect(onChange).toHaveBeenCalledWith(''))
 })
})

it('allowCreate 选择器在刷新后显示最新标签', async () => {
 const initial = source()
 const { rerender } = render(<ApiKeySelect source={initial} value="sk-a" onChange={vi.fn()} allowCreate />)
 expect(screen.getByRole('combobox')).toHaveTextContent('生产')
 rerender(<ApiKeySelect source={{ ...initial, options: buildKeyOptions(['sk-a'], [], [{ key: 'sk-a', alias: '最新' }]) }} value="sk-a" onChange={vi.fn()} allowCreate />)
 await waitFor(() => expect(screen.getByRole('combobox')).toHaveTextContent('最新'))
})
it('允许手填时刷新后的新别名与新增 Key 仍能搜索和选择', async () => {
 const user = userEvent.setup(); const onChange = vi.fn(); const initial = source()
 const { rerender } = render(<ApiKeySelect source={initial} value="sk-a" onChange={onChange} allowCreate />)
 rerender(<ApiKeySelect source={{ ...initial, options: buildKeyOptions(['sk-a', 'sk-b'], [], [{ key: 'sk-a', alias: '最新' }, { key: 'sk-b', alias: '新增' }]) }} value="sk-a" onChange={onChange} allowCreate />)
 await user.click(screen.getByRole('combobox'))
 await user.type(screen.getByRole('textbox'), '新增')
 await user.click(await screen.findByRole('option', { name: /新增.*sk-b/ }))
 await waitFor(() => expect(onChange).toHaveBeenCalledWith('sk-b'))
})
it('仍可创建 CPA 列表以外的完整 Key', async () => {
 const user = userEvent.setup(); const onChange = vi.fn()
 render(<ApiKeySelect source={source()} value="" onChange={onChange} allowCreate />)
 await user.click(screen.getByRole('combobox'))
 await user.type(screen.getByRole('textbox'), 'sk-manual')
 await user.click(await screen.findByRole('option', { name: /sk-manual/ }))
 await waitFor(() => expect(onChange).toHaveBeenCalledWith('sk-manual'))
})
it('手填 Key 是现有 Key 或别名的子串时仍有明确的创建入口', async () => {
 const user = userEvent.setup(); const onChange = vi.fn()
 render(<ApiKeySelect source={{ ...source(), options: buildKeyOptions(['sk-manual-long'], [], []) }} value="" onChange={onChange} allowCreate />)
 await user.click(screen.getByRole('combobox'))
 await user.type(screen.getByRole('textbox'), 'sk-manual')
 await user.click(await screen.findByRole('option', { name: /使用手动 Key：\s*sk-manual/ }))
 await waitFor(() => expect(onChange).toHaveBeenCalledWith('sk-manual'))
})
it('输入无匹配的手动 Key 后回车不会选中旧 Key', async () => {
 const user = userEvent.setup(); const onChange = vi.fn()
 render(<ApiKeySelect source={source()} value="" onChange={onChange} allowCreate />)
 await user.click(screen.getByRole('combobox'))
 await user.type(screen.getByRole('textbox'), 'manual-key-xyz')
 expect(screen.queryByRole('option', { name: /生产/ })).not.toBeInTheDocument()
 // Semi reads the legacy keyCode; user-event does not populate it in jsdom.
 fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter', code: 'Enter', keyCode: 13, which: 13 })
 await waitFor(() => expect(onChange).toHaveBeenCalledWith('manual-key-xyz'))
})
