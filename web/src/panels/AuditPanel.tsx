import { useEffect, useRef, useState } from 'react'
import { Banner, Button, Card, Input, Modal, Select, Table, Tag } from '@douyinfe/semi-ui'
import { api, type AuditOperation, type AuditPage } from '../api'
import './AuditPanel.css'

const outcomes = { running: '进行中', succeeded: '成功', failed: '失败', unknown: '结果未确认' }
const actions: Record<string, string> = { create: '新增', update: '更新', delete: '删除', sync: '同步' }
const formatValue = (value: unknown) => typeof value === 'string' ? value : JSON.stringify(value, null, 2) ?? '未记录'
const formatTime = (value: string) => new Date(value).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false })

export default function AuditPanel() {
  const [date, setDate] = useState('')
  const [page, setPage] = useState(1)
  const [size, setSize] = useState(20)
  const [refresh, setRefresh] = useState(0)
  const [data, setData] = useState<AuditPage>()
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [detail, setDetail] = useState<AuditOperation>()
  const request = useRef(0)
  // Keep the server-selected day for subsequent paging without a second initial request.
  const serverDate = useRef('')
  useEffect(() => {
    const version = ++request.current
    setLoading(true)
    setData(undefined)
    setError('')
    setDetail(undefined)
    api.getAudit(date || serverDate.current || undefined, page, size).then(result => {
      if (version !== request.current) return
      serverDate.current = result.date
      setData(result)
    }).catch((e: Error) => {
      if (version === request.current) setError(e.message)
    }).finally(() => {
      if (version === request.current) setLoading(false)
    })
    return () => { request.current++ }
  }, [date, page, size, refresh])

  return <Card title="操作审计" className="audit-panel">
    <div className="audit-toolbar">
      <label className="audit-date">日期
        <Input type="date" aria-label="审计日期" value={date || data?.date || serverDate.current}
          onChange={value => { setDate(value); setPage(1); if (!value) serverDate.current = '' }} />
      </label>
      <span>北京时间（Asia/Shanghai）</span>
      <Button onClick={() => setRefresh(value => value + 1)} loading={loading}>刷新</Button>
    </div>
    {error && <Banner type="danger" title="审计读取失败" description={error} />}
    {!!data?.warnings.length && <Banner type="warning" title="审计数据不完整"
      description={data.warnings.join('；')} />}
    {loading && <p role="status">正在读取审计记录…</p>}
    {!loading && data && <>
      {data.items.length > 0 ? <div className="audit-table-scroll">
        <Table rowKey="operation_id" dataSource={data.items} pagination={false} columns={[
          { title: '时间（北京）', dataIndex: 'started_at', render: (value: string) => formatTime(value), width: 180 },
          { title: '动作', dataIndex: 'action', render: (value: string) => actions[value] ?? value, width: 80 },
          { title: '对象', dataIndex: 'object_ref', render: (value: string, item: AuditOperation) => <span>{item.object_type} · {value}</span> },
          { title: '结果', dataIndex: 'outcome', render: (value: AuditOperation['outcome']) => <Tag
            color={value === 'succeeded' ? 'green' : value === 'failed' ? 'red' : 'grey'}>{outcomes[value]}</Tag>, width: 120 },
          { title: '详情', dataIndex: 'operation_id', width: 100,
            render: (_: string, item: AuditOperation) => <Button onClick={() => setDetail(item)}>查看详情</Button> },
        ]} />
      </div> : <p>{data.warnings.length ? '没有可展示的有效记录，请检查上述审计警告' : data.total ? '本页暂无操作记录' : '当天暂无操作记录'}</p>}
      <div className="audit-pagination">
        <span>共 {data.total} 项 · 第 {data.page} 页</span>
        <Select aria-label="每页条数" value={size} optionList={[20, 50, 100].map(value => ({ value, label: `${value} 条/页` }))}
          onChange={value => { setSize(Number(value)); setPage(1) }} />
        <Button disabled={page <= 1} onClick={() => setPage(value => value - 1)}>上一页</Button>
        <Button disabled={page * size >= data.total} onClick={() => setPage(value => value + 1)}>下一页</Button>
      </div>
    </>}
    <Modal title="操作详情" visible={!!detail} onCancel={() => setDetail(undefined)} footer={
      <Button onClick={() => setDetail(undefined)}>关闭</Button>} width={720} style={{ maxWidth: 'calc(100vw - 32px)' }}>
      {detail && <div className="audit-detail">
        <p>操作 ID：{detail.operation_id}</p>
        <p>开始：{formatTime(detail.started_at)}{detail.finished_at ? ` · 结束：${formatTime(detail.finished_at)}` : ''}</p>
        <p>{detail.changed === null ? '变更未确认' : detail.changed ? '有变更' : '无变化（changed=false）'}</p>
        {detail.error_code && <p>错误：{detail.error_code}</p>}
        {Object.entries(detail.changes).map(([field, change]) => <section key={field}>
          <h4>{field}</h4><div className="audit-change">
            <div><strong>变更前</strong><pre>{formatValue(change.before)}</pre></div>
            <div><strong>变更后</strong><pre>{formatValue(change.after)}</pre></div>
          </div>
        </section>)}
      </div>}
    </Modal>
  </Card>
}
