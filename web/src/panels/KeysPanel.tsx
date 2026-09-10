import { useEffect, useRef, useState } from 'react'
import { Button, Card, Table, Modal, Input, Switch, Tag, Tabs, TabPane, Toast, Typography } from '@douyinfe/semi-ui'
import { api, ChannelTarget, CpaAuthFile, KeyBinding, RuleSet, StateResponse, listCpaCredentials } from '../api'
import { normalizeChannelTarget } from '../channelTarget'
import ChannelTargetEditor from '../components/ChannelTargetEditor'
import RuleSetEditor from '../components/RuleSetEditor'
import ApiKeySelect from '../components/ApiKeySelect'
import { maskKey } from '../keyOptions'
import { keeperStatusText, type KeyOptionsState } from '../useKeyOptions'

const EMPTY_RULES: RuleSet = { global: '', claude: '', codex: '', openai: '' }
const EMPTY_CHANNEL_TARGET: ChannelTarget = { enabled: false, suppliers: [], auth_ids: [] }

function normalizeBinding(binding: KeyBinding): KeyBinding {
  return {
    ...binding,
    blocked: !!binding.blocked,
    fast_allowed: binding.fast_allowed ?? true,
    channel_target: normalizeChannelTarget(binding.channel_target),
    rules: { ...binding.rules },
  }
}

function channelTargetSummary(binding: KeyBinding): string {
  const target = binding.channel_target
  if (!target?.enabled) return '关闭'
  return `${target.suppliers?.length ?? 0} 个供应商 · ${target.auth_ids?.length ?? 0} 个凭据`
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
  keyOptions: KeyOptionsState
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
  const binding = { ...editing, key }
  if (binding.channel_target) binding.channel_target = normalizeChannelTarget(binding.channel_target)
  return {
    binding,
    deleteKey: previous && previous !== key ? previous : '',
  }
}

export default function KeysPanel({ state, onSaved, keyOptions }: Props) {
  const [editing, setEditing] = useState<KeyBinding | null>(null)
  // originalKey tracks the binding being edited; empty means "create new".
  // Used so the upsert warning only shows when the selected key collides with
  // a *different* existing binding (M4).
  const [originalKey, setOriginalKey] = useState('')
  const [saving, setSaving] = useState(false)
  const [authFiles, setAuthFiles] = useState<CpaAuthFile[]>([])
  const [authLoading, setAuthLoading] = useState(false)
  const [authError, setAuthError] = useState('')
  const [aliasSyncing, setAliasSyncing] = useState(false)
  const [aliasNotice, setAliasNotice] = useState('')
  const editRevision = useRef(0)
  const syncRequest = useRef(0)

  const invalidateAliasSync = () => {
    editRevision.current++
    syncRequest.current++
    setAliasSyncing(false)
    setAliasNotice('')
  }

  useEffect(() => () => { editRevision.current++; syncRequest.current++ }, [])

  const syncAlias = async () => {
    const key = editing?.key.trim()
    if (!key || aliasSyncing || keyOptions.keeper?.status === 'disabled') return
    const revision = editRevision.current
    const request = ++syncRequest.current
    const isCurrent = () => revision === editRevision.current && request === syncRequest.current
    setAliasSyncing(true)
    setAliasNotice('')
    try {
      const result = await keyOptions.refreshAliases()
      if (!isCurrent()) return
      if (result.status !== 'ready') {
        setAliasNotice(keeperStatusText(result) + '，已保留当前内容')
        return
      }
      const match = result.items.find(item => item.key === key)
      if (!match) {
        setAliasNotice('Keeper 中没有对应的 API Key，已保留当前内容')
        return
      }
      const alias = match.alias.trim()
      if (!alias) {
        setAliasNotice('Keeper 中尚未设置别名，已保留当前内容')
        return
      }
      setEditing(current => current?.key.trim() === key ? { ...current, alias } : current)
      setAliasNotice('已填入，保存绑定后生效')
    } catch {
      if (isCurrent()) setAliasNotice('读取 Keeper 失败，已保留当前内容')
    } finally {
      if (isCurrent()) setAliasSyncing(false)
    }
  }

  const loadAuthFiles = () => {
    setAuthLoading(true)
    setAuthError('')
    listCpaCredentials()
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
    invalidateAliasSync()
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
    invalidateAliasSync()
    setOriginalKey('')
    setEditing(normalizeBinding({
      key: '', alias: '', enabled: true, blocked: false,
      fast_allowed: true,
      channel_target: { ...EMPTY_CHANNEL_TARGET },
      rules: { ...EMPTY_RULES },
    }))
  }

  const openEdit = (b: KeyBinding) => {
    invalidateAliasSync()
    setOriginalKey(b.key)
    setEditing(normalizeBinding(b))
  }

  const closeEdit = () => {
    invalidateAliasSync()
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
        style={{ maxWidth: 'calc(100vw - 32px)' }}
      >
        {editing && (
          <Tabs type="line" keepDOM>
            <TabPane tab="基础" itemKey="basic">
              <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                <ApiKeySelect
                  source={keyOptions}
                  allowCreate
                  value={editing.key}
                  onChange={(key) => { invalidateAliasSync(); setEditing(current => current ? { ...current, key } : current) }}
                />
                <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
                  <Input
                    aria-label="绑定别名"
                    style={{ flex: '1 1 220px', minWidth: 0 }}
                    placeholder="别名（可选）"
                    value={editing.alias}
                    onChange={(alias) => { invalidateAliasSync(); setEditing(current => current ? { ...current, alias } : current) }}
                  />
                  <Button
                    loading={aliasSyncing}
                    disabled={!editing.key.trim() || keyOptions.keeper?.status === 'disabled' || aliasSyncing || saving}
                    onClick={() => { void syncAlias() }}
                  >从 Keeper 同步</Button>
                </div>
                <Typography.Text size="small" type="tertiary" aria-live="polite">
                  {aliasNotice || (!editing.key.trim() ? '选择或输入 Key 后可同步别名' : keyOptions.keeper?.status === 'disabled' ? '请在 CPA 插件设置中配置 Keeper 后同步' : '同步会替换输入框内容，保存绑定后生效')}
                </Typography.Text>
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
