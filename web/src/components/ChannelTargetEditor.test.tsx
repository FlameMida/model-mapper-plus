import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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

  it('双区块混选回显', () => {
    render(<ChannelTargetEditor
      value={VALUE}
      authFiles={FILES}
      loading={false}
      error=""
      onChange={vi.fn()}
      onRetry={vi.fn()}
    />)

    expect(screen.getByLabelText('供应商 claude')).toBeChecked()
    expect(screen.getByLabelText('供应商 gemini')).not.toBeChecked()
    expect(screen.getByLabelText('认证文件 gemini-main')).toBeChecked()
    expect(screen.getByLabelText('认证文件 claude-main')).not.toBeChecked()
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
    expect(screen.getByLabelText('认证文件 gemini-main')).toBeDisabled()
    expect(screen.getByLabelText('供应商 claude')).toBeChecked()
    expect(screen.getByLabelText('认证文件 gemini-main')).toBeChecked()
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

    await user.click(screen.getByLabelText('全选 gemini'))
    expect(onChange).toHaveBeenCalledWith({
      enabled: true,
      suppliers: [],
      auth_ids: ['gemini-backup', 'gemini-main'],
    })
  })
})
