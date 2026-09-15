// Key 级「通知」页签（T14）。受控渲染 binding.notifications，编辑结果经
// NotificationEditor.onSaved → onChange 回流 KeysPanel 的 editing state，随既有
// 「保存」按钮走 postKey，本组件不新增保存路径。行内操作针对已保存通知：
// 测试发送（test-send 受理提示）、投递记录展开表（失败/未终态行可重试）。
// 2026-09-14 quick-fix：
// - 抽屉保存即落库：父级传入 persist（基于已保存绑定 postKey 仅落 notifications），
//   未提供 persist（绑定从未保存过）时只更新草稿并提示先保存 Key 配置；
// - 未落库的行禁用「测试发送」（后端 test-send 只认已保存实体，避免「通知不存在」报错）。
// deliveries 查询按 notification_id 过滤（前端拿不到 key 指纹，通知 id 全局唯一），
// 且全部自带 catch 兜底：Modal keepDOM 下页签随编辑弹窗挂载，请求失败不得抛未处理
// rejection 污染宿主（KeysPanel 既有测试未 mock notifications API）。
import { Button, Modal, Switch, Table, Toast, Tooltip, Typography } from '@douyinfe/semi-ui'
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

export default function KeyNotificationsTab({ binding, onChange, globalName, globalSchedule, savedNotificationIds, persist }: {
  binding: KeyBinding
  siblingNames: string[]
  onChange: (notifications: Notification[]) => void
  globalName?: string
  globalSchedule?: string
  /** 已落库的通知 ID；不在其中的行禁用「测试发送」。缺省视为全部已落库（兼容旧调用）。 */
  savedNotificationIds?: string[]
  /** 抽屉保存即落库通道；缺省表示绑定从未保存过（测试发送需先保存 Key 配置）。 */
  persist?: (notifications: Notification[]) => Promise<void>
}) {
  const list = binding.notifications ?? []
  const [editing, setEditing] = useState<{ draft: Notification; originalName: string } | null>(null)
  const [expanded, setExpanded] = useState<string>()

  const toggle = (n: Notification, enabled: boolean) =>
    onChange(list.map((x) => (x.id === n.id ? { ...x, enabled } : x)))

  const isSaved = (n: Notification) => savedNotificationIds === undefined || savedNotificationIds.includes(n.id)

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
            全局通知按认证渠道发送，不再按本 Key 复制。需要本 Key 自己的用量正文时请新增专属通知。
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
            { title: '下次发送', render: (_: unknown, n: Notification) => n.next_fire || '—' },
            { title: '平台', render: (_: unknown, n: Notification) =>
              (n.platforms ?? []).filter((p) => p.enabled).map((p) => p.kind).join('、') || '—' },
            { title: '最近投递', render: (_: unknown, n: Notification) => <LastDelivery notificationId={n.id} /> },
            { title: '操作', render: (_: unknown, n: Notification) => (
              <>
                <Button size="small" style={{ marginRight: 4 }}
                  onClick={() => setEditing({ draft: n, originalName: n.name })}>编辑</Button>
                {isSaved(n) ? (
                  <Button size="small" style={{ marginRight: 4 }} disabled={!n.enabled}
                    onClick={() => api.notifications.testSend({ key: binding.key, notification_id: n.id })
                      .then(() => Toast.success('测试发送已受理'))
                      .catch((e: Error) => Toast.error(e.message))}
                  >测试发送</Button>
                ) : (
                  <Tooltip content="该通知尚未保存，请先在抽屉保存（或保存 Key 配置）后再测试发送">
                    {/* disabled 按钮不触发 hover 事件，Tooltip 需包一层 span */}
                    <span style={{ marginRight: 4, display: 'inline-block' }}>
                      <Button size="small" disabled={!n.enabled || !isSaved(n)}>测试发送</Button>
                    </span>
                  </Tooltip>
                )}
                <Button size="small" style={{ marginRight: 4 }}
                  onClick={() => setExpanded(expanded === n.id ? undefined : n.id)}>投递记录</Button>
                <Button size="small" type="danger" onClick={() => Modal.confirm({
                  title: '删除通知', content: `确认删除 ${n.name}？`,
                  onOk: () => onChange(list.filter((x) => x.id !== n.id)),
                })}>删除</Button>
              </>
            )},
          ]} />
      )}
      {editing && (
        <NotificationEditor visible originalName={editing.originalName}
          siblingNames={list.map((n) => n.name).filter((name) => name !== editing.originalName)}
          initial={editing.draft} previewKey={binding.key}
          onSaved={async (n) => {
            const next = editing.originalName
              ? list.map((x) => (x.id === n.id ? n : x))
              : [...list, n]
            onChange(next)
            if (persist) {
              // 抽屉保存即落库：persist 失败时抛错保持抽屉打开（父级已 Toast）。
              await persist(next)
              return
            }
            Toast.info('已并入 Key 配置草稿；该 Key 尚未保存过，请先保存 Key 配置再测试发送')
          }}
          onClose={() => setEditing(null)} />
      )}
    </div>
  )
}
