import { Banner, Button, Checkbox, CheckboxGroup, Collapse, Spin, Switch, Tag, Typography } from '@douyinfe/semi-ui'
import { ChannelTarget, CpaAuthFile } from '../api'

interface Props {
  value: ChannelTarget
  authFiles: CpaAuthFile[]
  loading: boolean
  error: string
  onChange: (value: ChannelTarget) => void
  onRetry: () => void
}

function normalizedUniqueStrings(values: readonly string[], foldCase: boolean): string[] {
  const seen = new Set<string>()
  const result: string[] = []
  for (const raw of values) {
    const value = raw.trim()
    if (!value) continue
    const key = foldCase ? value.toLowerCase() : value
    if (seen.has(key)) continue
    seen.add(key)
    result.push(value)
  }
  return result.sort((a, b) => a.localeCompare(b))
}

function normalizeProviders(values: readonly string[]): string[] {
  return normalizedUniqueStrings(values, true)
}

function normalizeAuthIDs(values: readonly string[]): string[] {
  return normalizedUniqueStrings(values, false)
}

function providerKey(value: string): string {
  return value.trim().toLowerCase()
}

function normalizeAuthFiles(files: readonly CpaAuthFile[]): CpaAuthFile[] {
  const seen = new Set<string>()
  const result: CpaAuthFile[] = []
  for (const file of files) {
    const id = file.id.trim()
    const provider = file.provider.trim()
    if (!id || !provider || seen.has(id)) continue
    seen.add(id)
    result.push({ ...file, id, provider })
  }
  return result
}

function authStatus(file: CpaAuthFile) {
  if (file.disabled) return <Tag color="red">已禁用</Tag>
  if (file.status && file.status !== 'active') return <Tag color="orange">{file.status}</Tag>
  return <Tag color="green">可用</Tag>
}

export default function ChannelTargetEditor({ value, authFiles, loading, error, onChange, onRetry }: Props) {
  const selectedSuppliers = normalizeProviders(value.suppliers)
  const selectedAuthIDs = normalizeAuthIDs(value.auth_ids)
  const normalizedFiles = normalizeAuthFiles(authFiles)
  const providers = normalizeProviders([
    ...selectedSuppliers,
    ...normalizedFiles.map((file) => file.provider),
  ])
  const knownAuthIDs = new Set(normalizedFiles.map((file) => file.id))
  const missingAuthIDs = selectedAuthIDs.filter((id) => !knownAuthIDs.has(id))
  const grouped = providers.map((provider) => ({
    provider,
    files: normalizedFiles
      .filter((file) => providerKey(file.provider) === providerKey(provider))
      .sort((a, b) => a.id.localeCompare(b.id)),
  }))

  const emit = (enabled: boolean, suppliers: readonly string[], authIDs: readonly string[]) => onChange({
    enabled,
    suppliers: normalizeProviders(suppliers),
    auth_ids: normalizeAuthIDs(authIDs),
  })
  const setSuppliers = (suppliers: string[]) => emit(value.enabled, suppliers, selectedAuthIDs)
  const setAuthIDs = (authIDs: string[]) => emit(value.enabled, selectedSuppliers, authIDs)

  const toggleAuthGroup = (ids: string[], checked: boolean) => {
    const next = new Set(selectedAuthIDs)
    for (const id of ids) {
      if (checked) next.add(id)
      else next.delete(id)
    }
    setAuthIDs(Array.from(next))
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <span>
        <Switch
          aria-label="渠道定向总开关"
          checked={value.enabled}
          onChange={(enabled) => emit(enabled, selectedSuppliers, selectedAuthIDs)}
        />{' '}
        渠道定向
      </span>

      <div aria-disabled={!value.enabled} style={{ opacity: value.enabled ? 1 : 0.55 }}>
        <Typography.Title heading={6}>AI 供应商整选</Typography.Title>
        <Typography.Paragraph size="small" type="tertiary">
          勾选供应商后，该供应商未来新增的认证文件也会自动进入候选池。
        </Typography.Paragraph>
        <CheckboxGroup
          aria-label="AI 供应商"
          direction="horizontal"
          disabled={!value.enabled}
          value={selectedSuppliers}
          onChange={(items) => setSuppliers(items.map(String))}
        >
          {providers.map((provider) => (
            <Checkbox key={provider} value={provider} aria-label={`供应商 ${provider}`}>
              {provider}
            </Checkbox>
          ))}
        </CheckboxGroup>
      </div>

      <div aria-disabled={!value.enabled} style={{ opacity: value.enabled ? 1 : 0.55 }}>
        <Typography.Title heading={6}>认证文件单选</Typography.Title>
        <Typography.Paragraph size="small" type="tertiary">
          可跨供应商混选；最终候选池与上方整选供应商取并集。
        </Typography.Paragraph>

        {missingAuthIDs.length > 0 && (
          <div style={{ marginBottom: 8 }}>
            <Typography.Text type="tertiary">已保存但 CPA 当前未返回：</Typography.Text>{' '}
            {missingAuthIDs.map((id) => <Tag key={id} color="grey">{id}</Tag>)}
          </div>
        )}

        {loading && <Spin tip="正在读取 CPA auth-files" />}
        {!loading && error && (
          <Banner
            type="danger"
            title="认证文件加载失败"
            description={<><span>{error}</span>{' '}<Button onClick={onRetry}>重试</Button></>}
          />
        )}
        {!loading && !error && grouped.length === 0 && (
          <Typography.Text type="tertiary">CPA 当前没有可展示的认证文件。</Typography.Text>
        )}
        {!loading && !error && grouped.length > 0 && (
          <Collapse defaultActiveKey={providers} keepDOM>
            {grouped.map(({ provider, files }) => {
              const ids = files.map((file) => file.id)
              const selectedCount = ids.filter((id) => selectedAuthIDs.includes(id)).length
              const allChecked = ids.length > 0 && selectedCount === ids.length
              return (
                <Collapse.Panel
                  key={provider}
                  itemKey={provider}
                  header={`${provider}（${selectedCount}/${ids.length}）`}
                  extra={(
                    <span onClick={(event) => event.stopPropagation()}>
                      <Checkbox
                        aria-label={`全选 ${provider}`}
                        disabled={!value.enabled || ids.length === 0}
                        checked={allChecked}
                        indeterminate={selectedCount > 0 && !allChecked}
                        onChange={(event) => toggleAuthGroup(ids, !!event.target.checked)}
                      >全选</Checkbox>
                    </span>
                  )}
                >
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                    {files.map((file) => (
                      <Checkbox
                        key={file.id}
                        aria-label={`认证文件 ${file.id}`}
                        disabled={!value.enabled}
                        checked={selectedAuthIDs.includes(file.id)}
                        onChange={(event) => {
                          const next = new Set(selectedAuthIDs)
                          if (event.target.checked) next.add(file.id)
                          else next.delete(file.id)
                          setAuthIDs(Array.from(next))
                        }}
                      >
                        {file.label || file.id} <Typography.Text type="tertiary">({file.id})</Typography.Text>{' '}
                        {authStatus(file)}
                      </Checkbox>
                    ))}
                  </div>
                </Collapse.Panel>
              )
            })}
          </Collapse>
        )}
      </div>
    </div>
  )
}
