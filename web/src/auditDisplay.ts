import type { AuditChange, AuditOperation } from './api'

// 审计面板的展示层纯函数：模块归类、动词/错误文案、变更摘要 chips、
// 行级 diff 与数组 diff、脱敏识别与渠道名称解析。仅服务 AuditPanel，不发起请求。

export interface DiffLine {
  type: 'add' | 'del' | 'keep'
  text: string
}

export interface ChipPart {
  text: string
  tone?: 'pos' | 'neg' | 'mute'
}

export interface AuditChip {
  key: string
  parts: ChipPart[]
}

export const AUDIT_MODULES = ['rules', 'key_binding', 'notifications', 'keeper_auth_name'] as const

const MODULE_META: Record<string, { label: string; color: 'blue' | 'cyan' | 'orange' | 'green' | 'grey' }> = {
  rules: { label: '全局规则', color: 'blue' },
  key_binding: { label: 'Key 绑定', color: 'cyan' },
  notifications: { label: '通知', color: 'orange' },
  keeper_auth_name: { label: 'Keeper 名称', color: 'green' },
  other: { label: '其他', color: 'grey' },
}

export function moduleMeta(module: string) {
  return MODULE_META[module] ?? MODULE_META.other
}

/** v2 事件自带 module；v1 历史按 object_type 推导。 */
export function moduleOf(item: Pick<AuditOperation, 'module' | 'object_type'>): string {
  if (item.module) return item.module
  switch (item.object_type) {
    case 'rules': return 'rules'
    case 'key_binding': return 'key_binding'
    case 'keeper_auth_name': return 'keeper_auth_name'
    default: return 'other'
  }
}

const ACTION_LABELS: Record<string, string> = {
  create: '新增', update: '更新', delete: '删除', sync: '同步', test_send: '测试发送',
}

export function actionLabel(action: string): string {
  return ACTION_LABELS[action] ?? action
}

const ERROR_LABELS: Record<string, string> = {
  invalid_request: '参数非法',
  not_found: '目标不存在',
  state_write_failed: '状态写入失败',
  audit_write_failed: '审计写入失败',
  invalid_rule: '规则非法',
  store_unavailable: '通知存储不可用',
}

export function errorText(code?: string): string {
  if (!code) return ''
  return ERROR_LABELS[code] ?? code
}

const FIELD_LABELS: Record<string, string> = {
  alias: '别名',
  enabled: '启用',
  blocked: '封禁',
  fast_allowed: '快速通道',
  'rules.global': '全局段',
  'rules.claude': 'Claude 段',
  'rules.codex': 'Codex 段',
  'rules.openai': 'OpenAI 段',
  'channel_target.enabled': '渠道定向总开关',
  'channel_target.suppliers': '供应商',
  'channel_target.auth_ids': '认证',
  notifications: 'Key 级通知',
  'notifications.enabled': '通知总开关',
  'notifications.global_default.enabled': '默认通知开关',
  'notifications.global_default.modules': '统计模块',
  'notifications.global_default.schedule': '发送计划',
  'notifications.global_default.platforms': '接收平台',
}

export function fieldLabel(field: string): string {
  return FIELD_LABELS[field] ?? field
}

/** blocked 的「开」是负向语义，单独反转配色。 */
function boolTone(field: string, on: boolean): ChipPart['tone'] {
  if (!on) return 'mute'
  return field === 'blocked' ? 'neg' : 'pos'
}

export function formatValue(value: unknown): string {
  return typeof value === 'string' ? value : JSON.stringify(value, null, 2) ?? '未记录'
}

export function isRedacted(value: unknown): boolean {
  return typeof value === 'string' && value.includes('[REDACTED]')
}

function toLines(value: unknown): string[] {
  if (typeof value === 'string') return value.split('\n')
  if (value == null) return []
  return [formatValue(value)]
}

function toStringArray(value: unknown): string[] {
  return Array.isArray(value) ? value.map(item => String(item)) : []
}

/** 行级 LCS diff；规则段与平台/模块列表行数有限，O(n·m) 足够。 */
export function diffLines(before: unknown, after: unknown): DiffLine[] {
  const a = toLines(before)
  const b = toLines(after)
  const n = a.length
  const m = b.length
  const dp: number[][] = Array.from({ length: n + 1 }, () => new Array<number>(m + 1).fill(0))
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      dp[i][j] = a[i] === b[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1])
    }
  }
  const out: DiffLine[] = []
  let i = 0
  let j = 0
  while (i < n && j < m) {
    if (a[i] === b[j]) {
      out.push({ type: 'keep', text: a[i] })
      i++
      j++
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      out.push({ type: 'del', text: a[i] })
      i++
    } else {
      out.push({ type: 'add', text: b[j] })
      j++
    }
  }
  while (i < n) out.push({ type: 'del', text: a[i++] })
  while (j < m) out.push({ type: 'add', text: b[j++] })
  return out
}

export interface ArrayDiff {
  added: string[]
  removed: string[]
  kept: string[]
}

/** 基于集合语义的字符串数组 diff；kept/added 保持 after 顺序、removed 保持 before 顺序。 */
export function arrayDiff(before: unknown, after: unknown): ArrayDiff {
  const a = toStringArray(before)
  const b = toStringArray(after)
  const setA = new Set(a)
  const setB = new Set(b)
  return {
    added: b.filter(x => !setA.has(x)),
    removed: a.filter(x => !setB.has(x)),
    kept: b.filter(x => setA.has(x)),
  }
}

function lineStats(change: AuditChange): { added: number; removed: number } {
  let added = 0
  let removed = 0
  for (const line of diffLines(change.before, change.after)) {
    if (line.type === 'add') added++
    if (line.type === 'del') removed++
  }
  return { added, removed }
}

function truncate(value: string, max = 16): string {
  return value.length > max ? `${value.slice(0, max)}…` : value
}

const REDACTED_PART: ChipPart = { text: '已脱敏', tone: 'mute' }

/** 收起态行内摘要 chips；按字段类型给「+n −n」计数、开关态或别名新值。 */
export function summarizeChanges(changes: Record<string, AuditChange>): AuditChip[] {
  const chips: AuditChip[] = []
  const push = (key: string, parts: ChipPart[]) => chips.push({ key, parts })
  const pushCounts = (key: string, label: string, added: number, removed: number) => {
    const parts: ChipPart[] = [{ text: `${label} ` }]
    if (added) parts.push({ text: `+${added}`, tone: 'pos' })
    if (removed) parts.push({ text: ` −${removed}`, tone: 'neg' })
    push(key, parts)
  }
  for (const [field, change] of Object.entries(changes)) {
    const label = fieldLabel(field)
    if (typeof change.before === 'boolean' || typeof change.after === 'boolean') {
      const before = typeof change.before === 'boolean' ? change.before : null
      const after = typeof change.after === 'boolean' ? change.after : null
      push(field, [
        { text: `${label} ` },
        { text: before == null ? '—' : before ? '开' : '关', tone: before == null ? undefined : boolTone(field, before) },
        { text: '→' },
        { text: after == null ? '—' : after ? '开' : '关', tone: after == null ? undefined : boolTone(field, after) },
      ])
      continue
    }
    if (field === 'alias') {
      if (isRedacted(change.after) || isRedacted(change.before)) {
        push(field, [{ text: `${label} ` }, REDACTED_PART])
      } else if (typeof change.after === 'string') {
        push(field, [{ text: `${label} →「${truncate(change.after)}」` }])
      } else {
        push(field, [{ text: `${label} 已变更` }])
      }
      continue
    }
    if (field.startsWith('rules.') || field === 'notifications.global_default.modules'
      || field === 'notifications.global_default.platforms') {
      const { added, removed } = lineStats(change)
      pushCounts(field, label, added, removed)
      continue
    }
    if (field === 'channel_target.suppliers' || field === 'channel_target.auth_ids') {
      const diff = arrayDiff(change.before, change.after)
      pushCounts(field, label, diff.added.length, diff.removed.length)
      continue
    }
    if (field === 'notifications') {
      const diff = idListDiff(change.before, change.after)
      pushCounts(field, label, diff.added.length, diff.removed.length)
      continue
    }
    if (isRedacted(change.after) || isRedacted(change.before)) {
      push(field, [{ text: `${label} ` }, REDACTED_PART])
      continue
    }
    push(field, [{ text: `${label} 已变更` }])
  }
  return chips
}

export interface NotificationProjection {
  id: string
  name: string
  enabled: boolean
}

function toNotifications(value: unknown): NotificationProjection[] {
  if (!Array.isArray(value)) return []
  const out: NotificationProjection[] = []
  for (const item of value) {
    if (item && typeof item === 'object') {
      const record = item as Record<string, unknown>
      out.push({
        id: String(record.id ?? ''),
        name: String(record.name ?? ''),
        enabled: record.enabled === true,
      })
    }
  }
  return out
}

/** Key 级通知按 id 对齐的前后投影（名称/开关变化在 kept 行内呈现）。 */
export function idListDiff(before: unknown, after: unknown): { added: NotificationProjection[]; removed: NotificationProjection[]; kept: { before: NotificationProjection; after: NotificationProjection }[] } {
  const a = toNotifications(before)
  const b = toNotifications(after)
  const byId = (rows: NotificationProjection[]) => new Map(rows.map(row => [row.id, row]))
  const mapA = byId(a)
  const mapB = byId(b)
  return {
    added: b.filter(row => !mapA.has(row.id)),
    removed: a.filter(row => !mapB.has(row.id)),
    kept: b.filter(row => mapA.has(row.id)).map(row => ({ before: mapA.get(row.id)!, after: row })),
  }
}

export interface ResolvedName {
  name?: string
  source: 'snapshot' | 'live' | 'raw'
}

/** 渠道 ID 显示名：事件写入时快照优先，实时富化次之，原始 ID 兜底。 */
export function resolveName(id: string, snapshot?: Record<string, string>, live?: Record<string, string>): ResolvedName {
  if (snapshot?.[id]) return { name: snapshot[id], source: 'snapshot' }
  if (live?.[id]) return { name: live[id], source: 'live' }
  return { source: 'raw' }
}
