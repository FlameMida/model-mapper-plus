// 全局通知面板。三区块：通知服务状态卡（15s 轮询 + 总开关）、全局通知多条列表卡
// （v0.6.0 起支持多条：新增/编辑/设为默认/测试发送/删除，默认标记条是 Key 级
// 「跟随全局」的解析来源）、投递记录总查询（平台/结果/通知 ID/Key 指纹四筛选 +
// 失败/未知行重试）。
// 契约：putSettings 响应是不含 notifications 块的 StateResponse（见 api.ts 注释），
// 保存或切总开关后必须回读 getSettings() 刷新回显；unknown 为非终态投递，同样可重试。
import { Button, Card, Input, Modal, Select, Switch, Table, Tag, Toast, Typography } from '@douyinfe/semi-ui'
import { useCallback, useEffect, useRef, useState } from 'react'
import { api } from '../api'
import type { DeliveryRecord, Notification, NotificationSettings, NotificationStatus } from '../notifications'
import { scheduleText } from '../notificationSummary'
import NotificationEditor from '../components/NotificationEditor'
import { validatePlatforms } from '../components/PlatformIdentityEditor'
import { DeliveriesTable, LastDelivery } from '../components/NotificationDeliveries'
import { formatDateTime, formatPeriodKey } from '../formatTime'

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

/** 列表视图：后端归一化保证 notifications 非空；旧数据以 global_default 兜底。 */
function globalList(settings: NotificationSettings): Notification[] {
  return settings.notifications?.length ? settings.notifications : [settings.global_default]
}

/** 新建全局通知草稿：默认标记由「设为默认」操作控制，新建条不带。 */
function newGlobalDraft(): Notification {
  return {
    id: crypto.randomUUID(),
    name: '',
    enabled: true,
    modules: [],
    schedule: { kind: 'interval', interval: 86400, time: '09:00:00' },
    platforms: [
      { kind: 'wecom', enabled: false },
      { kind: 'feishu', enabled: false },
      { kind: 'dingtalk', enabled: false },
    ],
  }
}

export default function NotificationsPanel() {
  const [settings, setSettings] = useState<NotificationSettings | null>(null)
  const [status, setStatus] = useState<NotificationStatus | null>(null)
  const [saving, setSaving] = useState(false)
  const [deliveries, setDeliveries] = useState<DeliveryRecord[]>([])
  const [filter, setFilter] = useState<DeliveryFilter>({})
  const [editing, setEditing] = useState<{ draft: Notification; originalName: string } | null>(null)
  const [expanded, setExpanded] = useState<string>()
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
    for (const n of globalList(settings)) {
      const errs = validatePlatforms(n.platforms ?? [])
      if (errs.length) { Toast.error(`${n.name || '未命名通知'}：${errs[0]}`); return }
    }
    setSaving(true)
    try {
      await saveSettings(settings)
      Toast.success('全局通知已保存')
    } catch (e) { Toast.error((e as Error).message) } finally { setSaving(false) }
  }

  // 列表编辑操作：改 notifications 列表（落盘仍靠「保存全局通知」按钮）。
  const setList = (list: Notification[]) => {
    if (!settings) return
    setSettings({ ...settings, notifications: list, global_default: list.find((n) => n.is_default) ?? list[0] })
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
          <Typography.Text>待发任务 {status?.pending_jobs ?? 0}{status?.next_fire ? ` · 下次触发 ${formatDateTime(status.next_fire)}` : ''}</Typography.Text>
        </div>
      </Card>
      <Card title="全局通知" headerExtraContent={
        <div style={{ display: 'flex', gap: 8 }}>
          <Button onClick={() => setEditing({ draft: newGlobalDraft(), originalName: '' })}>＋ 新增通知</Button>
          <Button theme="solid" loading={saving} onClick={() => void save()}>保存全局通知</Button>
        </div>
      }>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <Typography.Text type="tertiary">
            全局通知可配置多条，各自按自己的发送计划独立触发；「默认」标记条是 Key 级通知「跟随全局」的模板与计划来源。
          </Typography.Text>
          <Table dataSource={globalList(settings)} rowKey="id" pagination={false} size="small"
            expandedRowKeys={expanded ? [expanded] : []}
            onExpandedRowsChange={(keys) => setExpanded((keys ?? [])[0] as unknown as string | undefined)}
            expandedRowRender={(n?: Notification) => <DeliveriesTable notificationId={n!.id} />}
            columns={[
              { title: '通知名称', render: (_: unknown, n: Notification) => (
                <>{n.name}{n.is_default && <Tag color="blue" style={{ marginLeft: 8 }}>默认</Tag>}</>
              ) },
              { title: '启用', dataIndex: 'enabled', render: (_: unknown, n: Notification) => (
                <Switch aria-label={`启用全局通知：${n.name}`} checked={n.enabled}
                  onChange={(enabled) => setList(globalList(settings).map((x) => (x.id === n.id ? { ...x, enabled } : x)))} />
              ) },
              { title: '发送计划', render: (_: unknown, n: Notification) => (
                <Typography.Text>{scheduleText(n.schedule)}</Typography.Text>
              ) },
              { title: '下次发送', render: (_: unknown, n: Notification) => n.next_fire ? formatDateTime(n.next_fire) : '—' },
              { title: '平台', render: (_: unknown, n: Notification) =>
                (n.platforms ?? []).filter((p) => p.enabled).map((p) => p.kind + (p.at_all ? '(@所有人)' : '')).join('、') || '—' },
              { title: '最近投递', render: (_: unknown, n: Notification) => <LastDelivery notificationId={n.id} /> },
              { title: '操作', render: (_: unknown, n: Notification) => (
                <>
                  <Button size="small" style={{ marginRight: 4 }}
                    onClick={() => setEditing({ draft: n, originalName: n.name })}>编辑</Button>
                  {!n.is_default && (
                    <Button size="small" style={{ marginRight: 4 }} onClick={() =>
                      setList(globalList(settings).map((x) => ({ ...x, is_default: x.id === n.id })))
                    }>设为默认</Button>
                  )}
                  <Button size="small" style={{ marginRight: 4 }} disabled={!n.enabled}
                    onClick={() => api.notifications.testSend({ notification_id: n.id })
                      .then(() => Toast.success('测试发送已受理'))
                      .catch((e: Error) => Toast.error(e.message))}
                  >测试发送</Button>
                  <Button size="small" style={{ marginRight: 4 }}
                    onClick={() => setExpanded(expanded === n.id ? undefined : n.id)}>投递记录</Button>
                  <Button size="small" type="danger" onClick={() => Modal.confirm({
                    title: '删除全局通知', content: `确认删除 ${n.name}？`,
                    onOk: () => setList(globalList(settings).filter((x) => x.id !== n.id)),
                  })}>删除</Button>
                </>
              ) },
            ]} />
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
            { title: '时间', dataIndex: 'created_at', width: 170, render: (v: string) => formatDateTime(v) },
            { title: '平台', dataIndex: 'platform', width: 90 },
            { title: '周期', dataIndex: 'period_key', render: (v: string) => formatPeriodKey(v) },
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
      {editing && (
        <NotificationEditor visible scope="global" originalName={editing.originalName}
          siblingNames={globalList(settings).map((n) => n.name).filter((name) => name !== editing.originalName)}
          initial={editing.draft}
          onSaved={async (n) => {
            const list = editing.originalName
              ? globalList(settings).map((x) => (x.id === n.id ? { ...n, is_default: x.is_default } : x))
              : [...globalList(settings), n]
            const next = { ...settings, notifications: list, global_default: list.find((x) => x.is_default) ?? list[0] }
            setSettings(next)
            // 抽屉保存即落库（2026-09-14）：行内「测试发送」始终对已保存实体操作，
            // 消除「抽屉保存 ≠ 落库」两层保存陷阱；失败时抛错保持抽屉打开。
            try {
              await saveSettings(next)
              Toast.success('全局通知已保存')
            } catch (e) {
              Toast.error((e as Error).message)
              throw e
            }
          }}
          onClose={() => setEditing(null)} />
      )}
    </div>
  )
}
