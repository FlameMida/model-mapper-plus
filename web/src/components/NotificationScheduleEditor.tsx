// 通知计划编辑器（T11）。三态：每隔 = 数值+单位（仅折算 interval 秒数）、每月 = 月初/月末、
// 每年 = 月份+日期；日历态一律配 HH:mm:ss 的 TimePicker。kind 切换构造全新对象，不残留
// 上一形态的字段（与 Go NotificationSchedule json tag 对齐：interval/month_end/month/day/time）。
// 月份/日期/单位用原生 <select> 承载受控值；间隔校验沿用 Go 侧 1..31*86400，超界即时提示。
import { InputNumber, Select, TimePicker } from '@douyinfe/semi-ui'
import type { CSSProperties, ReactNode } from 'react'
import type { NotificationSchedule, ScheduleKind } from '../notifications'

const UNITS = [
  { label: '秒', sec: 1 },
  { label: '分', sec: 60 },
  { label: '时', sec: 3600 },
  { label: '天', sec: 86400 },
]
const MAX_INTERVAL_SEC = 31 * 86400

const numberSelectStyle: CSSProperties = {
  fontSize: 12,
  padding: '2px 4px',
  border: '1px solid rgba(28,31,35,.15)',
  borderRadius: 3,
  color: '#1c1f23',
  background: '#fff',
}

/** interval 能被整除的最大单位秒数（用于把总秒数折算成「数值 + 单位」展示）。 */
function pickUnitSec(interval: number): number {
  for (let i = UNITS.length - 1; i >= 0; i--) {
    if (interval % UNITS[i].sec === 0) return UNITS[i].sec
  }
  return 1
}

export default function NotificationScheduleEditor({ value, onChange }: {
  value: NotificationSchedule
  onChange: (s: NotificationSchedule) => void
}) {
  const set = (patch: Partial<NotificationSchedule>) => onChange({ ...value, ...patch })

  const interval = value.kind === 'interval' ? Math.max(1, Math.round(value.interval ?? 1)) : 0
  let intervalBody: ReactNode = null
  if (value.kind === 'interval') {
    const unitSec = pickUnitSec(interval)
    const count = Math.max(1, Math.round(interval / unitSec))
    const outOfRange = interval < 1 || interval > MAX_INTERVAL_SEC
    intervalBody = (
      <>
        <InputNumber
          value={count}
          min={1}
          onChange={(v) => set({ interval: Math.max(1, Number(v) || 1) * unitSec })}
          style={{ width: 80 }}
        />
        <select
          aria-label="间隔单位"
          value={unitSec}
          onChange={(e) => set({ interval: count * Number(e.target.value) })}
          style={numberSelectStyle}
        >
          {UNITS.map((u) => (
            <option key={u.sec} value={u.sec}>
              {u.label}
            </option>
          ))}
        </select>
        {outOfRange && <span style={{ fontSize: 12, color: '#d4552a' }}>间隔需在 1 秒到 31 天之间</span>}
      </>
    )
  }

  return (
    <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
      <Select
        value={value.kind}
        style={{ width: 110 }}
        onChange={(k) => {
          const kind = (Array.isArray(k) ? k[0] : k) as ScheduleKind | undefined
          if (kind) onChange({ kind, time: kind === 'interval' ? '' : value.time })
        }}
        optionList={[
          { value: 'interval', label: '每隔' },
          { value: 'monthly', label: '每月' },
          { value: 'yearly', label: '每年' },
        ]}
      />
      {intervalBody}
      {value.kind === 'monthly' && (
        <Select
          value={value.month_end ? 'end' : 'start'}
          style={{ width: 100 }}
          onChange={(v) => set({ month_end: (Array.isArray(v) ? v[0] : v) === 'end' })}
          optionList={[
            { value: 'start', label: '月初' },
            { value: 'end', label: '月末' },
          ]}
        />
      )}
      {value.kind === 'yearly' && (
        <>
          <select
            aria-label="月份"
            value={value.month ?? 1}
            onChange={(e) => set({ month: Number(e.target.value) })}
            style={numberSelectStyle}
          >
            {Array.from({ length: 12 }, (_, i) => (
              <option key={i + 1} value={i + 1}>
                {i + 1}
              </option>
            ))}
          </select>
          <select
            aria-label="日期"
            value={value.day ?? 1}
            onChange={(e) => set({ day: Number(e.target.value) })}
            style={numberSelectStyle}
          >
            {Array.from({ length: 31 }, (_, i) => (
              <option key={i + 1} value={i + 1}>
                {i + 1}
              </option>
            ))}
          </select>
        </>
      )}
      {value.kind !== 'interval' && (
        <TimePicker
          format="HH:mm:ss"
          value={value.time}
          onChange={(t) => set({ time: typeof t === 'string' ? t : '' })}
        />
      )}
    </div>
  )
}
