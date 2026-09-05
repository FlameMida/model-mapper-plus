import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import ChannelTargetEditor from './ChannelTargetEditor'
import { ChannelTarget, CpaAuthFile } from '../api'

const FILES: CpaAuthFile[] = [
  { id: 'claude-main', provider: 'claude', status: 'active', disabled: false, label: 'Claude Main' },
  { id: 'gemini-main', provider: 'gemini', status: 'active', disabled: false, label: 'Gemini Main' },
  { id: 'gemini-backup', provider: 'gemini', status: 'disabled', disabled: true, label: 'Gemini Backup' },
]

const VALUE: ChannelTarget = {
  enabled: true,
  suppliers: ['claude'],
  auth_ids: ['gemini-main'],
}

describe('ChannelTargetEditor', () => {
  it('provider 与 auth ID 规范化一致', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<ChannelTargetEditor
      value={{ enabled: true, suppliers: [' Gemini ', 'gemini'], auth_ids: [' f1 ', 'f1'] }}
      authFiles={[
        { id: 'f1', provider: 'gemini', status: 'active', disabled: false, label: 'lower' },
        { id: 'F1', provider: 'GEMINI', status: 'active', disabled: false, label: 'upper' },
      ]}
      loading={false}
      error=""
      onChange={onChange}
      onRetry={vi.fn()}
    />)

    expect(screen.getAllByLabelText(/^供应商 /)).toHaveLength(1)
    expect(screen.getByLabelText('供应商 Gemini')).toBeChecked()
    expect(screen.getByLabelText('认证文件 f1')).toBeChecked()
    expect(screen.getByLabelText('认证文件 F1')).not.toBeChecked()
    expect(screen.queryByText('已保存但 CPA 当前未返回：')).not.toBeInTheDocument()

    await user.click(screen.getByLabelText('认证文件 f1'))
    expect(onChange).toHaveBeenLastCalledWith({
      enabled: true,
      suppliers: ['Gemini'],
      auth_ids: [],
    })
  })

  it('B 版导航切换保留供应商与认证文件混选回显', async () => {
    const user = userEvent.setup()
    render(<ChannelTargetEditor
      value={VALUE}
      authFiles={FILES}
      loading={false}
      error=""
      onChange={vi.fn()}
      onRetry={vi.fn()}
    />)

    expect(screen.getByLabelText('供应商 claude')).toBeChecked()
    expect(screen.getByRole('button', { name: '查看供应商 claude' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.queryByLabelText('认证文件 gemini-main')).not.toBeInTheDocument()
    expect(screen.getByLabelText('认证文件 claude-main')).not.toBeChecked()
    await user.click(screen.getByRole('button', { name: '查看供应商 gemini' }))
    expect(screen.getByLabelText('供应商 gemini')).not.toBeChecked()
    expect(screen.getByLabelText('认证文件 gemini-main')).toBeChecked()
    expect(screen.queryByLabelText('认证文件 claude-main')).not.toBeInTheDocument()
  })

  it('总开关关闭置灰', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    const { rerender } = render(<ChannelTargetEditor
      value={VALUE}
      authFiles={FILES}
      loading={false}
      error=""
      onChange={onChange}
      onRetry={vi.fn()}
    />)

    await user.click(screen.getByRole('switch', { name: '渠道定向总开关' }))
    expect(onChange).toHaveBeenCalledWith({ ...VALUE, enabled: false })

    rerender(<ChannelTargetEditor
      value={{ ...VALUE, enabled: false }}
      authFiles={FILES}
      loading={false}
      error=""
      onChange={onChange}
      onRetry={vi.fn()}
    />)
    expect(screen.getByLabelText('供应商 claude')).toBeDisabled()
    expect(screen.getByLabelText('供应商 claude')).toBeChecked()
    await user.click(screen.getByRole('button', { name: '查看供应商 gemini' }))
    expect(screen.getByLabelText('认证文件 gemini-main')).toBeDisabled()
    expect(screen.getByLabelText('认证文件 gemini-main')).toBeChecked()
    expect(screen.getByRole('textbox', { name: '搜索认证文件' })).toBeDisabled()
  })

  it('认证文件组头全选只更新该组', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<ChannelTargetEditor
      value={{ enabled: true, suppliers: [], auth_ids: [] }}
      authFiles={FILES}
      loading={false}
      error=""
      onChange={onChange}
      onRetry={vi.fn()}
    />)

    await user.click(screen.getByRole('button', { name: '查看供应商 gemini' }))
    await user.click(screen.getByLabelText('全选 gemini'))
    expect(onChange).toHaveBeenCalledWith({
      enabled: true,
      suppliers: [],
      auth_ids: ['gemini-backup', 'gemini-main'],
    })
  })

  it('搜索后批选仅更新可见结果，保留跨供应商与未返回的 ID', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    function Editor() {
      const [value, setValue] = useState<ChannelTarget>({
        enabled: true, suppliers: ['claude'], auth_ids: ['claude-main', 'gemini-main', 'missing'],
      })
      return <ChannelTargetEditor value={value} authFiles={FILES} loading={false} error=""
        onRetry={vi.fn()} onChange={(next) => { onChange(next); setValue(next) }} />
    }
    render(<Editor />)
    await user.click(screen.getByRole('button', { name: '查看供应商 gemini' }))
    await user.type(screen.getByRole('textbox', { name: '搜索认证文件' }), 'BACKUP')
    expect(screen.queryByLabelText('认证文件 gemini-main')).not.toBeInTheDocument()
    await user.click(screen.getByLabelText('全选 gemini'))
    expect(onChange).toHaveBeenLastCalledWith({
      enabled: true, suppliers: ['claude'], auth_ids: ['claude-main', 'gemini-backup', 'gemini-main', 'missing'],
    })
    await user.click(screen.getByLabelText('全选 gemini'))
    expect(onChange).toHaveBeenLastCalledWith({
      enabled: true, suppliers: ['claude'], auth_ids: ['claude-main', 'gemini-main', 'missing'],
    })
    await user.click(screen.getByRole('button', { name: '查看供应商 claude' }))
    expect(screen.getByRole('textbox', { name: '搜索认证文件' })).toHaveValue('')
    expect(screen.getByLabelText('认证文件 claude-main')).toBeChecked()
    await user.click(screen.getByLabelText('供应商 claude'))
    expect(onChange).toHaveBeenLastCalledWith({
      enabled: true, suppliers: [], auth_ids: ['claude-main', 'gemini-main', 'missing'],
    })
  })

  it('搜索无结果时禁用批选，目录刷新移除当前供应商时回退到有效供应商', async () => {
    const user = userEvent.setup()
    const props = { value: { enabled: true, suppliers: [], auth_ids: [] }, loading: false, error: '', onChange: vi.fn(), onRetry: vi.fn() }
    const { rerender } = render(<ChannelTargetEditor {...props} authFiles={FILES} />)
    await user.type(screen.getByRole('textbox', { name: '搜索认证文件' }), 'not-present')
    expect(screen.getByText('没有匹配的认证文件')).toBeInTheDocument()
    expect(screen.getByLabelText('全选 claude')).toBeDisabled()
    await user.clear(screen.getByRole('textbox', { name: '搜索认证文件' }))
    await user.click(screen.getByRole('button', { name: '查看供应商 gemini' }))
    rerender(<ChannelTargetEditor {...props} authFiles={[FILES[0]]} />)
    expect(screen.getByRole('button', { name: '查看供应商 claude' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByLabelText('认证文件 claude-main')).toBeInTheDocument()
  })

  it('加载与失败保留已选项，失败提供可重试提示', async () => {
    const user = userEvent.setup()
    const props = { value: VALUE, authFiles: [], onChange: vi.fn(), onRetry: vi.fn() }
    const { rerender } = render(<ChannelTargetEditor {...props} loading error="" />)
    expect(screen.getByRole('status')).toHaveTextContent('正在加载认证文件…')
    expect(screen.getByText('gemini-main')).toBeInTheDocument()
    expect(screen.queryByText('已保存但 CPA 当前未返回：')).not.toBeInTheDocument()
    rerender(<ChannelTargetEditor {...props} loading={false} error="HTTP 503" />)
    expect(screen.getByText('认证文件加载失败，已选配置已保留。')).toBeInTheDocument()
    expect(screen.getByText('HTTP 503')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '重新加载' }))
    expect(props.onRetry).toHaveBeenCalledOnce()
    expect(props.onChange).not.toHaveBeenCalled()
  })
})
