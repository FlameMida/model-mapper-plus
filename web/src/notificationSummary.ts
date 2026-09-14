// 通知发送计划摘要（列表列展示）：跟随全局 / 每隔 N 天(秒) [锚点] /
// 月初|月末 / 每年 M 月 D 日，含 HH:MM:SS。
import type { Notification, NotificationSchedule } from './notifications'

export function scheduleSummary(n: Notification): string {
  if (n.schedule_follows_global) return '跟随全局计划'
  return scheduleText(n.schedule)
}

export function scheduleText(s?: NotificationSchedule | null): string {
  if (!s) return '—'
  if (s.kind === 'interval') {
    const span = s.interval && s.interval % 86400 === 0
      ? `每隔 ${s.interval / 86400} 天`
      : `每隔 ${s.interval ?? 0} 秒`
    return s.time ? `${span} ${s.time}` : span
  }
  if (s.kind === 'monthly') {
    return s.month_end ? `月末 ${s.time}` : `每月 ${s.day ?? 1} 日 ${s.time}`
  }
  return `每年 ${s.month ?? 1} 月 ${s.day ?? 1} 日 ${s.time}`
}
