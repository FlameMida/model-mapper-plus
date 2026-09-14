import { Button, Input, Select, Switch, Tabs, TabPane, Typography } from '@douyinfe/semi-ui'
import type { CSSProperties, ReactNode } from 'react'
import { useEffect, useState } from 'react'
import { api } from '../api'
import type { NotificationMember, PlatformFetchCredentials, PlatformIdentity, PlatformKind } from '../notifications'

const PLATFORM_TABS: { kind: PlatformKind; label: string }[] = [
  { kind: 'wecom', label: '企业微信' },
  { kind: 'feishu', label: '飞书' },
  { kind: 'dingtalk', label: '钉钉' },
]

// 每平台的「成员拉取凭证」字段（可选，仅用于拉取通讯录成员列表，不参与投递）
const FETCH_CREDENTIAL_FIELDS: Record<PlatformKind, {
  key: keyof PlatformFetchCredentials
  label: string
  placeholder: string
  password?: boolean
}[]> = {
  feishu: [
    { key: 'fetch_app_id', label: '飞书 App ID', placeholder: 'cli_ 开头的应用 App ID' },
    { key: 'fetch_app_secret', label: '飞书 App Secret', placeholder: '应用的 App Secret', password: true },
  ],
  dingtalk: [
    { key: 'fetch_app_key', label: '钉钉 AppKey', placeholder: '企业内部应用 AppKey' },
    { key: 'fetch_app_secret', label: '钉钉 AppSecret', placeholder: '应用的 AppSecret', password: true },
  ],
  wecom: [
    { key: 'fetch_corp_id', label: '企微 Corp ID', placeholder: '企业 ID（corpid）' },
    { key: 'fetch_secret', label: '企微 Secret', placeholder: '自建应用 Secret', password: true },
  ],
}

// 字段标题常显在输入框左侧（不依赖占位符），错误提示缩进对齐输入框左缘（96 + 12 gap）
export const FIELD_LABEL_STYLE: CSSProperties = { width: 96, flexShrink: 0, textAlign: 'right' }
const FIELD_ERROR_STYLE: CSSProperties = { paddingLeft: 108 }

function FieldRow({ label, errorText, children }: { label: string; errorText?: string; children: ReactNode }) {
  return (
    <>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
        <Typography.Text style={FIELD_LABEL_STYLE}>{label}</Typography.Text>
        {children}
      </div>
      {errorText && <Typography.Text type="danger" style={FIELD_ERROR_STYLE}>{errorText}</Typography.Text>}
    </>
  )
}

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

type MemberFetchState = {
  phase: 'idle' | 'loading' | 'error'
  members?: NotificationMember[]
  errorCode?: string
  retryAfterSeconds?: number
}

function memberFetchErrorText(state: MemberFetchState): string | undefined {
  const code = state.errorCode
  if (!code) return undefined
  const text = {
    configuration_error: '拉取凭证不完整',
    timeout: '拉取超时，请稍后重试',
    connection_failed: '无法连接平台接口，请检查网络',
    authentication_failed: '平台凭证被拒绝，请检查应用凭证与通讯录权限',
    rate_limited: '触发平台限流，请稍后重试',
    invalid_response: '平台返回异常数据，请检查应用通讯录权限范围',
  }[code] ?? '拉取失败'
  const retry = state.retryAfterSeconds && state.retryAfterSeconds > 0 ? `（约 ${state.retryAfterSeconds} 秒内重试会直接返回缓存结果）` : ''
  return `${text}${retry}`
}

export default function PlatformIdentityEditor({ value, onChange }: {
  value: PlatformIdentity[]
  onChange: (p: PlatformIdentity[]) => void
}) {
  const [active, setActive] = useState<PlatformKind>(PLATFORM_TABS[0].kind)
  // 本地草稿态：行内校验提示即时反映编辑，不依赖父组件回写（保存拦截由 T13 用 validatePlatforms 兜底）
  const [draft, setDraft] = useState<Record<PlatformKind, PlatformIdentity>>(() => toDraft(value))
  useEffect(() => { setDraft(toDraft(value)) }, [value])
  // 成员拉取结果按平台保存（Tabs keepDOM=false 切换不丢已拉取的成员列表）
  const [memberStates, setMemberStates] = useState<Record<PlatformKind, MemberFetchState>>({
    wecom: { phase: 'idle' }, feishu: { phase: 'idle' }, dingtalk: { phase: 'idle' },
  })
  const [searches, setSearches] = useState<Record<PlatformKind, string>>({ wecom: '', feishu: '', dingtalk: '' })
  // 仅修改目标平台条目，其余平台原样保留（平台隔离的受控保证）；目标平台无条目时追加该平台条目
  const update = (kind: PlatformKind, patch: Partial<PlatformIdentity>) => {
    const nextP: PlatformIdentity = { ...draft[kind], ...patch }
    setDraft({ ...draft, [kind]: nextP })
    onChange(value.some((p) => p.kind === kind)
      ? value.map((p) => (p.kind === kind ? { ...p, ...patch } : p))
      : [...value, nextP])
  }

  const fetchCredentialValues = (kind: PlatformKind): PlatformFetchCredentials => {
    const credentials: PlatformFetchCredentials = {}
    for (const field of FETCH_CREDENTIAL_FIELDS[kind]) {
      const v = (draft[kind][field.key] ?? '').trim()
      if (v) credentials[field.key] = v
    }
    return credentials
  }

  const fetchCredentialsComplete = (kind: PlatformKind): boolean =>
    FETCH_CREDENTIAL_FIELDS[kind].every((field) => (draft[kind][field.key] ?? '').trim() !== '')

  // 组件内 API 调用必须自带 catch（KeyNotificationsTab 纪律）：未处理 rejection 会污染宿主测试。
  const fetchMembers = (kind: PlatformKind) => {
    if (!fetchCredentialsComplete(kind)) {
      setMemberStates((prev) => ({ ...prev, [kind]: { phase: 'error', errorCode: 'configuration_error' } }))
      return
    }
    setMemberStates((prev) => ({ ...prev, [kind]: { phase: 'loading' } }))
    api.notifications.fetchMembers({ platform: kind, credentials: fetchCredentialValues(kind) })
      .then((resp) => {
        setMemberStates((prev) => ({
          ...prev,
          [kind]: resp.status === 'ready'
            ? { phase: 'idle', members: resp.members }
            : { phase: 'error', errorCode: resp.error_code, retryAfterSeconds: resp.retry_after_seconds },
        }))
      })
      .catch(() => {
        setMemberStates((prev) => ({ ...prev, [kind]: { phase: 'error', errorCode: 'connection_failed' } }))
      })
  }

  const matches = (input: string, option: { searchText?: unknown; value?: unknown }) =>
    String(option.searchText ?? option.value ?? '').toLocaleLowerCase().includes(input.trim().toLocaleLowerCase())

  return (
    <Tabs type="card" keepDOM={false} activeKey={active} onChange={(k) => setActive(k as PlatformKind)}>
      {PLATFORM_TABS.map(({ kind, label }) => {
        const p = draft[kind]
        const missingUser = p.enabled && !p.user_ids?.some((u) => u.trim())
        const missingHook = p.enabled && !p.webhook?.trim()
        const memberState = memberStates[kind]

        // 成员选项三元组（照 keyOptions 模式）；已选但未拉取到的 ID 补保留项，不丢手动值。
        let options = (memberState.members ?? []).map((m) => ({
          value: m.id,
          label: `${m.name} · ${m.id}`,
          searchText: `${m.name} ${m.id}`.toLocaleLowerCase(),
        }))
        for (const id of p.user_ids ?? []) {
          if (!options.some((o) => o.value === id)) {
            options = [...options, { value: id, label: `${id}（未拉取到成员名）`, searchText: id.toLocaleLowerCase() }]
          }
        }
        const search = searches[kind]
        const manual = search.trim()
        if (manual && !options.some((o) => o.value === manual)) {
          options = [...options, { value: manual, label: `添加手动 ID：${manual}`, searchText: manual.toLocaleLowerCase() }]
        }

        return (
          <TabPane tab={<span>{label}{p.enabled ? ' · 启用' : ''}</span>} itemKey={kind} key={kind}>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 12, paddingTop: 8 }}>
              <label style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <Switch aria-label={`启用${label}通知`} checked={p.enabled}
                  onChange={(enabled) => update(kind, { enabled })} />
                <Typography.Text>启用{label}通知</Typography.Text>
              </label>
              <FieldRow label="Webhook 地址" errorText={missingHook ? 'Webhook 地址为必填项' : undefined}>
                <Input aria-label="Webhook 地址" style={{ flex: 1 }} value={p.webhook ?? ''} placeholder="https://…"
                  onChange={(webhook) => update(kind, { webhook })} />
              </FieldRow>
              <FieldRow label="用户唯一 ID" errorText={missingUser ? '启用通知时用户唯一 ID 为必填项' : undefined}>
                <Select
                  aria-label="用户唯一 ID"
                  style={{ flex: 1 }}
                  multiple filter={matches}
                  placeholder={kind === 'feishu' ? '拉取成员后搜索选择，或手动添加 ou_ Open ID' : '拉取成员后搜索选择，或手动添加 ID'}
                  value={p.user_ids ?? []}
                  optionList={search ? options.filter((o) => matches(search, o)) : options}
                  loading={memberState.phase === 'loading'}
                  maxTagCount={3} showRestTagsPopover
                  onChange={(ids) => update(kind, { user_ids: ids as string[] })}
                  onSearch={(v) => setSearches((prev) => ({ ...prev, [kind]: v }))}
                  onDropdownVisibleChange={(visible) => { if (!visible) setSearches((prev) => ({ ...prev, [kind]: '' })) }}
                />
              </FieldRow>
              <FieldRow label="签名密钥">
                {kind === 'wecom'
                  ? <Typography.Text type="tertiary">
                      此平台无签名密钥<span>（企业微信群机器人凭据即 Webhook 自身）</span>
                    </Typography.Text>
                  : <Input aria-label="签名密钥" style={{ flex: 1 }} mode="password" value={p.sign_secret ?? ''}
                    placeholder="机器人开启签名校验时填写" onChange={(sign_secret) => update(kind, { sign_secret })} />}
              </FieldRow>
              {FETCH_CREDENTIAL_FIELDS[kind].map((field, index) => (
                <FieldRow key={field.key} label={index === 0 ? '拉取凭证' : ''}>
                  <Input
                    aria-label={field.label}
                    style={{ flex: 1 }}
                    mode={field.password ? 'password' : undefined}
                    value={(draft[kind][field.key] as string | undefined) ?? ''}
                    placeholder={field.placeholder}
                    onChange={(v) => update(kind, { [field.key]: v } as Partial<PlatformIdentity>)}
                  />
                  {index === 0 && (
                    <Button
                      aria-label="拉取成员"
                      loading={memberState.phase === 'loading'}
                      onClick={() => fetchMembers(kind)}
                    >拉取成员</Button>
                  )}
                </FieldRow>
              ))}
              {memberState.phase === 'error' && (
                <Typography.Text type="danger" style={FIELD_ERROR_STYLE}>
                  {memberFetchErrorText(memberState)}
                </Typography.Text>
              )}
              {memberState.members && memberState.phase !== 'loading' && (
                <Typography.Text type="tertiary" style={FIELD_ERROR_STYLE}>
                  已拉取 {memberState.members.length} 名成员
                  {memberState.members.length === 0 && '（为空请检查应用通讯录权限范围）'}
                  ；@ 仅对机器人所在群的成员生效
                </Typography.Text>
              )}
            </div>
          </TabPane>
        )
      })}
    </Tabs>
  )
}
