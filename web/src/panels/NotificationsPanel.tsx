// 全局通知面板（T15）。三区块：通知服务状态卡（15s 轮询 + 总开关）、全局默认通知实体卡、
// 投递记录总查询（平台/结果/通知 ID/Key 指纹四筛选 + 失败/未知行重试）。
// 契约：putSettings 响应是不含 notifications 块的 StateResponse（见 api.ts 注释），
// 保存或切总开关后必须回读 getSettings() 刷新回显；unknown 为非终态投递，同样可重试。
import { Button, Card, Input, Select, Switch, Table, Tag, Toast, Typography } from '@douyinfe/semi-ui'
import { useCallback, useEffect, useRef, useState } from 'react'
import { api } from '../api'
import type { DeliveryRecord, NotificationSettings, NotificationStatus } from '../notifications'
import NotificationModulesEditor from '../components/NotificationModulesEditor'
import NotificationScheduleEditor from '../components/NotificationScheduleEditor'
import PlatformIdentityEditor, { FIELD_LABEL_STYLE, validatePlatforms } from '../components/PlatformIdentityEditor'

type DeliveryFilter = { platform?: string; outcome?: string; notification_id?: string; key_fingerprint?: string }

const PLATFORM_OPTIONS = [
  { value: 'wecom', label: '企业微信' },
  { value: 'feishu', label: '飞书' },
  { value: 'dingtalk', label: '钉钉' },
]

const OUTCOME_OPTIONS = [
  { value: 'accepted', label: '已受理' },
  { value: 'failed', label: '失败' },
  { value: 'unknown', label: '未知' },
]

export default function NotificationsPanel() {
  const [settings, setSettings] = useState<NotificationSettings | null>(null)
  const [status, setStatus] = useState<NotificationStatus | null>(null)
  const [saving, setSaving] = useState(false)
  const [deliveries, setDeliveries] = useState<DeliveryRecord[]>([])
  const [filter, setFilter] = useState<DeliveryFilter>({})
  // 通知 ID 筛选候选只增不减：筛选后回包可能不含其他 ID，避免选项塌缩。
  const [knownIds, setKnownIds] = useState<string[]>([])
  // 快速输入筛选时请求可能乱序返回；只采纳最后一次请求的结果。
  const reqSeq = useRef(0)

  const refresh = useCallback(async () => {
    const [s, st] = await Promise.all([api.notifications.getSettings(), api.notifications.getStatus()])
    setSettings(s)
    setStatus(st)
  }, [])

  useEffect(() => { void refresh().catch((e: Error) => Toast.error(e.message)) }, [refresh])
  // 服务状态 15s 轮询；轮询失败静默保留上次状态，避免网络抖动反复弹 Toast。
  useEffect(() => {
    const t = setInterval(() => { void api.notifications.getStatus().then(setStatus).catch(() => {}) }, 15000)
    return () => clearInterval(t)
  }, [])
  useEffect(() => {
    const seq = ++reqSeq.current
    api.notifications.deliveries({ ...filter, limit: 50 })
      .then((r) => {
        if (seq !== reqSeq.current) return
        setDeliveries(r.items)
        setKnownIds((ids) => [...new Set([...ids, ...r.items.map((d) => d.notification_id)])])
      })
      .catch(() => {})
  }, [filter])

  // putSettings 返回完整 state（不含 notifications 块），回读 getSettings() 才是保存后回显。
  const saveSettings = async (next: NotificationSettings) => {
    await api.notifications.putSettings(next)
    setSettings(await api.notifications.getSettings())
  }

  const save = async () => {
    if (!settings) return
    const errs = validatePlatforms(settings.global_default.platforms ?? [])
    if (errs.length) { Toast.error(errs[0]); return }
    setSaving(true)
    try {
      await saveSettings(settings)
      Toast.success('全局默认通知已保存')
    } catch (e) { Toast.error((e as Error).message) } finally { setSaving(false) }
  }

  if (!settings) return <Card title="通知">加载中…</Card>
  const errorCode = status?.running ? undefined : (status?.error_code || status?.store_error)
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16, margin: 16 }}>
      <Card title="通知服务">
        <div style={{ display: 'flex', gap: 12, alignItems: 'center', flexWrap: 'wrap' }}>
          <Switch checked={settings.enabled} aria-label="通知服务总开关"
            onChange={(enabled) => { void saveSettings({ ...settings, enabled }).catch((e: Error) => Toast.error(e.message)) }} />
          {status?.running
            ? <Tag color="green" aria-live="polite">运行中</Tag>
            : <Tag color="red" aria-live="polite">不可用{errorCode ? `（${errorCode}）` : ''}</Tag>}
          <Typography.Text>待发任务 {status?.pending_jobs ?? 0}{status?.next_fire ? ` · 下次触发 ${status.next_fire}` : ''}</Typography.Text>
        </div>
      </Card>
      <Card title="全局默认通知" headerExtraContent={<Button theme="solid" loading={saving} onClick={() => void save()}>保存全局通知</Button>}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <div style={{ display: 'flex', gap: 16, alignItems: 'center', flexWrap: 'wrap' }}>
            <Typography.Text style={FIELD_LABEL_STYLE}>全局通知名称</Typography.Text>
            <Input aria-label="全局通知名称" style={{ width: 320 }} value={settings.global_default.name}
              onChange={(name) => setSettings({ ...settings, global_default: { ...settings.global_default, name } })} />
            <label style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <Switch checked={settings.global_default.enabled} aria-label="启用全局默认通知"
                onChange={(enabled) => setSettings({ ...settings, global_default: { ...settings.global_default, enabled } })} />
              <Typography.Text>启用</Typography.Text>
            </label>
          </div>
          <NotificationModulesEditor value={settings.global_default.modules ?? []}
            onChange={(modules) => setSettings({ ...settings, global_default: { ...settings.global_default, modules } })} />
          {/* schedule 为空（从未配置过）也要渲染编辑器并给默认计划，空值守卫会让全局计划永远无法编辑 */}
          <NotificationScheduleEditor value={settings.global_default.schedule ?? { kind: 'interval', interval: 86400, time: '09:00:00' }}
            onChange={(schedule) => setSettings({ ...settings, global_default: { ...settings.global_default, schedule } })} />
          <PlatformIdentityEditor value={settings.global_default.platforms ?? []}
            onChange={(platforms) => setSettings({ ...settings, global_default: { ...settings.global_default, platforms } })} />
        </div>
      </Card>
      <Card title="投递记录">
        <div style={{ display: 'flex', gap: 8, marginBottom: 8, flexWrap: 'wrap' }}>
          <Select placeholder="全部平台" showClear style={{ width: 140 }} value={filter.platform}
            optionList={PLATFORM_OPTIONS}
            onChange={(platform) => setFilter((f) => ({ ...f, platform: (platform as string) || undefined }))} />
          <Select placeholder="全部结果" showClear style={{ width: 140 }} value={filter.outcome}
            optionList={OUTCOME_OPTIONS}
            onChange={(outcome) => setFilter((f) => ({ ...f, outcome: (outcome as string) || undefined }))} />
          <Select placeholder="全部通知" showClear style={{ width: 160 }} value={filter.notification_id}
            optionList={knownIds.map((id) => ({ value: id, label: id }))}
            onChange={(id) => setFilter((f) => ({ ...f, notification_id: (id as string) || undefined }))} />
          <Input placeholder="Key 指纹" aria-label="Key 指纹筛选" showClear style={{ width: 200 }}
            value={filter.key_fingerprint ?? ''}
            onChange={(v) => setFilter((f) => ({ ...f, key_fingerprint: v.trim() || undefined }))} />
        </div>
        <Table dataSource={deliveries} rowKey="id" pagination={false} size="small"
          columns={[
            { title: '时间', dataIndex: 'created_at', width: 170, render: (v: string) => new Date(v).toLocaleString() },
            { title: '平台', dataIndex: 'platform', width: 90 },
            { title: '周期', dataIndex: 'period_key' },
            { title: '状态', dataIndex: 'outcome', width: 90, render: (v: DeliveryRecord['outcome']) =>
                v === 'accepted' ? <Tag color="green">已受理</Tag>
                  : v === 'unknown' ? <Tag color="grey">未知</Tag>
                    : <Tag color="red">失败</Tag> },
            { title: '错误码', dataIndex: 'error_code', width: 120, render: (v?: string) => v || '—' },
            { title: '操作', width: 90, render: (_v: unknown, r: DeliveryRecord) => r.outcome === 'accepted' ? null
                : <Button size="small" onClick={() => api.notifications.retryDelivery(r.id)
                    .then(() => Toast.success('已重新入队')).catch((e: Error) => Toast.error(e.message))}>重试</Button> },
          ]} />
      </Card>
    </div>
  )
}
