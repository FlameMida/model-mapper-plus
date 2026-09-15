// T11 步骤 1 测试（任务文件给定；仅一处例外见第 30 行注释）。
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import NotificationModulesEditor, { reorderModules } from './NotificationModulesEditor'
import type { ModuleConfig } from '../notifications'

const mods: ModuleConfig[] = [
  { kind: 'daily', period: 'current' },
  { kind: 'weekly' },
]

describe('reorderModules', () => {
  it('moves an enabled module and keeps period fields', () => {
    const out = reorderModules(mods, 1, 0)
    expect(out.map((m) => m.kind)).toEqual(['weekly', 'daily'])
    expect(out[1].period).toBe('current')
  })
})

describe('NotificationModulesEditor', () => {
  it('checking a module appends it with default period, unchecking removes it', async () => {
    const onChange = vi.fn()
    render(<NotificationModulesEditor value={mods} onChange={onChange} />)
    await userEvent.click(screen.getByRole('checkbox', { name: '年统计' }))
    expect(onChange).toHaveBeenCalledWith([
      { kind: 'daily', period: 'current' },
      { kind: 'yearly', period: 'current' },
      { kind: 'weekly' },
    ])
    await userEvent.click(screen.getByRole('checkbox', { name: 'Weekly 窗口' }))
    expect(onChange).toHaveBeenLastCalledWith([{ kind: 'daily', period: 'current' }])
  })

  it('stat modules expose period select; window modules do not', () => {
    render(<NotificationModulesEditor value={mods} onChange={() => {}} />)
    expect(screen.getByDisplayValue('本期累计')).toBeInTheDocument()
    // 任务原稿为 getByDisplayValue：testing-library v10 查询失败是 throw 而非返回 null，
    // `??` 兜底分支不可达、用例确定性失败；按作者宽松断言意图改用 queryByDisplayValue
    //（value 无 previous 模块时为 null，走 combobox 计数分支）。偏差已记录 T11-a1 报告。
    expect(screen.queryByDisplayValue('上一完整周期') ?? screen.getAllByRole('combobox').length).toBeTruthy()
    const weeklyRow = screen.getByText('Weekly 窗口').closest('[data-module-row]')
    expect(weeklyRow?.querySelector('select')).toBeNull()
  })
})
