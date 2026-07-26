import { useEffect, useState } from 'react'
import { Button, Card, Input, Select, Tag, Toast, Typography, Descriptions } from '@douyinfe/semi-ui'
import { api, PreviewResponse, listCpaApiKeys } from '../api'

const FORMATS = [
  { value: 'claude', label: 'claude（/v1/messages）' },
  { value: 'openai', label: 'openai（chat completions）' },
  { value: 'openai-response', label: 'openai-response（responses/codex）' },
]

export default function PreviewPanel() {
  const [cpaKeys, setCpaKeys] = useState<string[]>([])
  const [key, setKey] = useState('')
  const [format, setFormat] = useState('claude')
  const [model, setModel] = useState('')
  const [result, setResult] = useState<PreviewResponse | null>(null)
  const [running, setRunning] = useState(false)

  useEffect(() => {
    listCpaApiKeys().then(setCpaKeys).catch(() => setCpaKeys([]))
  }, [])

  const run = () => {
    setRunning(true)
    api.preview({ key: key || undefined, format, model })
      .then(setResult)
      .catch((e: Error) => { setResult(null); Toast.error(e.message) })
      .finally(() => setRunning(false))
  }

  return (
    <Card title="规则试跑（不落盘、不发上游；引擎与正式路由相同，含 enabled 开关）" style={{ margin: 16 }}>
      <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', alignItems: 'center' }}>
        <Select style={{ width: 260 }} filter placeholder="Key（可选，模拟 key 维度）"
          value={key || undefined} onChange={(v) => setKey(String(v))} showClear
          optionList={cpaKeys.map((k) => ({ value: k, label: `${k.slice(0, 6)}…${k.slice(-4)}` }))}
        />
        <Select style={{ width: 280 }} value={format} onChange={(v) => setFormat(String(v))}>
          {FORMATS.map((f) => <Select.Option key={f.value} value={f.value}>{f.label}</Select.Option>)}
        </Select>
        <Input style={{ width: 320 }} placeholder="模型，如 claude-opus-4-5(max)"
          value={model} onChange={setModel} onEnterPress={run} />
        <Button theme="solid" loading={running} onClick={run} disabled={!model}>试跑</Button>
      </div>

      {result && (
        <Descriptions style={{ marginTop: 16 }} align="left"
          data={[
            { key: '顶层规则输出 M₁', value: result.m1 },
            { key: 'key 层输出 M₂', value: result.m2 },
            { key: '是否路由', value: result.routed ? <Tag color="green">路由回插件执行</Tag> : <Tag>不接管，走默认路径</Tag> },
            { key: '最终出站模型', value: <Typography.Text strong>{result.final}</Typography.Text> },
            { key: 'CPA 处理', value: '剥后缀选模型并应用强度；响应模型字段恢复为客户端请求模型' },
          ]}
        />
      )}
    </Card>
  )
}
