import { Input, Switch, Tabs, TabPane, Typography } from '@douyinfe/semi-ui'
import { useEffect, useState } from 'react'
import type { PlatformIdentity, PlatformKind } from '../notifications'

const PLATFORM_TABS: { kind: PlatformKind; label: string }[] = [
  { kind: 'wecom', label: '企业微信' },
  { kind: 'feishu', label: '飞书' },
  { kind: 'dingtalk', label: '钉钉' },
]

export function validatePlatforms(platforms: PlatformIdentity[]): string[] {
  const errs: string[] = []
  for (const p of platforms) {
    if (!p.enabled) continue
    if (!p.webhook?.trim()) errs.push(`${p.kind}: Webhook 地址为必填项`)
    if (!p.user_ids?.some((u) => u.trim())) errs.push(`${p.kind}: 启用通知时用户唯一 ID 为必填项`)
  }
  return errs
}

function toDraft(value: PlatformIdentity[]): Record<PlatformKind, PlatformIdentity> {
  const draft: Record<PlatformKind, PlatformIdentity> = {
    wecom: { kind: 'wecom', enabled: false },
    feishu: { kind: 'feishu', enabled: false },
    dingtalk: { kind: 'dingtalk', enabled: false },
  }
  for (const p of value) draft[p.kind] = p
  return draft
}

export default function PlatformIdentityEditor({ value, onChange }: {
  value: PlatformIdentity[]
  onChange: (p: PlatformIdentity[]) => void
}) {
  const [active, setActive] = useState<PlatformKind>('feishu')
  // 本地草稿态：行内校验提示即时反映编辑，不依赖父组件回写（保存拦截由 T13 用 validatePlatforms 兜底）
  const [draft, setDraft] = useState<Record<PlatformKind, PlatformIdentity>>(() => toDraft(value))
  useEffect(() => { setDraft(toDraft(value)) }, [value])
  // 仅修改目标平台条目，其余平台原样保留（平台隔离的受控保证）；目标平台无条目时追加该平台条目
  const update = (kind: PlatformKind, patch: Partial<PlatformIdentity>) => {
    const nextP: PlatformIdentity = { ...draft[kind], ...patch }
    setDraft({ ...draft, [kind]: nextP })
    onChange(value.some((p) => p.kind === kind)
      ? value.map((p) => (p.kind === kind ? { ...p, ...patch } : p))
      : [...value, nextP])
  }
  return (
    <Tabs type="card" keepDOM={false} activeKey={active} onChange={(k) => setActive(k as PlatformKind)}>
      {PLATFORM_TABS.map(({ kind, label }) => {
        const p = draft[kind]
        const missingUser = p.enabled && !p.user_ids?.some((u) => u.trim())
        const missingHook = p.enabled && !p.webhook?.trim()
        return (
          <TabPane tab={<span>{label}{p.enabled ? ' · 启用' : ''}</span>} itemKey={kind} key={kind}>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 12, paddingTop: 8 }}>
              <label style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <Switch aria-label={`启用${label}通知`} checked={p.enabled}
                  onChange={(enabled) => update(kind, { enabled })} />
                <Typography.Text>启用{label}通知</Typography.Text>
              </label>
              <Input aria-label="Webhook 地址" value={p.webhook ?? ''} placeholder="https://…"
                onChange={(webhook) => update(kind, { webhook })} />
              {missingHook && <Typography.Text type="danger">Webhook 地址为必填项</Typography.Text>}
              <Input aria-label="用户唯一 ID" value={(p.user_ids ?? []).join(',')}
                placeholder={kind === 'feishu' ? 'ou_ 开头的 Open ID（外部群仅支持 Open ID）' : '多个用英文逗号分隔'}
                onChange={(v) => update(kind, { user_ids: v.split(',').map((s) => s.trim()).filter(Boolean) })} />
              {missingUser && <Typography.Text type="danger">启用通知时用户唯一 ID 为必填项</Typography.Text>}
              {kind === 'wecom'
                ? <Typography.Text type="tertiary">
                    此平台无签名密钥<span>（企业微信群机器人凭据即 Webhook 自身）</span>
                  </Typography.Text>
                : <Input aria-label="签名密钥" mode="password" value={p.sign_secret ?? ''}
                    placeholder="机器人开启签名校验时填写" onChange={(sign_secret) => update(kind, { sign_secret })} />}
            </div>
          </TabPane>
        )
      })}
    </Tabs>
  )
}
