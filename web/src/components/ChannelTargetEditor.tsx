import { useState } from 'react'
import { Banner, Button, Checkbox, Input, Spin, Switch, Tag } from '@douyinfe/semi-ui'
import { IconChevronRight, IconSearch } from '@douyinfe/semi-icons'
import { ChannelTarget, CpaAuthFile } from '../api'
import { normalizeAuthIDs, normalizeChannelTarget, normalizeProviders, providerKey } from '../channelTarget'
import './ChannelTargetEditor.css'

interface Props {
  value: ChannelTarget
  authFiles: CpaAuthFile[]
  loading: boolean
  error: string
  onChange: (value: ChannelTarget) => void
  onRetry: () => void
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
  if (file.disabled) return <Tag color="red" size="small">已禁用</Tag>
  if (file.status && file.status !== 'active') return <Tag color="orange" size="small">{file.status}</Tag>
  return <Tag color="green" size="small">可用</Tag>
}

export default function ChannelTargetEditor({ value, authFiles, loading, error, onChange, onRetry }: Props) {
  const [activeProvider, setActiveProvider] = useState('')
  const [search, setSearch] = useState({ provider: '', text: '' })
  const selectedSuppliers = normalizeProviders(value.suppliers)
  const selectedAuthIDs = normalizeAuthIDs(value.auth_ids)
  const normalizedFiles = normalizeAuthFiles(authFiles)
  const providers = normalizeProviders([...selectedSuppliers, ...normalizedFiles.map((file) => file.provider)])
  const knownAuthIDs = new Set(normalizedFiles.map((file) => file.id))
  const missingAuthIDs = selectedAuthIDs.filter((id) => !knownAuthIDs.has(id))
  const grouped = providers.map((provider) => ({
    provider,
    files: normalizedFiles
      .filter((file) => providerKey(file.provider) === providerKey(provider))
      .sort((a, b) => a.id.localeCompare(b.id)),
  }))
  const active = grouped.find((group) => providerKey(group.provider) === activeProvider) ?? grouped[0]
  const activeKey = active ? providerKey(active.provider) : ''
  const query = search.provider === activeKey ? search.text : ''
  const visibleFiles = active?.files.filter((file) =>
    `${file.label} ${file.id}`.toLowerCase().includes(query.trim().toLowerCase()),
  ) ?? []
  const visibleIDs = visibleFiles.map((file) => file.id)
  const visibleSelectedCount = visibleIDs.filter((id) => selectedAuthIDs.includes(id)).length
  const allVisibleChecked = visibleIDs.length > 0 && visibleSelectedCount === visibleIDs.length
  const supplierSelected = (provider: string) => selectedSuppliers.some((item) => providerKey(item) === providerKey(provider))
  const emit = (enabled: boolean, suppliers: readonly string[], authIDs: readonly string[]) =>
    onChange(normalizeChannelTarget({ enabled, suppliers, auth_ids: authIDs }))
  const toggleAuthGroup = (ids: string[], checked: boolean) => {
    const next = new Set(selectedAuthIDs)
    for (const id of ids) {
      if (checked) next.add(id)
      else next.delete(id)
    }
    emit(value.enabled, selectedSuppliers, Array.from(next))
  }

  return (
    <div className="channel-target-editor">
      <div className="channel-target-enable">
        <div>
          <strong>渠道定向</strong>
          <p>{value.enabled ? '仅在指定范围内调度；候选池为空时请求失败。' : '已关闭定向，已选配置保留。'}</p>
        </div>
        <Switch aria-label="渠道定向总开关" checked={value.enabled}
          onChange={(enabled) => emit(enabled, selectedSuppliers, selectedAuthIDs)} />
      </div>

      {missingAuthIDs.length > 0 && (
        <div className="channel-target-missing">
          <span>{loading || error ? '已保存的认证文件：' : '已保存但 CPA 当前未返回：'}</span>
          {missingAuthIDs.map((id) => <Tag key={id} color="grey">{id}</Tag>)}
        </div>
      )}

      {loading ? (
        <div className="channel-target-state">
          <div className="channel-target-loading" role="status"><Spin size="small" /><span>正在加载认证文件…</span></div>
        </div>
      ) : error ? (
        <Banner className="channel-target-error" type="danger" description={
          <div className="channel-target-error-content">
            <span>认证文件加载失败，已选配置已保留。</span>
            <span className="channel-target-error-detail">{error}</span>
            <Button theme="borderless" onClick={onRetry}>重新加载</Button>
          </div>
        } />
      ) : !active ? (
        <div className="channel-target-state">CPA 当前没有可展示的认证文件。</div>
      ) : (
        <div className={`channel-target-browser${value.enabled ? '' : ' channel-target-disabled'}`}>
          <nav className="channel-target-sidebar" aria-label="供应商导航">
            <div className="channel-target-sidebar-title"><span>AI 供应商</span><span>{providers.length}</span></div>
            <div className="channel-target-provider-list">
              {grouped.map(({ provider, files }) => (
                <Button key={provider} theme="borderless" type="tertiary"
                  aria-label={`查看供应商 ${provider}`}
                  aria-pressed={providerKey(provider) === activeKey}
                  className={`channel-target-provider${providerKey(provider) === activeKey ? ' channel-target-provider-active' : ''}`}
                  onClick={() => {
                    setActiveProvider(providerKey(provider))
                    setSearch({ provider: providerKey(provider), text: '' })
                  }}>
                  <span className="channel-target-avatar" aria-hidden="true">{provider.slice(0, 1).toUpperCase()}</span>
                  <span className="channel-target-provider-copy">
                    <strong title={provider}>{provider}</strong>
                    <small>{supplierSelected(provider) ? '已整选 · 自动包含新增' :
                      `${files.filter((file) => selectedAuthIDs.includes(file.id)).length} 项指定 / ${files.length} 个文件`}</small>
                  </span>
                  <IconChevronRight aria-hidden="true" />
                </Button>
              ))}
            </div>
            <p className="channel-target-sidebar-hint">点击供应商查看文件。<br />切换时保留所有选择。</p>
          </nav>

          <section className="channel-target-detail" aria-label={`${active.provider} 认证文件`}>
            <div className="channel-target-detail-heading">
              <div><h3>{active.provider}</h3><span>{active.files.length} 个认证文件</span></div>
              <Checkbox aria-label={`供应商 ${active.provider}`} disabled={!value.enabled}
                checked={supplierSelected(active.provider)}
                onChange={(event) => emit(value.enabled,
                  event.target.checked ? [...selectedSuppliers, active.provider] :
                    selectedSuppliers.filter((item) => providerKey(item) !== activeKey),
                  selectedAuthIDs,
                )}>整选此供应商</Checkbox>
            </div>
            <p className="channel-target-help">整选包含该供应商及其后续新增凭据；下方指定文件与整选范围取并集。</p>
            <Input prefix={<IconSearch />} aria-label="搜索认证文件" placeholder="搜索文件名称或 ID"
              showClear disabled={!value.enabled} value={query}
              onChange={(text) => setSearch({ provider: activeKey, text })} />
            <div className="channel-target-file-toolbar">
              <Checkbox aria-label={`全选 ${active.provider}`} disabled={!value.enabled || visibleIDs.length === 0}
                checked={allVisibleChecked} indeterminate={visibleSelectedCount > 0 && !allVisibleChecked}
                onChange={(event) => toggleAuthGroup(visibleIDs, !!event.target.checked)}>
                {query.trim() ? '全选搜索结果' : '全选当前文件'}（{visibleIDs.length}）
              </Checkbox>
              <span>状态</span>
            </div>
            <div className="channel-target-files">
              {visibleFiles.length === 0 ? (
                <div className="channel-target-no-results">{query.trim() ? '没有匹配的认证文件' : '该供应商当前没有认证文件。'}</div>
              ) : visibleFiles.map((file) => (
                <div key={file.id} className={`channel-target-file${selectedAuthIDs.includes(file.id) ? ' channel-target-file-selected' : ''}`}>
                  <Checkbox aria-label={`认证文件 ${file.id}`} disabled={!value.enabled}
                    checked={selectedAuthIDs.includes(file.id)}
                    onChange={(event) => toggleAuthGroup([file.id], !!event.target.checked)} />
                  <div className="channel-target-file-name">
                    <strong>{file.label || file.id}</strong>
                    <code title={file.id}>{file.id}</code>
                  </div>
                  <div className="channel-target-file-badges">
                    {supplierSelected(active.provider) && <span>整选已覆盖</span>}
                    {authStatus(file)}
                  </div>
                </div>
              ))}
            </div>
          </section>
        </div>
      )}

      <div className="channel-target-summary">
        <strong>{selectedSuppliers.length} 个整选供应商 + {selectedAuthIDs.length} 个指定文件</strong>
        <span>保存后应用于当前 Key</span>
      </div>
    </div>
  )
}
