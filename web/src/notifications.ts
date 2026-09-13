// 通知类型与常量（T10）。与后端 Go 结构体逐字段对齐（json tags 为真源）：
// notification_types.go（Notification/Settings/Schedule/Platform/Module）
// notification_service.go notificationStatus + notification_management.go
// notificationStatusView（Status 视图）、notification_store.go deliveryRecord。

export type PlatformKind = 'wecom' | 'feishu' | 'dingtalk'

export type ModuleKind =
  | 'daily' | 'monthly' | 'half_year' | 'yearly'
  | 'window_5h' | 'weekly' | 'reset_cards'

/** 累计统计模块必须携带 current/previous；窗口模块必须留空。 */
export type PeriodKind = 'current' | 'previous'

export type ScheduleKind = 'interval' | 'monthly' | 'yearly'

export const STAT_MODULE_KINDS: ModuleKind[] = ['daily', 'monthly', 'half_year', 'yearly']
export const WINDOW_MODULE_KINDS: ModuleKind[] = ['window_5h', 'weekly', 'reset_cards']
export const MODULE_KINDS: ModuleKind[] = [...STAT_MODULE_KINDS, ...WINDOW_MODULE_KINDS]

export const MODULE_LABELS: Record<ModuleKind, string> = {
  daily: '日统计',
  monthly: '月统计',
  half_year: '半年统计',
  yearly: '年统计',
  window_5h: '5H 窗口',
  weekly: 'Weekly 窗口',
  reset_cards: '重置卡',
}

export interface ModuleConfig {
  kind: ModuleKind
  period?: PeriodKind
}

export interface NotificationSchedule {
  kind: ScheduleKind
  /** interval：1..31*86400 秒；time 可作为可选发送锚点（HH:MM:SS）。 */
  interval?: number
  month_end?: boolean
  month?: number
  day?: number
  time: string
}

export interface PlatformIdentity {
  kind: PlatformKind
  enabled: boolean
  webhook?: string
  user_ids?: string[]
  /** 仅 DingTalk/Feishu 支持；wecom 留空。 */
  sign_secret?: string
}

/** 一个独立发送单元：模板模块 + 计划 + 平台身份。Key 级通知整体替换全局。 */
export interface Notification {
  id: string
  name: string
  enabled: boolean
  template_follows_global?: boolean
  schedule_follows_global?: boolean
  modules?: ModuleConfig[]
  schedule?: NotificationSchedule | null
  platforms?: PlatformIdentity[]
}

export interface NotificationSettings {
  enabled: boolean
  global_default: Notification
}

/** GET /notifications/status：notificationStatus + notificationStatusView 扩展字段。 */
export interface NotificationStatus {
  running: boolean
  error_code?: string
  revision: number
  pending_jobs: number
  next_fire?: string
  /** 全局默认通知名（无配置时为空串）。 */
  global_name: string
  /** 无运行服务时解释原因的封闭代码（如 unavailable）。 */
  store_error?: string
}

/** deliveries 记录；outcome 非终态（unknown）可被 retry 重投。 */
export interface DeliveryRecord {
  id: string
  job_id: string
  key_fingerprint: string
  notification_id: string
  platform: PlatformKind
  period_key: string
  outcome: 'accepted' | 'failed' | 'unknown'
  error_code?: string
  detail?: string
  created_at: string
}

/** POST /notifications/preview 响应：保存配置的渲染结果，不发送、不含秘密。 */
export interface PreviewResponse {
  text: string
  warnings: string[]
  bytes: number
}
