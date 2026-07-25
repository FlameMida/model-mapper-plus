import { useState } from 'react'
import { Button, Card, Toast } from '@douyinfe/semi-ui'
import { api, RuleSet, StateResponse } from '../api'
import RuleSetEditor from '../components/RuleSetEditor'

interface Props {
  state: StateResponse
  onSaved: (s: StateResponse) => void
}

export default function RulesPanel({ state, onSaved }: Props) {
  const [rules, setRules] = useState<RuleSet>(state.rules)
  const [saving, setSaving] = useState(false)

  const save = () => {
    setSaving(true)
    api.putRules(rules)
      .then((s) => { onSaved(s); Toast.success('规则已保存') })
      .catch((e: Error) => Toast.error(e.message))
      .finally(() => setSaving(false))
  }

  return (
    <Card title="映射规则（按端点分段，有序执行）" style={{ margin: 16 }}
      headerExtraContent={<Button theme="solid" loading={saving} onClick={save}>保存</Button>}>
      <RuleSetEditor value={rules} onChange={setRules} />
    </Card>
  )
}
