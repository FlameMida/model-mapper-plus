// 通知编辑抽屉（T13）。Semi SideSheet（medium）+ 双 Tab（通知模板 / 平台身份配置），
// footer 三键「预览消息 / 取消 / 保存」（mockup 定稿）。草稿态随 props.initial 重置。
// 保存拦截 = 名称必填 + 同 Key 唯一 + 模块非空（不跟随全局模板时）+ validatePlatforms
// （按 scope：key 级无 @所有人）。错误定位到具体字段行内展示（名称错误在名称框下、
// 模块错误在模块编辑器下、平台错误在平台字段行内）；保存点击时第一条错误以 Toast
// 弹窗提示并切到出错 Tab（2026-09-14 确认）。
// onSaved 可为 async（抽屉保存即落库路径）：reject 时抽屉保持打开，由调用方 Toast 错误。
// key scope 保存时剥离平台 at_all（后端 managementPostKey 同样剥离，双保险）。
// 预览针对已保存实体（POST /notifications/preview），抽屉内编辑不即时上传；dirty 时提示
// 预览可能过期，预览成功后清除 dirty。
import { Banner, Button, Input, SideSheet, Switch, TabPane, Tabs, Toast, Typography } from '@douyinfe/semi-ui'
import { useEffect, useMemo, useState } from 'react'
import type { CSSProperties } from 'react'
import { api } from '../api'
import type { Notification } from '../notifications'
import NotificationModulesEditor from './NotificationModulesEditor'
import NotificationScheduleEditor from './NotificationScheduleEditor'
import PlatformIdentityEditor, { validatePlatforms } from './PlatformIdentityEditor'

// 默认计划（每天 09:00）：跟随全局创建的通知从未落过独立 schedule，关闭跟随时
// 需要一个起点对象，否则编辑器因空值守卫永远不渲染。
const DEFAULT_SCHEDULE: NonNullable<Notification['schedule']> = { kind: 'interval', interval: 86400, time: '09:00:00' }

// 分组小标题（与 NotificationModulesEditor 的「统计周期/渠道窗口」分组标题同款）
const SECTION_TITLE_STYLE: CSSProperties = { fontSize: 12, color: 'var(--semi-color-text-1)', marginBottom: 6 }

export default function NotificationEditor({ visible, originalName, siblingNames, initial, scope = 'key', onSaved, onClose }: {
  visible: boolean
  /** 编辑态原名（空串 = 新建）。 */
  originalName: string
  /** 同 Key 其它通知名（名称唯一校验数据源）。 */
  siblingNames: string[]
  /** 草稿；新建默认 template_follows_global=true / schedule_follows_global=true（T02 语义）。 */
  initial: Notification
  /** global = 全局通知条目（自身不跟随全局，隐藏跟随开关；仅全局展示 @所有人）。 */
  scope?: 'key' | 'global'
  /** 保存回调；返回 Promise 且 reject 时抽屉不关闭（保存即落库失败场景）。 */
  onSaved: (n: Notification) => void | Promise<void>
  onClose: () => void
}) {
  const [draft, setDraft] = useState<Notification>(initial)
  const [dirty, setDirty] = useState(false)
  const [preview, setPreview] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [activeTab, setActiveTab] = useState('template')

  // initial 变化（切换编辑对象）时重置草稿与临时状态
  useEffect(() => {
    setDraft(initial)
    setDirty(false)
    setPreview(null)
    setError('')
    setActiveTab('template')
  }, [initial])

  const edit = (patch: Partial<Notification>) => {
    setDraft((d) => ({ ...d, ...patch }))
    setDirty(true)
  }

  // 字段级守卫：名称错误渲染在名称输入框下、模块错误渲染在模块编辑器下（平台守卫在平台字段行内）
  const nameError = useMemo(() => {
    const name = draft.name.trim()
    if (!name) return '通知名称为必填项'
    const others = siblingNames.filter((n) => originalName === '' || n !== originalName)
    if (others.includes(name)) return '同 Key 内已存在同名通知'
    return ''
  }, [draft, siblingNames, originalName])

  const modulesError = useMemo(() =>
    (scope === 'global' || !draft.template_follows_global) && !draft.modules?.length
      ? '至少启用一个统计模块' : '', [draft, scope])

  const platformsError = useMemo(() => validatePlatforms(draft.platforms ?? [], scope)[0] ?? '', [draft.platforms, scope])

  async function doPreview() {
    try {
      const r = await api.notifications.preview({ notification_id: draft.id || undefined })
      setPreview(r.text)
      setDirty(false)
      setError('')
    } catch (e) {
      setError((e as Error).message)
    }
  }

  async function doSave() {
    // 第一条错误 Toast 提示并定位到出错 Tab；行内错误各自渲染在字段下方。
    if (nameError || modulesError || platformsError) {
      const first = nameError || modulesError || platformsError
      Toast.error(first)
      setActiveTab(nameError || modulesError ? 'template' : 'platforms')
      return
    }
    const payload: Notification = { ...draft, name: draft.name.trim() }
    // key 级通知无 @所有人（2026-09-14）：保存草稿时一并剥离，后端同样兜底。
    if (scope === 'key') {
      payload.platforms = (draft.platforms ?? []).map((p) => ({ ...p, at_all: false }))
    }
    try {
      await onSaved(payload)
    } catch {
      return // 落库失败：调用方已 Toast，抽屉保持打开以便修正
    }
    onClose()
  }

  return (
    <SideSheet title={`编辑通知：${originalName || draft.name}`} visible={visible}
      onCancel={onClose} size="medium" keepDOM
      footer={
        <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end', alignItems: 'center' }}>
          <Button onClick={doPreview}>预览消息</Button>
          <Button onClick={onClose}>取消</Button>
          <Button theme="solid" onClick={() => { void doSave() }}>保存</Button>
        </div>
      }>
      <Tabs type="line" activeKey={activeTab} onChange={(k) => setActiveTab(k as string)}>
        <TabPane tab="通知模板" itemKey="template">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
              <Typography.Text>通知名称</Typography.Text>
              <Input aria-label="通知名称" placeholder="如：日报用量通知" value={draft.name} onChange={(name) => edit({ name })} />
              {nameError && <Typography.Text type="danger">{nameError}</Typography.Text>}
            </div>
            <label style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <Switch checked={draft.enabled} onChange={(enabled) => edit({ enabled })} />
              <Typography.Text>启用通知（关闭只停发，不删除配置）</Typography.Text>
            </label>
            {scope === 'key' && (
              <label style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <Switch checked={!!draft.template_follows_global}
                  onChange={(template_follows_global) => edit({ template_follows_global })} />
                <Typography.Text>跟随全局默认模板</Typography.Text>
              </label>
            )}
            {scope === 'key' && (draft.template_follows_global
              ? <Typography.Text type="tertiary">模块序列与周期将使用全局默认模板（来源：默认全局通知）</Typography.Text>
              : <>
                  <NotificationModulesEditor value={draft.modules ?? []} onChange={(modules) => edit({ modules })} />
                  {modulesError && <Typography.Text type="danger">{modulesError}</Typography.Text>}
                </>)}
            {scope === 'key' && (
              <label style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <Switch checked={!!draft.schedule_follows_global}
                  onChange={(schedule_follows_global) => edit({ schedule_follows_global })} />
                <Typography.Text>跟随全局默认计划</Typography.Text>
              </label>
            )}
            {scope === 'key' && (draft.schedule_follows_global
              ? <Typography.Text type="tertiary">发送计划将使用全局默认计划（来源：默认全局通知）</Typography.Text>
              : <>
                  <div style={SECTION_TITLE_STYLE}>发送计划</div>
                  <NotificationScheduleEditor value={draft.schedule ?? DEFAULT_SCHEDULE}
                    onChange={(schedule) => edit({ schedule })} />
                </>)}
            {scope === 'global' && (
              <>
                <div style={SECTION_TITLE_STYLE}>发送计划</div>
                <NotificationScheduleEditor value={draft.schedule ?? DEFAULT_SCHEDULE}
                  onChange={(schedule) => edit({ schedule })} />
                <NotificationModulesEditor value={draft.modules ?? []} onChange={(modules) => edit({ modules })} />
                {modulesError && <Typography.Text type="danger">{modulesError}</Typography.Text>}
              </>
            )}
          </div>
        </TabPane>
        <TabPane tab="平台身份配置" itemKey="platforms">
          <PlatformIdentityEditor value={draft.platforms ?? []} onChange={(platforms) => edit({ platforms })} scope={scope} />
        </TabPane>
      </Tabs>
      {dirty && (
        <Banner type="warning" bordered style={{ marginTop: 12 }}
          description="配置自上次预览后已修改——预览结果可能过期，发送前请重新预览。" />
      )}
      {preview && <Banner type="info" bordered style={{ marginTop: 12 }} description={preview} />}
      {error && <Banner type="danger" bordered style={{ marginTop: 12 }} description={error} />}
    </SideSheet>
  )
}
