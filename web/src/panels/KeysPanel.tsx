import { useEffect, useState } from 'react'
import { Button, Card, Table, Modal, Input, Select, Switch, Tag, Tabs, TabPane, Toast, Typography } from '@douyinfe/semi-ui'
import { api, ChannelTarget, CpaAuthFile, KeyBinding, RuleSet, StateResponse, listCpaApiKeys, listCpaAuthFiles } from '../api'
import ChannelTargetEditor from '../components/ChannelTargetEditor'
import RuleSetEditor from '../components/RuleSetEditor'

const EMPTY_RULES: RuleSet = { global: '', claude: '', codex: '', openai: '' }
const EMPTY_CHANNEL_TARGET: ChannelTarget = { enabled: false, suppliers: [], auth_ids: [] }

function normalizeBinding(binding: KeyBinding): KeyBinding {
  return {
    ...binding,
    blocked: !!binding.blocked,
    fast_allowed: binding.fast_allowed ?? true,
    channel_target: {
      ...EMPTY_CHANNEL_TARGET,
      ...binding.channel_target,
      suppliers: [...(binding.channel_target?.suppliers ?? [])],
      auth_ids: [...(binding.channel_target?.auth_ids ?? [])],
    },
    rules: { ...binding.rules },
  }
}

function channelTargetSummary(binding: KeyBinding): string {
  const target = binding.channel_target
  if (!target?.enabled) return '关闭'
  return `${target.suppliers?.length ?? 0} 个供应商 · ${target.auth_ids?.length ?? 0} 个认证文件`
}

function maskKey(key: string): string {
  if (key.length <= 10) return key
  return `${key.slice(0, 6)}…${key.slice(-4)}`
}

function ruleSummary(b: KeyBinding): string {
  const parts: string[] = []
  const count = (dsl: string) => (dsl ? dsl.split(';').filter(Boolean).length : 0)
  if (b.rules.global) parts.push(`全局 ${count(b.rules.global)} 条`)
  if (b.rules.claude) parts.push(`Claude ${count(b.rules.claude)} 条`)
  if (b.rules.codex) parts.push(`Codex ${count(b.rules.codex)} 条`)
  if (b.rules.openai) parts.push(`OpenAI ${count(b.rules.openai)} 条`)
  return parts.join(' · ') || '—'
}

interface Props {
  state: StateResponse
  onSaved: (s: StateResponse) => void
}

/**
 * 保存一条绑定时要执行的动作。
 *
 * 编辑态改了 key 属于「重命名」：postKey 按新 key upsert，旧绑定不会被动过，
 * 只调 postKey 会留下两条同时生效的绑定，所以要显式删掉旧的。
 */
export interface KeySavePlan {
  /** 落库的绑定（key 已 trim）。 */
  binding: KeyBinding
  /** 需要删除的旧 key；空串表示无需删除。 */
  deleteKey: string
}

export function planKeySave(originalKey: string, editing: KeyBinding): KeySavePlan | null {
  const key = editing.key.trim()
  if (!key) return null
  const previous = originalKey.trim()
  return {
    binding: { ...editing, key },
    deleteKey: previous && previous !== key ? previous : '',
  }
}

export default function KeysPanel({ state, onSaved }: Props) {
  const [cpaKeys, setCpaKeys] = useState<string[]>([])
  const [editing, setEditing] = useState<KeyBinding | null>(null)
  // originalKey tracks the binding being edited; empty means "create new".
  // Used so the upsert warning only shows when the selected key collides with
  // a *different* existing binding (M4).
  const [originalKey, setOriginalKey] = useState('')
  const [saving, setSaving] = useState(false)
  const [authFiles, setAuthFiles] = useState<CpaAuthFile[]>([])
  const [authLoading, setAuthLoading] = useState(false)
  const [authError, setAuthError] = useState('')

  useEffect(() => {
    listCpaApiKeys().then(setCpaKeys).catch(() => setCpaKeys([]))
  }, [])

  const loadAuthFiles = () => {
    setAuthLoading(true)
    setAuthError('')
    listCpaAuthFiles()
      .then(setAuthFiles)
      .catch((error: Error) => {
        setAuthFiles([])
        setAuthError(error.message)
      })
      .finally(() => setAuthLoading(false))
  }

  useEffect(() => {
    if (editing !== null) loadAuthFiles()
  }, [editing !== null])

  const save = () => {
    if (!editing) return
    const plan = planKeySave(originalKey, editing)
    if (!plan) {
      Toast.error('请选择或输入 API key')
      return
    }
    setSaving(true)
    api.postKey(plan.binding)
      .then((s) => (plan.deleteKey ? api.deleteKey(plan.deleteKey) : Promise.resolve(s)))
      .then((s) => {
        onSaved(s)
        setEditing(null)
        setOriginalKey('')
        Toast.success(plan.deleteKey ? '绑定已重命名并保存' : '绑定已保存')
      })
      .catch((e: Error) => Toast.error(e.message))
      .finally(() => setSaving(false))
  }

  const openCreate = () => {
    setOriginalKey('')
    setEditing(normalizeBinding({
      key: '', alias: '', enabled: true, blocked: false,
      fast_allowed: true,
      channel_target: { ...EMPTY_CHANNEL_TARGET },
      rules: { ...EMPTY_RULES },
    }))
  }

  const openEdit = (b: KeyBinding) => {
    setOriginalKey(b.key)
    setEditing(normalizeBinding(b))
  }

  const closeEdit = () => {
    setEditing(null)
    setOriginalKey('')
  }

  const isKeyCollision =
    !!editing?.key &&
    state.key_bindings.some((b) => b.key === editing.key && b.key !== originalKey)

  const toggleEnabled = (b: KeyBinding, enabled: boolean) => {
    api.patchKey(b.key, { enabled }).then(onSaved).catch((e: Error) => Toast.error(e.message))
  }

  const toggleBlocked = (b: KeyBinding, blocked: boolean) => {
    api.patchKey(b.key, { blocked }).then(onSaved).catch((e: Error) => Toast.error(e.message))
  }

  const remove = (b: KeyBinding) => {
    Modal.confirm({
      title: '删除绑定',
      content: `确认删除 ${b.alias || maskKey(b.key)} 的绑定？`,
      onOk: () => api.deleteKey(b.key).then(onSaved).catch((e: Error) => Toast.error(e.message)),
    })
  }

  return (
    <Card title="Key 绑定策略" style={{ margin: 16 }}
      headerExtraContent={
        <Button theme="solid" onClick={openCreate}>
          + 新增绑定
        </Button>
      }>
      <Table
        dataSource={state.key_bindings}
        pagination={false}
        columns={[
          { title: 'API Key', dataIndex: 'key', render: (k: string) => <Typography.Text code>{maskKey(k)}</Typography.Text> },
          { title: '别名', dataIndex: 'alias' },
          { title: '追加规则', dataIndex: 'rules', render: (_: unknown, b: KeyBinding) => ruleSummary(b) },
          {
            title: '渠道定向',
            dataIndex: 'channel_target',
            render: (_: unknown, b: KeyBinding) => channelTargetSummary(b),
          },
          {
            title: 'Fast',
            dataIndex: 'fast_allowed',
            render: (_: unknown, b: KeyBinding) => b.fast_allowed === false
              ? <Tag color="grey">关闭</Tag>
              : <Tag color="green">允许</Tag>,
          },
          {
            title: '启用规则',
            dataIndex: 'enabled',
            render: (on: boolean, b: KeyBinding) => (
              <Switch
                aria-label={`启用规则：${b.alias || maskKey(b.key)}`}
                checked={on}
                onChange={(v) => toggleEnabled(b, v)}
              />
            ),
          },
          {
            title: '禁止访问',
            dataIndex: 'blocked',
            render: (blocked: boolean, b: KeyBinding) => (
              <Switch
                aria-label={`禁止访问：${b.alias || maskKey(b.key)}`}
                checked={!!blocked}
                onChange={(v) => toggleBlocked(b, v)}
              />
            ),
          },
          { title: '', dataIndex: 'ops', render: (_: unknown, b: KeyBinding) => (
            <>
              <Button size="small" onClick={() => openEdit(b)}>编辑</Button>{' '}
              <Button size="small" type="danger" onClick={() => remove(b)}>删除</Button>
            </>
          ) },
        ]}
      />
      <Typography.Paragraph size="small" type="tertiary" style={{ marginTop: 8 }}>
        Key 来源：GET /v0/management/api-keys（下拉选择，也可手动输入未列出的 key）
      </Typography.Paragraph>

      <Modal
        title={originalKey ? `编辑绑定：${editing?.alias || maskKey(originalKey)}` : '新增绑定'}
        visible={editing !== null}
        onCancel={closeEdit}
        onOk={save}
        confirmLoading={saving}
        width={860}
      >
        {editing && (
          <Tabs type="line" keepDOM>
            <TabPane tab="基础" itemKey="basic">
              <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                <Select
                  style={{ width: '100%' }}
                  filter
                  allowCreate
                  placeholder="选择或输入 API key"
                  value={editing.key || undefined}
                  onChange={(value) => setEditing({ ...editing, key: String(value) })}
                  optionList={cpaKeys.map((key) => ({ value: key, label: maskKey(key) }))}
                />
                <Input
                  placeholder="别名（可选）"
                  value={editing.alias}
                  onChange={(alias) => setEditing({ ...editing, alias })}
                />
                <div style={{ display: 'flex', alignItems: 'center', gap: 16, flexWrap: 'wrap' }}>
                  <span>
                    <Switch
                      aria-label="编辑绑定：启用规则"
                      checked={editing.enabled}
                      onChange={(enabled) => setEditing({ ...editing, enabled })}
                    />{' '}启用规则
                  </span>
                  <span>
                    <Switch
                      aria-label="编辑绑定：禁止访问"
                      checked={!!editing.blocked}
                      onChange={(blocked) => setEditing({ ...editing, blocked })}
                    />{' '}禁止访问
                  </span>
                  <span>
                    <Switch
                      aria-label="编辑绑定：Fast 允许"
                      checked={editing.fast_allowed ?? true}
                      onChange={(fast_allowed) => setEditing({ ...editing, fast_allowed })}
                    />{' '}Fast 允许
                  </span>
                  {isKeyCollision && <Tag color="orange">同 key 已存在，保存将覆盖</Tag>}
                </div>
              </div>
            </TabPane>
            <TabPane tab="渠道定向" itemKey="channel-target">
              <ChannelTargetEditor
                value={editing.channel_target ?? { ...EMPTY_CHANNEL_TARGET }}
                authFiles={authFiles}
                loading={authLoading}
                error={authError}
                onRetry={loadAuthFiles}
                onChange={(channel_target) => setEditing({ ...editing, channel_target })}
              />
            </TabPane>
            <TabPane tab="规则集" itemKey="rules">
              <Typography.Paragraph size="small" type="tertiary">
                追加规则集在顶层规则之后执行；渠道定向开启时本页规则跳过。
              </Typography.Paragraph>
              <RuleSetEditor value={editing.rules} onChange={(rules) => setEditing({ ...editing, rules })} />
            </TabPane>
          </Tabs>
        )}
      </Modal>
    </Card>
  )
}
