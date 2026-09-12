import { useState } from 'react'
import { Banner, Button, Checkbox, Input, Spin, Switch, Tag } from '@douyinfe/semi-ui'
import { IconChevronRight, IconSearch } from '@douyinfe/semi-icons'
import { ChannelTarget, CpaAuthFile, type KeeperAuthName, type KeeperAuthNamesResponse } from '../api'
import { findKeeperAuthName } from '../keeperAuthNames'
import KeeperAuthNameEditor from './KeeperAuthNameEditor'
import { normalizeAuthIDs, normalizeChannelTarget, normalizeProviders, providerKey } from '../channelTarget'
import './ChannelTargetEditor.css'

interface Props {
  value: ChannelTarget
  authFiles: CpaAuthFile[]
  loading: boolean
  error: string
  onChange: (value: ChannelTarget) => void
  onRetry: () => void
  keeperNames?: KeeperAuthNamesResponse
  keeperLoading?: boolean
  keeperViewRevision?: number
  onRefreshNames?: () => void
  onNameSaved?: (name: KeeperAuthName) => void
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
  if (file.source === 'ai-provider' && file.status === 'configured') return <Tag color="blue" size="small">已配置</Tag>
  if (file.status && file.status !== 'active') return <Tag color="orange" size="small">{file.status}</Tag>
  return <Tag color="green" size="small">可用</Tag>
}

export default function ChannelTargetEditor({ value, authFiles, loading, error, onChange, onRetry,
  keeperNames, keeperLoading, keeperViewRevision = 0, onRefreshNames, onNameSaved }: Props) {
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
    label: normalizedFiles.find(file => providerKey(file.provider) === providerKey(provider) && file.provider_label)?.provider_label || provider,
    files: normalizedFiles
      .filter((file) => providerKey(file.provider) === providerKey(provider))
      .sort((a, b) => a.id.localeCompare(b.id)),
  }))
  const active = grouped.find((group) => providerKey(group.provider) === activeProvider) ?? grouped[0]
  const activeKey = active ? providerKey(active.provider) : ''
  const query = search.provider === activeKey ? search.text : ''
  const keeperName = (file: CpaAuthFile) => keeperNames?.status === 'ready' ? findKeeperAuthName(file, keeperNames.items) : undefined
  const visibleFiles = active?.files.filter((file) =>
    `${keeperName(file)?.display_name ?? ''} ${file.label} ${file.name ?? ''} ${file.base_url ?? ''} ${file.id}`.toLowerCase().includes(query.trim().toLowerCase()),
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

      {onRefreshNames && <div className="keeper-auth-name-status">
        <span>{keeperNames?.status === 'disabled' ? '未配置 Keeper，使用 CPA 原名称' :
          keeperNames?.status === 'unavailable' ? 'Keeper 名称暂不可用，已保留 CPA 目录与选择' :
            keeperLoading ? '正在加载 Keeper 名称…' : '认证名称来自 Keeper'}</span>
        <Button loading={keeperLoading} disabled={keeperLoading} onClick={onRefreshNames}>刷新名称</Button>
      </div>}

      {missingAuthIDs.length > 0 && (
        <div className="channel-target-missing">
          <span>{loading || error ? '已保存的凭据：' : '已保存但 CPA 当前未返回：'}</span>
          {missingAuthIDs.map((id) => <Tag key={id} color="grey">{id}</Tag>)}
        </div>
      )}

      {loading ? (
        <div className="channel-target-state">
          <div className="channel-target-loading" role="status"><Spin size="small" /><span>正在加载凭据…</span></div>
        </div>
      ) : error ? (
        <Banner className="channel-target-error" type="danger" description={
          <div className="channel-target-error-content">
            <span>凭据加载失败，已选配置已保留。</span>
            <span className="channel-target-error-detail">{error}</span>
            <Button theme="borderless" onClick={onRetry}>重新加载</Button>
          </div>
        } />
      ) : !active ? (
        <div className="channel-target-state">CPA 当前没有可展示的凭据。</div>
      ) : (
        <div className={`channel-target-browser${value.enabled ? '' : ' channel-target-disabled'}`}>
          <nav className="channel-target-sidebar" aria-label="供应商导航">
            <div className="channel-target-sidebar-title"><span>AI 供应商</span><span>{providers.length}</span></div>
            <div className="channel-target-provider-list">
              {grouped.map(({ provider, label, files }) => (
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
                    <strong title={provider}>{label}</strong>
                    <small>{supplierSelected(provider) ? '已整选 · 自动包含新增' :
                      `${files.filter((file) => selectedAuthIDs.includes(file.id)).length} 项指定 / ${files.length} 个凭据`}</small>
                  </span>
                  <IconChevronRight aria-hidden="true" />
                </Button>
              ))}
            </div>
            <p className="channel-target-sidebar-hint">点击供应商查看凭据。<br />切换时保留所有选择。</p>
          </nav>

          <section className="channel-target-detail" aria-label={`${active.provider} 凭据`}>
            <div className="channel-target-detail-heading">
              <div><h3>{active.label}</h3><span>{active.files.length} 个凭据</span></div>
              <Checkbox aria-label={`供应商 ${active.provider}`} disabled={!value.enabled}
                checked={supplierSelected(active.provider)}
                onChange={(event) => emit(value.enabled,
                  event.target.checked ? [...selectedSuppliers, active.provider] :
                    selectedSuppliers.filter((item) => providerKey(item) !== activeKey),
                  selectedAuthIDs,
                )}>整选此供应商</Checkbox>
            </div>
            <p className="channel-target-help">整选包含该供应商及其后续新增凭据；下方指定凭据与整选范围取并集。</p>
            <Input prefix={<IconSearch />} aria-label="搜索凭据" placeholder="搜索名称、地址或 ID"
              showClear disabled={!value.enabled} value={query}
              onChange={(text) => setSearch({ provider: activeKey, text })} />
            <div className="channel-target-file-toolbar">
              <Checkbox aria-label={`全选 ${active.provider}`} disabled={!value.enabled || visibleIDs.length === 0}
                checked={allVisibleChecked} indeterminate={visibleSelectedCount > 0 && !allVisibleChecked}
                onChange={(event) => toggleAuthGroup(visibleIDs, !!event.target.checked)}>
                {query.trim() ? '全选搜索结果' : '全选当前凭据'}（{visibleIDs.length}）
              </Checkbox>
              <span>状态</span>
            </div>
            <div className="channel-target-files">
              {visibleFiles.length === 0 ? (
                <div className="channel-target-no-results">{query.trim() ? '没有匹配的凭据' : '该供应商当前没有凭据。'}</div>
              ) : visibleFiles.map((file) => (
                <div key={file.id} className={`channel-target-file${selectedAuthIDs.includes(file.id) ? ' channel-target-file-selected' : ''}`}>
                  <Checkbox aria-label={`凭据 ${file.id}`} disabled={!value.enabled}
                    checked={selectedAuthIDs.includes(file.id)}
                    onChange={(event) => toggleAuthGroup([file.id], !!event.target.checked)} />
                  <div className="channel-target-file-name">
                    <strong>{keeperName(file)?.display_name || file.label || file.name || file.id}</strong>
                    <code title={file.id}>{file.id}</code>
                    {file.name && file.name !== file.id && <span className="channel-target-original-name">{file.name}</span>}
                    {file.base_url && <span className="channel-target-credential-url">{file.base_url}</span>}
                  </div>
                  <div className="channel-target-file-badges">
                    <Tag size="small" color={file.source === 'ai-provider' ? 'blue' : 'grey'}>{file.source === 'ai-provider' ? 'AI Providers' : '认证文件'}</Tag>
                    {supplierSelected(active.provider) && <span>整选已覆盖</span>}
                    {authStatus(file)}
                  </div>
                  {file.source === 'auth-file' && file.provider === 'codex' && onNameSaved && (
                    <div className="channel-target-name-edit">
                      {keeperName(file) ? <KeeperAuthNameEditor authIndex={file.auth_index!} name={keeperName(file)!}
                        viewRevision={keeperViewRevision} onSaved={onNameSaved} /> :
                        keeperNames?.status === 'ready' ? <span>未找到唯一匹配的 Keeper 身份，无法同步名称</span> : null}
                    </div>
                  )}
                </div>
              ))}
            </div>
          </section>
        </div>
      )}

      <div className="channel-target-summary">
        <strong>{selectedSuppliers.length} 个整选供应商 + {selectedAuthIDs.length} 个指定凭据</strong>
        <span>认证文件与 AI Providers 统一按勾选生效；可用性由 CPA 调度判断。</span>
      </div>
    </div>
  )
}
