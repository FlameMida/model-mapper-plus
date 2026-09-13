// T11 步骤 1 测试（任务文件给定，未改动）。
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import NotificationScheduleEditor from './NotificationScheduleEditor'
import type { NotificationSchedule } from '../notifications'

describe('NotificationScheduleEditor', () => {
  it('switching kind swaps exactly the spec fields', async () => {
    const onChange = vi.fn()
    render(<NotificationScheduleEditor value={{ kind: 'interval', interval: 86400, time: '' }} onChange={onChange} />)
    await userEvent.click(screen.getByText('每隔').closest('.semi-select') ?? screen.getByText('每隔'))
    await userEvent.click(screen.getByText('每月'))
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ kind: 'monthly' }))
  })

  it('monthly shows only month-start/end select plus time picker', () => {
    render(<NotificationScheduleEditor value={{ kind: 'monthly', month_end: false, time: '09:00:00' }} onChange={() => {}} />)
    expect(screen.getByText('月初')).toBeInTheDocument()
    expect(screen.queryByText('月份')).not.toBeInTheDocument()
    expect(screen.getByDisplayValue('09:00:00')).toBeInTheDocument()
  })

  it('yearly shows month, day and seconds time picker', () => {
    render(<NotificationScheduleEditor value={{ kind: 'yearly', month: 2, day: 29, time: '09:00:00' }} onChange={() => {}} />)
    expect(screen.getByDisplayValue('2')).toBeInTheDocument()
    expect(screen.getByDisplayValue('29')).toBeInTheDocument()
  })
})
