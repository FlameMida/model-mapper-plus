import { useEffect, useRef, useState } from 'react'
import { Banner, Button, Card, Input, Select, Tag } from '@douyinfe/semi-ui'
import { api, listCpaCredentials, type AuditChange, type AuditOperation, type AuditPage } from '../api'
import { findKeeperAuthName } from '../keeperAuthNames'
import {
  AUDIT_MODULES, actionLabel, arrayDiff, diffLines, errorText, fieldLabel, formatValue,
  idListDiff, isRedacted, moduleMeta, moduleOf, resolveName, summarizeChanges,
} from '../auditDisplay'
import './AuditPanel.css'

const OUTCOME_META: Record<AuditOperation['outcome'], { label: string; tone: 'green' | 'red' | 'grey' }> = {
  running: { label: '进行中', tone: 'grey' },
  succeeded: { label: '成功', tone: 'green' },
  failed: { label: '失败', tone: 'red' },
  unknown: { label: '结果未确认', tone: 'grey' },
}

const formatTime = (value: string) => new Date(value).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false })
const formatClock = (value: string) => new Date(value).toLocaleTimeString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false })
const durationMs = (item: AuditOperation) =>
  item.finished_at ? Math.max(0, new Date(item.finished_at).getTime() - new Date(item.started_at).getTime()) : null

/** Key/规则等单值字段：脱敏标记、旧 → 新或两列原文。 */
function ScalarField({ field, change }: { field: string; change: AuditChange }) {
  if (isRedacted(change.before) || isRedacted(change.after)) {
    return <div className="audit-field-body">
      <span className="audit-pill mute">变更前：已脱敏</span>
      <span className="audit-arrow">→</span>
      <span className="audit-pill mute">变更后：已脱敏</span>
    </div>
  }
  if (typeof change.before === 'boolean' || typeof change.after === 'boolean') {
    const pill = (value: unknown, invert: boolean) => value == null ? <span className="audit-pill mute">—</span>
      : <span className={`audit-pill ${value ? (invert ? 'danger' : 'success') : 'mute'}`}>{value ? '开' : '关'}</span>
    return <div className="audit-field-body">
      {pill(change.before, field === 'blocked')}
      <span className="audit-arrow">→</span>
      {pill(change.after, field === 'blocked')}
    </div>
  }
  if (typeof change.before === 'string' && typeof change.after === 'string') {
    return <div className="audit-field-body audit-scalar">
      <code>{change.before || '（空）'}</code>
      <span className="audit-arrow">→</span>
      <code>{change.after || '（空）'}</code>
    </div>
  }
  return <div className="audit-json">
    <div><strong>变更前</strong><pre>{formatValue(change.before)}</pre></div>
    <div><strong>变更后</strong><pre>{formatValue(change.after)}</pre></div>
  </div>
}

/** 渠道定向供应商/认证：增删保留行，名称按「事件快照 → 实时富化 → 原始 ID」解析。 */
function ChannelListField({ change, item, live }: {
  change: AuditChange
  item: AuditOperation
  live?: Record<string, string>
}) {
  const diff = arrayDiff(change.before, change.after)
  const rows = [
    ...diff.removed.map(id => ({ sign: 'del' as const, id })),
    ...diff.added.map(id => ({ sign: 'add' as const, id })),
    ...diff.kept.map(id => ({ sign: 'keep' as const, id })),
  ]
  return <div className="audit-diff-list">
    {rows.map(({ sign, id }) => {
      const resolved = resolveName(id, item.labels, live)
      return <div key={`${sign}:${id}`} className={`audit-diff-row ${sign}`}>
        <span className={`audit-sign ${sign}`}>{sign === 'add' ? '新增' : sign === 'del' ? '移除' : '保留'}</span>
        <span className="audit-name">{resolved.name ?? id}</span>
        {resolved.name && resolved.name !== id && <span className="audit-id">{id}</span>}
      </div>
    })}
  </div>
}

/** Key 级通知 [{id,name,enabled}]：按 id 对齐，名称/开关变化在保留行内呈现。 */
function NotificationListField({ change }: { change: AuditChange }) {
  const diff = idListDiff(change.before, change.after)
  const rows: { sign: 'add' | 'del' | 'keep'; text: string; detail: string }[] = [
    ...diff.removed.map(row => ({ sign: 'del' as const, text: row.name || row.id || '未命名', detail: `${row.enabled ? '启用' : '停用'} · ${row.id}` })),
    ...diff.added.map(row => ({ sign: 'add' as const, text: row.name || row.id || '未命名', detail: `${row.enabled ? '启用' : '停用'} · ${row.id}` })),
    ...diff.kept.map(({ before, after }) => ({
      sign: 'keep' as const,
      text: after.name || after.id || '未命名',
      detail: [
        before.name !== after.name ? `名称「${before.name || '（空）'}」→「${after.name || '（空）'}」` : '',
        before.enabled !== after.enabled ? `${before.enabled ? '启用' : '停用'}→${after.enabled ? '启用' : '停用'}` : '',
      ].filter(Boolean).join(' · ') || `无字段变化 · ${after.id}`,
    })),
  ]
  return <div className="audit-diff-list">
    {rows.map((row, index) => <div key={`${row.sign}:${index}`} className={`audit-diff-row ${row.sign}`}>
      <span className={`audit-sign ${row.sign}`}>{row.sign === 'add' ? '新增' : row.sign === 'del' ? '移除' : '保留'}</span>
      <span className="audit-name">{row.text}</span>
      <span className="audit-id">{row.detail}</span>
    </div>)}
  </div>
}

function RuleDiffField({ change }: { change: AuditChange }) {
  const lines = diffLines(change.before, change.after)
  return <pre className="audit-rule-diff">
    {lines.map((line, index) => <span key={index} className={line.type}>{`${line.type === 'add' ? '+' : line.type === 'del' ? '−' : ' '} ${line.text}`}</span>)}
  </pre>
}

function ChangeField({ field, change, item, live }: {
  field: string
  change: AuditChange
  item: AuditOperation
  live?: Record<string, string>
}) {
  return <section className="audit-field">
    <h4>{fieldLabel(field)} <code>{field}</code></h4>
    {(field === 'channel_target.suppliers' || field === 'channel_target.auth_ids')
      ? <ChannelListField change={change} item={item} live={live} />
      : field === 'notifications' ? <NotificationListField change={change} />
        : field.startsWith('rules.') ? <RuleDiffField change={change} />
          : <ScalarField field={field} change={change} />}
  </section>
}

function AuditRow({ item, live, expanded, onToggle }: {
  item: AuditOperation
  live?: Record<string, string>
  expanded: boolean
  onToggle: () => void
}) {
  const module = moduleOf(item)
  const meta = moduleMeta(module)
  const outcome = OUTCOME_META[item.outcome]
  const chips = summarizeChanges(item.changes)
  const duration = durationMs(item)
  return <div className={`audit-item ${expanded ? 'expanded' : ''}`.trim()}>
    <div className="audit-row" role="button" tabIndex={0} aria-expanded={expanded}
      data-operation-id={item.operation_id}
      aria-label={`${formatClock(item.started_at)} ${meta.label} ${actionLabel(item.action)} ${item.object_label ?? item.object_ref}`}
      onClick={onToggle} onKeyDown={event => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); onToggle() } }}>
      <span className="audit-time">{formatClock(item.started_at)}</span>
      <Tag size="small" color={meta.color} className="audit-module-tag">{meta.label}</Tag>
      <div className="audit-main">
        <div className="audit-title">
          <span className="audit-action">{actionLabel(item.action)}</span>
          <span className="audit-object">{item.object_label ?? item.object_ref}</span>
          {item.object_label && item.object_label !== item.object_ref && <span className="audit-ref">{item.object_ref}</span>}
        </div>
        {chips.length > 0 && <div className="audit-chips">
          {chips.map(chip => <span key={chip.key} className="audit-chip">
            {chip.parts.map((part, index) => <span key={index} className={part.tone ?? undefined}>{part.text}</span>)}
          </span>)}
        </div>}
        {item.outcome === 'failed' && item.error_code && <div className="audit-error">错误：{errorText(item.error_code)}</div>}
      </div>
      <div className="audit-right">
        <Tag size="small" type={outcome.tone === 'grey' ? 'ghost' : 'light'} color={outcome.tone}>{outcome.label}</Tag>
        <span className={`audit-chevron ${expanded ? 'open' : ''}`} aria-hidden>▾</span>
      </div>
    </div>
    {expanded && <div className="audit-detail">
      <div className="audit-meta">
        <span>操作 ID <code>{item.operation_id}</code></span>
        <span>开始 {formatTime(item.started_at)}{item.finished_at ? ` · 结束 ${formatTime(item.finished_at)}` : ''}</span>
        {duration != null && <span>耗时 {duration} ms</span>}
        <span>{item.changed == null ? '变更未确认' : item.changed ? '有变更' : '无变化（changed=false）'}</span>
        <span>来源 {item.actor}</span>
        {item.error_code && item.outcome !== 'failed' && <span>错误码 {item.error_code}</span>}
      </div>
      {Object.keys(item.changes).length === 0 && <p className="audit-nochange">无字段级变更记录</p>}
      {Object.entries(item.changes).map(([field, change]) =>
        <ChangeField key={field} field={field} change={change} item={item} live={live} />)}
    </div>}
  </div>
}

export default function AuditPanel() {
  const [date, setDate] = useState('')
  const [page, setPage] = useState(1)
  const [size, setSize] = useState(20)
  const [module, setModule] = useState('')
  const [refresh, setRefresh] = useState(0)
  const [data, setData] = useState<AuditPage>()
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [expandedId, setExpandedId] = useState<string>()
  const [live, setLive] = useState<Record<string, string>>()
  const request = useRef(0)
  // Keep the server-selected day for subsequent paging without a second initial request.
  const serverDate = useRef('')
  // 实时富化整个会话只尝试一次：失败静默降级，翻页/刷新不重复拉取。
  const liveTried = useRef(false)

  useEffect(() => {
    const version = ++request.current
    setLoading(true)
    setData(undefined)
    setError('')
    setExpandedId(undefined)
    api.getAudit(date || serverDate.current || undefined, page, size, module || undefined).then(result => {
      if (version !== request.current) return
      serverDate.current = result.date
      setData(result)
    }).catch((e: Error) => {
      if (version === request.current) setError(e.message)
    }).finally(() => {
      if (version === request.current) setLoading(false)
    })
    return () => { request.current++ }
  }, [date, page, size, module, refresh])

  // 事件 labels 快照缺失时的实时名称回退：渠道目录 + Keeper 名称，全部 allSettled 静默降级。
  useEffect(() => {
    if (!data?.items.length || liveTried.current) return
    const needsChannelNames = data.items.some(item =>
      item.changes && ('channel_target.suppliers' in item.changes || 'channel_target.auth_ids' in item.changes))
    if (!needsChannelNames) return
    liveTried.current = true
    Promise.allSettled([listCpaCredentials(), api.getKeeperAuthNames()]).then(([credentials, keeperNames]) => {
      const map: Record<string, string> = {}
      const keepers = keeperNames.status === 'fulfilled' && keeperNames.value.status === 'ready' ? keeperNames.value.items : []
      if (credentials.status === 'fulfilled') {
        for (const file of credentials.value) {
          const keeper = findKeeperAuthName(file, keepers)
          if (keeper) map[file.id] = keeper.display_name
          else if (file.provider_label && file.provider_label !== file.provider) map[file.id] = `${file.provider_label} · ${file.label}`
          if (file.provider_label) map[file.provider] ??= file.provider_label
        }
      }
      setLive(map)
    })
  }, [data])

  const counts = data?.module_counts
  const hasChannelChanges = !!data?.items.some(item =>
    item.changes && ('channel_target.suppliers' in item.changes || 'channel_target.auth_ids' in item.changes))

  return <Card title="操作审计" className="audit-panel">
    <div className="audit-toolbar">
      <label className="audit-date">日期
        <Input type="date" aria-label="审计日期" value={date || data?.date || serverDate.current}
          onChange={value => { setDate(value); setPage(1); if (!value) serverDate.current = '' }} />
      </label>
      <span className="audit-toolbar-note">北京时间（Asia/Shanghai）</span>
      <div className="audit-module-filter" role="group" aria-label="模块筛选">
        <button type="button" className={`audit-module-chip ${!module ? 'active' : ''}`}
          onClick={() => { setModule(''); setPage(1) }}>全部</button>
        {AUDIT_MODULES.map(key => {
          const meta = moduleMeta(key)
          const count = counts?.[key]
          return <button key={key} type="button" className={`audit-module-chip ${module === key ? 'active' : ''}`}
            onClick={() => { setModule(module === key ? '' : key); setPage(1) }}>
            {meta.label}{count != null && <span className="audit-module-count">{count}</span>}
          </button>
        })}
      </div>
      <Button onClick={() => setRefresh(value => value + 1)} loading={loading}>刷新</Button>
    </div>
    {error && <Banner type="danger" title="审计读取失败" description={error} />}
    {!!data?.warnings.length && <Banner type="warning" title="审计数据不完整"
      description={data.warnings.join('；')} />}
    {loading && <p role="status">正在读取审计记录…</p>}
    {!loading && data && <>
      {data.items.length > 0 ? <div className="audit-list">
        {data.items.map(item => <AuditRow key={item.operation_id} item={item} live={live}
          expanded={expandedId === item.operation_id}
          onToggle={() => setExpandedId(expandedId === item.operation_id ? undefined : item.operation_id)} />)}
      </div> : <p>{data.warnings.length ? '没有可展示的有效记录，请检查上述审计警告'
        : data.total ? '本页暂无操作记录' : module ? '该模块当天暂无操作记录' : '当天暂无操作记录'}</p>}
      {hasChannelChanges && !live && <p className="audit-enrich-note">正在补充渠道名称…（失败时显示原始 ID）</p>}
      <div className="audit-pagination">
        <span>共 {data.total} 项 · 第 {data.page} 页</span>
        <Select aria-label="每页条数" value={size} optionList={[20, 50, 100].map(value => ({ value, label: `${value} 条/页` }))}
          onChange={value => { setSize(Number(value)); setPage(1) }} />
        <Button disabled={page <= 1} onClick={() => setPage(value => value - 1)}>上一页</Button>
        <Button disabled={page * size >= data.total} onClick={() => setPage(value => value + 1)}>下一页</Button>
      </div>
    </>}
  </Card>
}
