// 通知编辑抽屉（T13）。Semi SideSheet（medium）+ 双 Tab（通知模板 / 平台身份配置），
// footer 三键「预览消息 / 取消 / 保存」（mockup 定稿）。草稿态随 props.initial 重置；
// 保存拦截 = 名称必填 + 同 Key 唯一 + 模块非空（不跟随全局模板时）+ validatePlatforms，
// 保存键 disable 兜底。名称/模块守卫在模板 Tab 内联展示；平台身份错误由
// PlatformIdentityEditor 行内提示呈现（Semi Tabs 默认 keepDOM，文案必须唯一，避免重复）。
// 预览针对已保存实体（POST /notifications/preview），抽屉内编辑不即时上传；dirty 时提示
// 预览可能过期，预览成功后清除 dirty。保存本身不发请求：onSaved 把草稿交父级并入
// binding/全局实体再走 postKey/putSettings（T14/T15，与既有「编辑绑定 → onOk → postKey」
// 路径一致）；test-send 在 T14 列表行内（针对已保存通知），抽屉不重复。
import { Banner, Button, Input, SideSheet, Switch, TabPane, Tabs, Typography } from '@douyinfe/semi-ui'
import { useEffect, useMemo, useState } from 'react'
import { api } from '../api'
import type { Notification } from '../notifications'
import NotificationModulesEditor from './NotificationModulesEditor'
import NotificationScheduleEditor from './NotificationScheduleEditor'
import PlatformIdentityEditor, { validatePlatforms } from './PlatformIdentityEditor'

export default function NotificationEditor({ visible, originalName, siblingNames, initial, onSaved, onClose }: {
  visible: boolean
  /** 编辑态原名（空串 = 新建）。 */
  originalName: string
  /** 同 Key 其它通知名（名称唯一校验数据源）。 */
  siblingNames: string[]
  /** 草稿；新建默认 template_follows_global=true / schedule_follows_global=true（T02 语义）。 */
  initial: Notification
  onSaved: (n: Notification) => void
  onClose: () => void
}) {
  const [draft, setDraft] = useState<Notification>(initial)
  const [dirty, setDirty] = useState(false)
  const [preview, setPreview] = useState<string | null>(null)
  const [error, setError] = useState('')

  // initial 变化（切换编辑对象）时重置草稿与临时状态
  useEffect(() => {
    setDraft(initial)
    setDirty(false)
    setPreview(null)
    setError('')
  }, [initial])

  const edit = (patch: Partial<Notification>) => {
    setDraft((d) => ({ ...d, ...patch }))
    setDirty(true)
  }

  // 模板 Tab 内联守卫：名称必填 / 同 Key 唯一 / 模块非空（平台守卫在 platforms Tab 行内提示）
  const templateBlocked = useMemo(() => {
    const name = draft.name.trim()
    if (!name) return '通知名称为必填项'
    const others = siblingNames.filter((n) => originalName === '' || n !== originalName)
    if (others.includes(name)) return '同 Key 内已存在同名通知'
    if (!draft.template_follows_global && !draft.modules?.length) return '至少启用一个统计模块'
    return ''
  }, [draft, siblingNames, originalName])

  const platformsBlocked = useMemo(() => validatePlatforms(draft.platforms ?? [])[0] ?? '', [draft.platforms])

  // 保存拦截总开关：空串 = 可保存
  const saveBlocked = templateBlocked || platformsBlocked

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

  function doSave() {
    if (saveBlocked) return
    onSaved({ ...draft, name: draft.name.trim() })
    onClose()
  }

  return (
    <SideSheet title={`编辑通知：${originalName || draft.name}`} visible={visible}
      onCancel={onClose} size="medium" keepDOM
      footer={
        <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end', alignItems: 'center' }}>
          <Button onClick={doPreview}>预览消息</Button>
          <Button onClick={onClose}>取消</Button>
          <Button theme="solid" disabled={!!saveBlocked} onClick={doSave}>保存</Button>
        </div>
      }>
      <Tabs type="line">
        <TabPane tab="通知模板" itemKey="template">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
              <Typography.Text>通知名称</Typography.Text>
              <Input aria-label="通知名称" value={draft.name} onChange={(name) => edit({ name })} />
            </div>
            {templateBlocked && <Typography.Text type="danger">{templateBlocked}</Typography.Text>}
            <label style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <Switch checked={draft.enabled} onChange={(enabled) => edit({ enabled })} />
              <Typography.Text>启用通知（关闭只停发，不删除配置）</Typography.Text>
            </label>
            <label style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <Switch checked={!!draft.template_follows_global}
                onChange={(template_follows_global) => edit({ template_follows_global })} />
              <Typography.Text>跟随全局默认模板</Typography.Text>
            </label>
            {draft.template_follows_global
              ? <Typography.Text type="tertiary">模块序列与周期将使用全局默认模板（来源：全局默认通知）</Typography.Text>
              : <NotificationModulesEditor value={draft.modules ?? []} onChange={(modules) => edit({ modules })} />}
            <label style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <Switch checked={!!draft.schedule_follows_global}
                onChange={(schedule_follows_global) => edit({ schedule_follows_global })} />
              <Typography.Text>跟随全局默认计划</Typography.Text>
            </label>
            {draft.schedule_follows_global
              ? <Typography.Text type="tertiary">发送计划将使用全局默认计划（来源：全局默认通知）</Typography.Text>
              : (draft.schedule &&
                <NotificationScheduleEditor value={draft.schedule} onChange={(schedule) => edit({ schedule })} />)}
          </div>
        </TabPane>
        <TabPane tab="平台身份配置" itemKey="platforms">
          <PlatformIdentityEditor value={draft.platforms ?? []} onChange={(platforms) => edit({ platforms })} />
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
