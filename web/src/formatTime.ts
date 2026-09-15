// 管理页时间展示：一律东八区 YYYY-MM-DD HH:mm:ss。已是该格式或纯日期则原样返回，
// 避免无时区字符串被 Date 按浏览器本地时区二次换算。
const HUMAN_DATE_TIME = /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/
const HUMAN_DATE = /^\d{4}-\d{2}-\d{2}$/
const SHANGHAI = 'Asia/Shanghai'

function shanghaiParts(date: Date) {
  const parts = Object.fromEntries(
    new Intl.DateTimeFormat('en-US', {
      timeZone: SHANGHAI,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hourCycle: 'h23',
    }).formatToParts(date).map((part) => [part.type, part.value]),
  )
  return {
    date: `${parts.year}-${parts.month}-${parts.day}`,
    time: `${parts.hour}:${parts.minute}:${parts.second}`,
  }
}

export function formatDateTime(value?: string | null): string {
  const raw = value?.trim() ?? ''
  if (!raw) return '—'
  if (HUMAN_DATE_TIME.test(raw) || HUMAN_DATE.test(raw)) return raw
  const date = new Date(raw)
  if (Number.isNaN(date.getTime())) return raw
  const { date: day, time } = shanghaiParts(date)
  return `${day} ${time}`
}

export function formatClock(value?: string | null): string {
  const full = formatDateTime(value)
  if (full === '—' || !full.includes(' ')) return full
  return full.slice(full.lastIndexOf(' ') + 1)
}

// 投递记录 period_key：interval 内嵌 RFC3339，只格式化时刻；daily/monthly 等日历键原样返回。
const RFC3339_IN_PERIOD = /T\d{2}:\d{2}:\d{2}/

export function formatPeriodKey(value?: string | null): string {
  const raw = value?.trim() ?? ''
  if (!raw) return '—'
  const colon = raw.indexOf(':')
  if (colon < 0) return formatDateTime(raw)
  const rest = raw.slice(colon + 1)
  if (!RFC3339_IN_PERIOD.test(rest)) return raw
  return `${raw.slice(0, colon)}:${formatDateTime(rest)}`
}
