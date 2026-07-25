import { useState } from 'react'
import { Tabs, TabPane, Input, Button, Select, Typography } from '@douyinfe/semi-ui'
import { IconArrowUp, IconArrowDown, IconDelete, IconPlus } from '@douyinfe/semi-icons'
import { RuleSet } from '../api'
import { Entry, splitEntries, joinEntries } from '../dsl'

const SEGMENTS = [
  { key: 'global', label: '全局' },
  { key: 'claude', label: 'Claude Messages' },
  { key: 'codex', label: 'Codex Responses' },
  { key: 'openai', label: 'OpenAI Completions' },
] as const

type SegmentKey = (typeof SEGMENTS)[number]['key']

interface Props {
  value: RuleSet
  onChange: (next: RuleSet) => void
}

function EntryRow({ entry, onUpdate, onMove, onDelete }: {
  entry: Entry
  onUpdate: (e: Entry) => void
  onMove: (delta: -1 | 1) => void
  onDelete: () => void
}) {
  if (entry.kind === 'case') {
    return (
      <div style={{ display: 'flex', gap: 8, alignItems: 'center', padding: '6px 0' }}>
        <Select value={entry.op} style={{ width: 200 }}
          onChange={(v) => onUpdate({ kind: 'case', op: v as 'lower' | 'upper' })}>
          <Select.Option value="lower">\a（转小写）</Select.Option>
          <Select.Option value="upper">\A（转大写）</Select.Option>
        </Select>
        <Button icon={<IconArrowUp />} size="small" onClick={() => onMove(-1)} />
        <Button icon={<IconArrowDown />} size="small" onClick={() => onMove(1)} />
        <Button icon={<IconDelete />} size="small" type="danger" onClick={onDelete} />
      </div>
    )
  }
  return (
    <div style={{ display: 'flex', gap: 8, alignItems: 'center', padding: '6px 0' }}>
      <Input value={entry.find} placeholder="find（* 捕获，(max) 等后缀可参与）" style={{ flex: 2 }}
        onChange={(v) => onUpdate({ ...entry, find: v })} />
      <span>⇒</span>
      <Input value={entry.replace} placeholder="replace（$1 引用捕获）" style={{ flex: 2 }}
        onChange={(v) => onUpdate({ ...entry, replace: v })} />
      <Button icon={<IconArrowUp />} size="small" onClick={() => onMove(-1)} />
      <Button icon={<IconArrowDown />} size="small" onClick={() => onMove(1)} />
      <Button icon={<IconDelete />} size="small" type="danger" onClick={onDelete} />
    </div>
  )
}

export default function RuleSetEditor({ value, onChange }: Props) {
  const [active, setActive] = useState<SegmentKey>('global')
  const entries = splitEntries(value[active])

  const update = (next: Entry[]) => onChange({ ...value, [active]: joinEntries(next) })

  return (
    <div>
      <Tabs activeKey={active} onChange={(k) => setActive(k as SegmentKey)}>
        {SEGMENTS.map((s) => <TabPane tab={s.label} itemKey={s.key} key={s.key} />)}
      </Tabs>
      {entries.length === 0 && (
        <Typography.Text type="tertiary">
          本段为空{active !== 'global' ? '，请求将回退「全局」段' : ''}
        </Typography.Text>
      )}
      {entries.map((e, i) => (
        <EntryRow key={i} entry={e}
          onUpdate={(ne) => update(entries.map((x, j) => (j === i ? ne : x)))}
          onMove={(d) => {
            const j = i + d
            if (j < 0 || j >= entries.length) return
            const next = [...entries]
            ;[next[i], next[j]] = [next[j], next[i]]
            update(next)
          }}
          onDelete={() => update(entries.filter((_, j) => j !== i))} />
      ))}
      <div style={{ display: 'flex', gap: 8, marginTop: 8 }}>
        <Button icon={<IconPlus />} onClick={() => update([...entries, { kind: 'map', find: '', replace: '' }])}>
          添加映射
        </Button>
        <Button icon={<IconPlus />} onClick={() => update([...entries, { kind: 'case', op: 'lower' }])}>
          添加大小写操作
        </Button>
      </div>
      <Typography.Paragraph size="small" type="tertiary" style={{ marginTop: 8 }}>
        DSL：{value[active] || '（空）'} · 后缀 (none/auto/minimal/low/medium/high/xhigh/max) 或 (8192) 预算值可直接用于规则，CPA 执行时后缀覆盖请求体强度字段
      </Typography.Paragraph>
    </div>
  )
}
