import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from './api'
import {
  MODULE_KINDS,
  MODULE_LABELS,
  STAT_MODULE_KINDS,
  WINDOW_MODULE_KINDS,
  type NotificationSettings,
} from './notifications'
import { clearKey, setKey } from './session'

// T10：notifications 管理面 API 客户端。走 api.ts 的 call 路径，
// 自动获得 401/403 跳转与宿主 HTML 实体还原（unescapeDeep）。
// 后端契约真源：notification_management.go / notification_types.go（T09 产出）。
function stubFetch(payload: unknown, status = 200): void {
  vi.stubGlobal('fetch', vi.fn(async () => ({
    ok: status >= 200 && status < 300,
    status,
    text: async () => JSON.stringify(payload),
  })))
}

afterEach(() => {
  vi.unstubAllGlobals()
  clearKey()
})

describe('notifications api', () => {
  it('putSettings PUT 全量 settings 并返回 stateResponse 形状', async () => {
    setKey('k')
    stubFetch({
      version: 1,
      rules: { global: '', claude: '', codex: '', openai: '' },
      key_bindings: [],
      persisted: true,
      state_file: '/tmp/s.json',
      plugin_version: '',
    })
    const settings: NotificationSettings = {
      enabled: true,
      global_default: {
        id: 'global',
        name: '用量通知',
        enabled: true,
        template_follows_global: false,
        schedule_follows_global: false,
        modules: [{ kind: 'daily', period: 'current' }],
        schedule: { kind: 'interval', interval: 3600, month_end: false, month: 0, day: 0, time: '' },
        platforms: [{ kind: 'feishu', enabled: true, webhook: 'https://x/hook?a=1<b', user_ids: ['ou_a'], sign_secret: '' }],
      },
    }
    const result = await api.notifications.putSettings(settings)
    const [url, init] = (fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    expect(url).toBe('/v0/management/plugins/model-mapper-plus/notifications/settings')
    expect(init.method).toBe('PUT')
    expect(init.headers.Authorization).toBe('Bearer k')
    // 请求方向不转义：原样 JSON（宿主不处理请求体）
    expect(JSON.parse(init.body)).toEqual(settings)
    // 响应是完整 state（managementGetState），不含 notifications 块
    expect(result.persisted).toBe(true)
  })

  it('getSettings 明文回显并还原宿主转义的 webhook', async () => {
    setKey('k')
    stubFetch({
      enabled: true,
      global_default: {
        id: 'global', name: '用量通知', enabled: true,
        modules: [{ kind: 'daily', period: 'current' }],
        platforms: [{ kind: 'feishu', enabled: true, webhook: 'https://x/hook?a=1&lt;b', user_ids: ['ou_a'] }],
      },
    })
    const result = await api.notifications.getSettings()
    const [url, init] = (fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    expect(url).toContain('/notifications/settings')
    expect(init.method).toBe('GET')
    // 宿主 html.EscapeString 把 &lt; 还原回原始值 '<'
    expect(result.global_default.platforms?.[0]?.webhook).toBe('https://x/hook?a=1<b')
  })

  it('getStatus 返回服务健康视图（含 global_name）', async () => {
    setKey('k')
    stubFetch({ running: true, revision: 3, pending_jobs: 1, next_fire: '2026-09-13T14:00:00+08:00', global_name: '用量通知' })
    const result = await api.notifications.getStatus()
    const [url] = (fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    expect(url).toContain('/notifications/status')
    expect(result.running).toBe(true)
    expect(result.revision).toBe(3)
    expect(result.global_name).toBe('用量通知')
  })

  it('preview 发送 key/notification_id 并还原转义的正文', async () => {
    setKey('k')
    stubFetch({ text: '日统计 <b>1</b>', warnings: ['Keeper 不可用'], bytes: 33 })
    const result = await api.notifications.preview({ key: '', notification_id: '' })
    const [url, init] = (fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    expect(url).toContain('/notifications/preview')
    expect(init.method).toBe('POST')
    expect(JSON.parse(init.body)).toEqual({ key: '', notification_id: '' })
    expect(result).toEqual({ text: '日统计 <b>1</b>', warnings: ['Keeper 不可用'], bytes: 33 })
  })

  it('testSend 立即发送并返回 delivery_ids', async () => {
    setKey('k')
    stubFetch({ delivery_ids: ['d1', 'd2'] })
    const result = await api.notifications.testSend({ key: 'sk-1', notification_id: 'n1' })
    const [url, init] = (fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    expect(url).toContain('/notifications/test-send')
    expect(init.method).toBe('POST')
    expect(JSON.parse(init.body)).toEqual({ key: 'sk-1', notification_id: 'n1' })
    expect(result.delivery_ids).toEqual(['d1', 'd2'])
  })

  it('deliveries 拼接过滤查询串（空值跳过）并返回 snake_case 记录', async () => {
    setKey('k')
    stubFetch({
      items: [{
        id: 'd1', job_id: 'j1', key_fingerprint: 'fp1', notification_id: 'global',
        platform: 'feishu', period_key: '2026-09-13', outcome: 'failed',
        error_code: 'timeout', detail: 'upstream &amp; down', created_at: '2026-09-13T10:00:00Z',
      }],
    })
    const result = await api.notifications.deliveries({ key_fingerprint: 'fp1', outcome: 'failed', limit: 20 })
    const [url] = (fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    expect(String(url)).toContain('/notifications/deliveries?key_fingerprint=fp1&outcome=failed&limit=20')
    expect(result.items[0].outcome).toBe('failed')
    // 响应值的 &amp; 被还原
    expect(result.items[0].detail).toBe('upstream & down')

    await api.notifications.deliveries({})
    const [url2] = (fetch as ReturnType<typeof vi.fn>).mock.calls[1]
    expect(String(url2)).not.toContain('?')
  })

  it('retryDelivery POST {id} 并返回 job_id', async () => {
    setKey('k')
    stubFetch({ job_id: 'j9' })
    const result = await api.notifications.retryDelivery('d1')
    const [url, init] = (fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    expect(url).toContain('/notifications/deliveries/retry')
    expect(init.method).toBe('POST')
    expect(JSON.parse(init.body)).toEqual({ id: 'd1' })
    expect(result.job_id).toBe('j9')
  })
})

describe('notification 模块常量', () => {
  it('覆盖 spec 的七个模块并带中文标签', () => {
    expect(STAT_MODULE_KINDS).toEqual(['daily', 'monthly', 'half_year', 'yearly'])
    expect(WINDOW_MODULE_KINDS).toEqual(['window_5h', 'weekly', 'reset_cards'])
    expect(MODULE_KINDS).toHaveLength(7)
    expect(MODULE_LABELS).toEqual({
      daily: '日统计', monthly: '月统计', half_year: '半年统计', yearly: '年统计',
      window_5h: '5H 窗口', weekly: 'Weekly 窗口', reset_cards: '重置卡',
    })
  })
})
