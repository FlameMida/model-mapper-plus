import { getKey, clearKey } from './session'
import type {
  DeliveryRecord,
  Notification,
  NotificationMembersResponse,
  NotificationSettings,
  NotificationStatus,
  PlatformFetchCredentials,
  PlatformKind,
  PreviewResponse as NotificationPreviewResponse,
} from './notifications'

export interface AuditMeta {
  operation_id: string
  recorded: boolean
  error_code?: string
}
export interface AuditChange {
  before?: unknown
  after?: unknown
}
export interface AuditOperation {
  operation_id: string
  actor: string
  action: string
  /** v2 事件携带；v1 历史缺省，由 object_type 推导。 */
  module?: string
  object_type: string
  object_ref: string
  /** v2 写入时快照的对象显示名（Key 别名、通知名等）；缺省回落 object_ref。 */
  object_label?: string
  /** v2 渠道 ID → 显示名快照；读取端以此为优先、实时目录富化次之。 */
  labels?: Record<string, string>
  started_at: string
  finished_at?: string
  outcome: 'running' | 'succeeded' | 'failed' | 'unknown'
  changed: boolean | null
  changes: Record<string, AuditChange>
  error_code?: string
}
export interface AuditPage {
  date: string
  timezone: string
  page: number
  page_size: number
  total: number
  /** 回显 module 筛选值；未筛选时缺省。 */
  module?: string
  /** 全日（未筛选）各模块计数。 */
  module_counts?: Record<string, number>
  items: AuditOperation[]
  warnings: string[]
}
export class ManagementAPIError extends Error {
  audit?: AuditMeta
  constructor(message: string, audit?: AuditMeta) {
    super(message)
    this.name = 'ManagementAPIError'
    this.audit = audit
  }
}

export interface RuleSet {
  global: string
  claude: string
  codex: string
  openai: string
}

export interface ChannelTarget {
  enabled: boolean
  suppliers: string[]
  auth_ids: string[]
}

export interface CpaAuthFile {
  id: string
  provider: string
  status: string
  disabled: boolean
  label: string
  source?: 'auth-file' | 'ai-provider'
  provider_label?: string
  base_url?: string
  auth_index?: string
  name?: string
  type?: string
}

export interface KeeperAuthName {
  identity_id: string
  auth_index: string
  alias: string
  display_name: string
}
export interface KeeperAuthNamesResponse {
  status: 'ready' | 'disabled' | 'unavailable'
  items: KeeperAuthName[]
  fetched_at?: string
  error_code?: string
}
export interface KeeperAuthNameUpdateResponse {
  status: 'ready' | 'not_found' | 'invalid' | 'unavailable' | 'unknown'
  item?: KeeperAuthName
  error_code?: string
  audit?: AuditMeta
}

export interface KeyBinding {
  key: string
  alias: string
  enabled: boolean
  blocked: boolean
  rules: RuleSet
  channel_target?: ChannelTarget
  fast_allowed?: boolean
  /** Key 级专属用量通知；为空时回落全局默认通知（state.go KeyBinding.Notifications）。 */
  notifications?: Notification[]
}

export interface KeeperAlias { key: string; alias: string }
export type KeeperAliasesResponse =
  | { status: 'ready'; items: KeeperAlias[]; fetched_at: string }
  | { status: 'disabled'; items: [] }
  | { status: 'unavailable'; items: []; error_code: 'configuration_error' | 'authentication_failed' | 'rate_limited' | 'timeout' | 'invalid_response' | 'connection_failed'; retry_after_seconds?: number }

export interface StateResponse {
  audit?: AuditMeta
  version: number
  rules: RuleSet
  key_bindings: KeyBinding[]
  updated_at?: string
  persisted: boolean
  state_file?: string
  plugin_version?: string
  /** 存在时表示磁盘上的 state_file 被拒收、当前回退到 YAML 种子。 */
  load_error?: string
}

export interface PreviewRequest {
  key?: string
  format: string
  model: string
}

export interface PreviewResponse {
  m1: string
  m2: string
  routed: boolean
  final: string
  mapping_skipped?: boolean
  channel_target?: {
    enabled: boolean
    resolved: Pick<ChannelTarget, 'suppliers' | 'auth_ids'>
  }
}

const PLUGIN_BASE = '/v0/management/plugins/model-mapper-plus'

// CPA 宿主对所有插件 management 响应统一跑 html.EscapeString
// （internal/pluginhost/management.go → htmlsanitize.JSONBody），JSON 里每个字符串
// 值的 & ' < > " 都被换成 HTML 实体。规则 DSL 的分隔符 `=>` 因此变成 `=&gt;`，
// splitEntries 匹配不到就整条丢弃 —— 表现为「保存后规则不回显」，且下次保存会把
// 磁盘上正确的规则一并清空。宿主的转义是它的 XSS 防护、插件侧无法关闭，只能在这里
// 对称还原。请求方向不受影响（宿主不处理请求体），故只解码、不编码。
const HTML_ENTITIES: Record<string, string> = {
  '&amp;': '&',
  '&#39;': "'",
  '&lt;': '<',
  '&gt;': '>',
  '&#34;': '"',
}

// 单次扫描替换，保证只解一层：原文里字面量的 `&gt;` 被宿主转义成 `&amp;gt;`，
// 还原到 `&gt;` 即停，不会继续解成 `>`。
function unescapeHTML(value: string): string {
  return value.replace(/&(?:amp|lt|gt|#39|#34);/g, (m) => HTML_ENTITIES[m])
}

// 只还原值；对象的 key 不动（宿主的 JSONValue 同样只处理 value）。
function unescapeDeep(value: unknown): unknown {
  if (typeof value === 'string') return unescapeHTML(value)
  if (Array.isArray(value)) return value.map((item) => unescapeDeep(item))
  if (value !== null && typeof value === 'object') {
    const out: Record<string, unknown> = {}
    for (const [k, v] of Object.entries(value)) out[k] = unescapeDeep(v)
    return out
  }
  return value
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const resp = await fetch(PLUGIN_BASE + path, {
    method,
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${getKey()}`,
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (resp.status === 401 || resp.status === 403) {
    clearKey()
    throw new Error('认证失败，请重新登录')
  }
  const text = await resp.text()
  if (!resp.ok) {
    let msg = `HTTP ${resp.status}`
    let audit: AuditMeta | undefined
    try {
      const parsed = unescapeDeep(JSON.parse(text)) as { error?: string; audit?: AuditMeta }
      if (parsed && typeof parsed.error === 'string') msg = parsed.error
      audit = parsed?.audit
    } catch { /* keep default */ }
    throw new ManagementAPIError(msg, audit)
  }
  return unescapeDeep(JSON.parse(text)) as T
}

export const api = {
  getAudit: (date?: string, page?: number, pageSize?: number, module?: string) => {
    const query = new URLSearchParams()
    if (date) query.set('date', date)
    if (page !== undefined) query.set('page', String(page))
    if (pageSize !== undefined) query.set('page_size', String(pageSize))
    if (module) query.set('module', module)
    return call<AuditPage>('GET', `/audit${query.size ? `?${query}` : ''}`)
  },
  getKeeperAuthNames: () => call<KeeperAuthNamesResponse>('GET', '/keeper/auth-names'),
  refreshKeeperAuthNames: () => call<KeeperAuthNamesResponse>('POST', '/keeper/auth-names/refresh'),
  patchKeeperAuthName: (authIndex: string, alias: string) =>
    call<KeeperAuthNameUpdateResponse>('PATCH', '/keeper/auth-names', { auth_index: authIndex, alias }),
  getKeeperAliases: () => call<KeeperAliasesResponse>('GET', '/keeper/key-aliases'),
  refreshKeeperAliases: () => call<KeeperAliasesResponse>('POST', '/keeper/key-aliases/refresh'),
  getState: () => call<StateResponse>('GET', '/state'),
  putRules: (rules: RuleSet) => call<StateResponse>('PUT', '/rules', rules),
  postKey: (binding: KeyBinding) => call<StateResponse>('POST', '/keys', binding),
  patchKey: (key: string, patch: Partial<Pick<KeyBinding,
    'alias' | 'enabled' | 'blocked' | 'rules' | 'channel_target' | 'fast_allowed'>>) =>
    call<StateResponse>('PATCH', `/keys?key=${encodeURIComponent(key)}`, patch),
  deleteKey: (key: string) => call<StateResponse>('DELETE', `/keys?key=${encodeURIComponent(key)}`),
  preview: (req: PreviewRequest) => call<PreviewResponse>('POST', '/preview', req),
  // 通知管理面（T09 路由契约）。PUT 响应是完整 state（managementGetState），
  // 不含 notifications 块 —— 保存后需要 getSettings() 刷新回显。
  notifications: {
    getSettings: () => call<NotificationSettings>('GET', '/notifications/settings'),
    putSettings: (s: NotificationSettings) => call<StateResponse>('PUT', '/notifications/settings', s),
    getStatus: () => call<NotificationStatus>('GET', '/notifications/status'),
    preview: (req: { key?: string; notification_id?: string }) =>
      call<NotificationPreviewResponse>('POST', '/notifications/preview', req),
    testSend: (req: { key?: string; notification_id?: string }) =>
      call<{ delivery_ids: string[] }>('POST', '/notifications/test-send', req),
    deliveries: (filter: { key_fingerprint?: string; notification_id?: string; platform?: string; outcome?: string; limit?: number }) => {
      const q = new URLSearchParams()
      for (const [k, v] of Object.entries(filter)) if (v) q.set(k, String(v))
      return call<{ items: DeliveryRecord[] }>('GET', `/notifications/deliveries${q.size ? `?${q}` : ''}`)
    },
    retryDelivery: (id: string) => call<{ job_id: string }>('POST', '/notifications/deliveries/retry', { id }),
    // 拉取平台通讯录成员（请求体带凭证，未保存也能拉）。失败也是 HTTP 200 信封。
    fetchMembers: (req: { platform: PlatformKind; credentials: PlatformFetchCredentials }) =>
      call<NotificationMembersResponse>('POST', '/notifications/fetch-members', req),
  },
}

// CPA 主程序的 api-keys 列表（GET /v0/management/api-keys 返回原文）。
export async function listCpaApiKeys(): Promise<string[]> {
  const resp = await fetch('/v0/management/api-keys', {
    headers: { Authorization: `Bearer ${getKey()}` },
  })
  if (!resp.ok) throw new Error(`读取 CPA api-keys 失败：HTTP ${resp.status}`)
  const body = (await resp.json()) as { 'api-keys'?: string[] }
  return body['api-keys'] ?? []
}

export async function listCpaAuthFiles(): Promise<CpaAuthFile[]> {
  const resp = await fetch('/v0/management/auth-files', {
    headers: { Authorization: `Bearer ${getKey()}` },
  })
  if (resp.status === 401 || resp.status === 403) {
    clearKey()
    throw new Error('认证失败，请重新登录')
  }
  if (!resp.ok) throw new Error(`读取 CPA auth-files 失败：HTTP ${resp.status}`)
  const body = (await resp.json()) as { files?: CpaAuthFile[] }
  return body.files ?? []
}

// Resolve IDs on the plugin using Go's SHA-256, including on HTTP-hosted pages.
// Raw provider configuration is transient and never part of a saved binding.
export async function listCpaCredentials(): Promise<CpaAuthFile[]> {
  const endpoints = ['gemini-api-key', 'interactions-api-key', 'claude-api-key',
    'codex-api-key', 'xai-api-key', 'openai-compatibility', 'vertex-api-key']
  const [files, entries] = await Promise.all([
    listCpaAuthFiles(),
    Promise.all(endpoints.map(async endpoint => {
      const resp = await fetch(`/v0/management/${endpoint}`, {
        headers: { Authorization: `Bearer ${getKey()}` },
      })
      if (resp.status === 401 || resp.status === 403) {
        clearKey()
        throw new Error('认证失败，请重新登录')
      }
      // These provider types were added after the original supported SDK.
      if (resp.status === 404 && ['interactions-api-key', 'xai-api-key'].includes(endpoint)) {
        return [endpoint, []] as const
      }
      if (!resp.ok) throw new Error(`读取 CPA ${endpoint} 失败：HTTP ${resp.status}`)
      const body = await resp.json()
      if (!body || !(endpoint in body) || (body[endpoint] !== null && !Array.isArray(body[endpoint]))) {
        throw new Error(`CPA ${endpoint} 响应格式错误`)
      }
      return [endpoint, body[endpoint] ?? []] as const
    })),
  ])
  const configured = await call<CpaAuthFile[]>('POST', '/channel-credentials', Object.fromEntries(entries))
  if (!Array.isArray(configured)) throw new Error('凭据目录响应格式错误')
  const combined = new Map<string, CpaAuthFile>()
  for (const file of files) combined.set(file.id, { ...file, source: 'auth-file' })
  for (const credential of configured) combined.set(credential.id, credential)
  return [...combined.values()]
}
