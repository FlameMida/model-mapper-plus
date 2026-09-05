import { getKey, clearKey } from './session'

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
}

export interface KeyBinding {
  key: string
  alias: string
  enabled: boolean
  blocked: boolean
  rules: RuleSet
  channel_target?: ChannelTarget
  fast_allowed?: boolean
}

export interface KeeperAlias { key: string; alias: string }
export type KeeperAliasesResponse =
  | { status: 'ready'; items: KeeperAlias[]; fetched_at: string }
  | { status: 'disabled'; items: [] }
  | { status: 'unavailable'; items: []; error_code: 'configuration_error' | 'authentication_failed' | 'rate_limited' | 'timeout' | 'invalid_response' | 'connection_failed'; retry_after_seconds?: number }

export interface StateResponse {
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
    try {
      const parsed = JSON.parse(text)
      if (parsed && typeof parsed.error === 'string') msg = unescapeHTML(parsed.error)
    } catch { /* keep default */ }
    throw new Error(msg)
  }
  return unescapeDeep(JSON.parse(text)) as T
}

export const api = {
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
