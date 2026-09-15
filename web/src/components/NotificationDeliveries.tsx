// 通知展示共享件：投递状态单元格、最近投递摘要、行内投递记录表。
// Key 级（KeyNotificationsTab）与全局多条列表（NotificationsPanel）共用。
import { Button, Table, Tag, Toast, Typography } from '@douyinfe/semi-ui'
import { useEffect, useState } from 'react'
import { api } from '../api'
import type { DeliveryRecord } from '../notifications'
import { formatDateTime } from '../formatTime'

export function outcomeCell(r: DeliveryRecord) {
  if (r.outcome === 'accepted') return <Tag color="green">已投递</Tag>
  if (r.outcome === 'failed') return <Tag color="red">{r.error_code || 'failed'}</Tag>
  return <Tag color="grey">{r.error_code || 'unknown'}</Tag>
}

/** 最近一次投递（列内摘要）：accepted 绿 / failed 红 + error_code / 无记录灰。 */
export function LastDelivery({ notificationId }: { notificationId: string }) {
  const [record, setRecord] = useState<DeliveryRecord | null>(null)
  const [phase, setPhase] = useState<'loading' | 'ready' | 'error'>('loading')
  useEffect(() => {
    let alive = true
    setPhase('loading')
    api.notifications.deliveries({ notification_id: notificationId, limit: 1 })
      .then((r) => { if (alive) { setRecord(r.items[0] ?? null); setPhase('ready') } })
      .catch(() => { if (alive) setPhase('error') })
    return () => { alive = false }
  }, [notificationId])
  if (phase !== 'ready') return <Typography.Text type="tertiary">—</Typography.Text>
  if (!record) return <Typography.Text type="tertiary">暂无投递</Typography.Text>
  return outcomeCell(record)
}

/** 展开行内的投递记录表（最近 20 条）；failed/unknown 行可重试并重拉。 */
export function DeliveriesTable({ notificationId }: { notificationId: string }) {
  const [items, setItems] = useState<DeliveryRecord[] | null>(null)
  const load = () => {
    api.notifications.deliveries({ notification_id: notificationId, limit: 20 })
      .then((r) => setItems(r.items))
      .catch(() => setItems([]))
  }
  useEffect(() => { load() }, [notificationId])
  return (
    <Table dataSource={items ?? []} loading={items === null} rowKey="id" pagination={false} size="small"
      columns={[
        { title: '时间', dataIndex: 'created_at', render: (v: string) => formatDateTime(v) },
        { title: '平台', dataIndex: 'platform' },
        { title: '周期', dataIndex: 'period_key' },
        { title: '结果', dataIndex: 'outcome', render: (_: unknown, r: DeliveryRecord) => outcomeCell(r) },
        { title: '详情', dataIndex: 'detail', render: (v?: string) => v || '—' },
        { title: '', dataIndex: 'ops', render: (_: unknown, r: DeliveryRecord) => (
          r.outcome !== 'accepted' ? (
            <Button size="small" onClick={() =>
              api.notifications.retryDelivery(r.id).then(load).catch((e: Error) => Toast.error(e.message))}
            >重试</Button>
          ) : null
        ) },
      ]} />
  )
}
