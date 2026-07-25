import { useEffect, useState } from 'react'
import { Button, Card, Table, Modal, Input, Select, Switch, Tag, Toast, Typography } from '@douyinfe/semi-ui'
import { api, KeyBinding, RuleSet, StateResponse, listCpaApiKeys } from '../api'
import RuleSetEditor from '../components/RuleSetEditor'

const EMPTY_RULES: RuleSet = { global: '', claude: '', codex: '', openai: '' }

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

export default function KeysPanel({ state, onSaved }: Props) {
  const [cpaKeys, setCpaKeys] = useState<string[]>([])
  const [editing, setEditing] = useState<KeyBinding | null>(null)
  // originalKey tracks the binding being edited; empty means "create new".
  // Used so the upsert warning only shows when the selected key collides with
  // a *different* existing binding (M4).
  const [originalKey, setOriginalKey] = useState('')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    listCpaApiKeys().then(setCpaKeys).catch(() => setCpaKeys([]))
  }, [])

  const save = () => {
    if (!editing) return
    setSaving(true)
    api.postKey(editing)
      .then((s) => { onSaved(s); setEditing(null); setOriginalKey(''); Toast.success('绑定已保存') })
      .catch((e: Error) => Toast.error(e.message))
      .finally(() => setSaving(false))
  }

  const openCreate = () => {
    setOriginalKey('')
    setEditing({ key: '', alias: '', enabled: true, rules: EMPTY_RULES })
  }

  const openEdit = (b: KeyBinding) => {
    setOriginalKey(b.key)
    setEditing({ ...b, rules: { ...b.rules } })
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

  const remove = (b: KeyBinding) => {
    Modal.confirm({
      title: '删除绑定',
      content: `确认删除 ${b.alias || maskKey(b.key)} 的绑定？`,
      onOk: () => api.deleteKey(b.key).then(onSaved).catch((e: Error) => Toast.error(e.message)),
    })
  }

  return (
    <Card title="指定 Key 追加规则集（串联跑在顶层规则之后）" style={{ margin: 16 }}
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
          { title: '启用', dataIndex: 'enabled', render: (on: boolean, b: KeyBinding) => <Switch checked={on} onChange={(v) => toggleEnabled(b, v)} /> },
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
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <Select
              style={{ width: '100%' }}
              filter
              allowCreate
              placeholder="选择或输入 API key"
              value={editing.key || undefined}
              onChange={(v) => setEditing({ ...editing, key: String(v) })}
            >
              {cpaKeys.map((k) => (
                <Select.Option key={k} value={k}>{maskKey(k)}</Select.Option>
              ))}
            </Select>
            <Input placeholder="别名（可选）" value={editing.alias}
              onChange={(v) => setEditing({ ...editing, alias: v })} />
            <div>
              <Typography.Text size="small" type="tertiary">
                追加规则集（与规则管理同构；本 key 的请求在顶层规则跑完后接力执行）
              </Typography.Text>
              <RuleSetEditor value={editing.rules} onChange={(r) => setEditing({ ...editing, rules: r })} />
            </div>
            <div>
              <Switch checked={editing.enabled} onChange={(v) => setEditing({ ...editing, enabled: v })} /> 启用
              {isKeyCollision && (
                <Tag color="orange" style={{ marginLeft: 8 }}>同 key 已存在，保存将覆盖</Tag>
              )}
            </div>
          </div>
        )}
      </Modal>
    </Card>
  )
}
