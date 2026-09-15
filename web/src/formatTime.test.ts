import { describe, expect, it } from 'vitest'
import { formatClock, formatDateTime } from './formatTime'

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
