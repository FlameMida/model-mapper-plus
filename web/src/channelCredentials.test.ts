import { afterEach, expect, it, vi } from 'vitest'
import * as apiModule from './api'

afterEach(() => vi.unstubAllGlobals())

const file = { id: 'file.json', provider: 'codex', label: 'Account', status: 'active', disabled: false }
const credential = { id: 'codex:apikey:123', provider: 'codex', label: 'Key ••••1234', status: 'configured', disabled: false, source: 'ai-provider' }

function responses(failure?: { endpoint: string; status: number }, malformed = false) {
  const mock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const endpoint = String(input).split('/').at(-1)!
    const status = endpoint === failure?.endpoint ? failure.status : 200
    const payload = endpoint === 'auth-files' ? { files: [file] }
      : endpoint === 'channel-credentials' ? [credential]
        : { [endpoint]: malformed ? 'invalid' : [{ 'api-key': `fake-${endpoint}` }] }
    return { ok: status === 200, status, json: async () => payload, text: async () => JSON.stringify(payload) }
  })
  vi.stubGlobal('fetch', mock)
  return mock
}

it('统一读取认证文件和七类配置；仅保留已解析目录，按原顺序提交配置', async () => {
  const mock = responses()
  const rows = await apiModule.listCpaCredentials()
  expect(rows).toEqual([{ ...file, source: 'auth-file' }, credential])
  expect(JSON.stringify(rows)).not.toContain('fake-')
  const resolve = mock.mock.calls.find(([url]) => String(url).endsWith('/channel-credentials'))!
  expect(resolve[1]?.method).toBe('POST')
  const config = JSON.parse(String(resolve[1]?.body))
  expect(Object.keys(config)).toHaveLength(7)
  expect(config['codex-api-key']).toEqual([{ 'api-key': 'fake-codex-api-key' }])
  expect(mock.mock.calls.every(([, init]) => (init?.headers as Record<string, string>).Authorization.startsWith('Bearer '))).toBe(true)
})

it('旧宿主的可选 xAI 接口 404 按不支持处理', async () => {
  const mock = responses({ endpoint: 'xai-api-key', status: 404 })
  await expect(apiModule.listCpaCredentials()).resolves.toHaveLength(2)
  const resolve = mock.mock.calls.find(([url]) => String(url).endsWith('/channel-credentials'))!
  expect(JSON.parse(String(resolve[1]?.body))['xai-api-key']).toEqual([])
})

it.each([401, 403, 503])('目录任何来源 HTTP %i 都不能静默显示不完整目录', async (status) => {
  const mock = responses({ endpoint: 'codex-api-key', status })
  await expect(apiModule.listCpaCredentials()).rejects.toThrow(status === 503 ? 'codex-api-key' : '认证失败')
  expect(mock.mock.calls.some(([url]) => String(url).endsWith('/channel-credentials'))).toBe(false)
})

it('拒绝成功状态下的错误响应形状，避免抹掉目录', async () => {
  responses(undefined, true)
  await expect(apiModule.listCpaCredentials()).rejects.toThrow('格式')
})
