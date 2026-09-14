// Key 级「通知」页签（T14）。受控渲染 binding.notifications，编辑结果经
// NotificationEditor.onSaved → onChange 回流 KeysPanel 的 editing state，随既有
// 「保存」按钮走 postKey，本组件不新增保存路径。行内操作针对已保存通知：
// 测试发送（test-send 受理提示）、投递记录展开表（失败/未终态行可重试）。
// deliveries 查询按 notification_id 过滤（前端拿不到 key 指纹，通知 id 全局唯一），
// 且全部自带 catch 兜底：Modal keepDOM 下页签随编辑弹窗挂载，请求失败不得抛未处理
// rejection 污染宿主（KeysPanel 既有测试未 mock notifications API）。
import { Button, Modal, Switch, Table, Toast, Typography } from '@douyinfe/semi-ui'
import { useState } from 'react'
import { api, type KeyBinding } from '../api'
import type { Notification } from '../notifications'
import { scheduleSummary } from '../notificationSummary'
import NotificationEditor from '../components/NotificationEditor'
import { DeliveriesTable, LastDelivery } from '../components/NotificationDeliveries'

/** 新建草稿：默认跟随全局模板与计划（2026-09-13 确认），平台身份三项全关待填。 */
function newDraft(): Notification {
  return {
    id: crypto.randomUUID(),
    name: '',
    enabled: true,
    template_follows_global: true,
    schedule_follows_global: true,
    modules: [],
    platforms: [
      { kind: 'wecom', enabled: false },
      { kind: 'feishu', enabled: false },
      { kind: 'dingtalk', enabled: false },
    ],
  }
}

export default function KeyNotificationsTab({ binding, onChange, globalName, globalSchedule }: {
  binding: KeyBinding
  siblingNames: string[]
  onChange: (notifications: Notification[]) => void
  globalName?: string
  globalSchedule?: string
}) {
  const list = binding.notifications ?? []
  const [editing, setEditing] = useState<{ draft: Notification; originalName: string } | null>(null)
  const [expanded, setExpanded] = useState<string>()

  const toggle = (n: Notification, enabled: boolean) =>
    onChange(list.map((x) => (x.id === n.id ? { ...x, enabled } : x)))

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      <div style={{ display: 'flex' }}>
        <Button theme="solid" style={{ marginLeft: 'auto' }}
          onClick={() => setEditing({ draft: newDraft(), originalName: '' })}>＋ 新增通知</Button>
      </div>
      {list.length === 0 ? (
        <div style={{ border: '1px dashed var(--semi-color-border)', borderRadius: 8, padding: 20, textAlign: 'center' }}>
          <Typography.Paragraph>该 Key 尚未配置专属通知</Typography.Paragraph>
          <Typography.Text type="tertiary">
            当前按全局默认通知「{globalName ?? '用量通知'}」发送（{globalSchedule ?? '跟随全局计划'}）——新增第一条专属通知后，全局默认通知即对本 Key 停止。
          </Typography.Text>
        </div>
      ) : (
        <Table dataSource={list} rowKey="id" pagination={false}
          expandedRowKeys={expanded ? [expanded] : []}
          onExpandedRowsChange={(keys) => setExpanded((keys ?? [])[0] as unknown as string | undefined)}
          expandedRowRender={(n?: Notification) => <DeliveriesTable notificationId={n!.id} />}
          columns={[
            { title: '通知名称', dataIndex: 'name' },
            { title: '启用', dataIndex: 'enabled', render: (_: unknown, n: Notification) => (
              <Switch aria-label={`启用通知：${n.name}`} checked={n.enabled} onChange={(v) => toggle(n, v)} />
            ) },
            { title: '发送计划', render: (_: unknown, n: Notification) => (
              <Typography.Text>{scheduleSummary(n)}</Typography.Text>
            ) },
            { title: '平台', render: (_: unknown, n: Notification) =>
              (n.platforms ?? []).filter((p) => p.enabled).map((p) => p.kind).join('、') || '—' },
            { title: '最近投递', render: (_: unknown, n: Notification) => <LastDelivery notificationId={n.id} /> },
            { title: '操作', render: (_: unknown, n: Notification) => (
              <>
                <Button size="small" style={{ marginRight: 4 }}
                  onClick={() => setEditing({ draft: n, originalName: n.name })}>编辑</Button>
                <Button size="small" style={{ marginRight: 4 }}
                  onClick={() => api.notifications.testSend({ key: binding.key, notification_id: n.id })
                    .then(() => Toast.success('测试发送已受理'))
                    .catch((e: Error) => Toast.error(e.message))}
                >测试发送</Button>
                <Button size="small" style={{ marginRight: 4 }}
                  onClick={() => setExpanded(expanded === n.id ? undefined : n.id)}>投递记录</Button>
                <Button size="small" type="danger" onClick={() => Modal.confirm({
                  title: '删除通知', content: `确认删除 ${n.name}？`,
                  onOk: () => onChange(list.filter((x) => x.id !== n.id)),
                })}>删除</Button>
              </>
            ) },
          ]} />
      )}
      {editing && (
        <NotificationEditor visible originalName={editing.originalName}
          siblingNames={list.map((n) => n.name).filter((name) => name !== editing.originalName)}
          initial={editing.draft}
          onSaved={(n) => onChange(editing.originalName
            ? list.map((x) => (x.id === n.id ? n : x))
            : [...list, n])}
          onClose={() => setEditing(null)} />
      )}
    </div>
  )
}
