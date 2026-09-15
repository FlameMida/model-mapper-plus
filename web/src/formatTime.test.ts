import { describe, expect, it } from 'vitest'
import { formatClock, formatDateTime, formatPeriodKey } from './formatTime'

describe('formatDateTime', () => {
  it('把 RFC3339 UTC 转成东八区人类可读时间', () => {
    expect(formatDateTime('2026-08-31T23:59:00Z')).toBe('2026-09-01 07:59:00')
  })

  it('把带偏移的 RFC3339 固定为东八区，并去掉毫秒', () => {
    expect(formatDateTime('2026-09-13T14:32:05.458+08:00')).toBe('2026-09-13 14:32:05')
  })

  it('已是东八区人类格式或纯日期时原样返回，避免无时区二次换算', () => {
    expect(formatDateTime('2026-09-15 08:44:47')).toBe('2026-09-15 08:44:47')
    expect(formatDateTime('2026-09-13')).toBe('2026-09-13')
  })

  it('空值显示破折号，无法解析时保留原文', () => {
    expect(formatDateTime('')).toBe('—')
    expect(formatDateTime(undefined)).toBe('—')
    expect(formatDateTime('garbage')).toBe('garbage')
  })
})

describe('formatClock', () => {
  it('只取东八区时分秒', () => {
    expect(formatClock('2026-09-13T13:58:47+08:00')).toBe('13:58:47')
  })
})

describe('formatPeriodKey', () => {
  it('interval 键保留前缀，只把 RFC3339 改成东八区人类时间', () => {
    expect(formatPeriodKey('interval:2026-09-15T17:31:54+08:00')).toBe('interval:2026-09-15 17:31:54')
  })

  it('日历周期键保持原样，避免把 2026-09 误解析成日期时间', () => {
    expect(formatPeriodKey('monthly:2026-08')).toBe('monthly:2026-08')
    expect(formatPeriodKey('daily:2026-09-13')).toBe('daily:2026-09-13')
    expect(formatPeriodKey('daily:2026-09-13:prev')).toBe('daily:2026-09-13:prev')
    expect(formatPeriodKey('half:2026H2')).toBe('half:2026H2')
    expect(formatPeriodKey('yearly:2026')).toBe('yearly:2026')
  })

  it('空值显示破折号', () => {
    expect(formatPeriodKey('')).toBe('—')
    expect(formatPeriodKey(undefined)).toBe('—')
  })
})
