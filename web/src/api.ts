import { getKey, clearKey } from './session'

export interface RuleSet {
  global: string
  claude: string
  codex: string
  openai: string
}

export interface KeyBinding {
  key: string
  alias: string
  enabled: boolean
  rules: RuleSet
}

export interface StateResponse {
  version: number
  rules: RuleSet
  key_bindings: KeyBinding[]
  updated_at?: string
  persisted: boolean
  state_file?: string
  plugin_version?: string
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
}

const PLUGIN_BASE = '/v0/management/plugins/model-mapper-plus'

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
      if (parsed && typeof parsed.error === 'string') msg = parsed.error
    } catch { /* keep default */ }
    throw new Error(msg)
  }
  return JSON.parse(text) as T
}

export const api = {
  getState: () => call<StateResponse>('GET', '/state'),
  putRules: (rules: RuleSet) => call<StateResponse>('PUT', '/rules', rules),
  postKey: (binding: KeyBinding) => call<StateResponse>('POST', '/keys', binding),
  patchKey: (key: string, patch: Partial<Pick<KeyBinding, 'alias' | 'enabled' | 'rules'>>) =>
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
