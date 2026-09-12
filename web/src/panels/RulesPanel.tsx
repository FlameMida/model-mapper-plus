import { useEffect, useState } from 'react'
import { Button, Card, Toast } from '@douyinfe/semi-ui'
import { api, RuleSet, StateResponse, type ManagementAPIError } from '../api'
import RuleSetEditor from '../components/RuleSetEditor'

interface Props {
  state: StateResponse
  onSaved: (s: StateResponse) => void
}

export default function RulesPanel({ state, onSaved }: Props) {
  const [rules, setRules] = useState<RuleSet>(state.rules)
  const [saving, setSaving] = useState(false)

  // Keep local draft in sync when parent reloads state (refresh / save elsewhere).
  useEffect(() => {
    setRules(state.rules)
  }, [state.rules])

  const save = () => {
    setSaving(true)
    api.putRules(rules)
      .then((s) => {
        onSaved(s)
        Toast.success('规则已保存')
        if (s.audit?.recorded === false) Toast.warning(`审计结果未写入（${s.audit.operation_id}）`)
      })
      .catch((e: ManagementAPIError) => {
        Toast.error(e.message)
        if (e.audit?.recorded === false) Toast.warning(`审计结果未写入（${e.audit.operation_id}）`)
      })
      .finally(() => setSaving(false))
  }

  return (
    <Card title="映射规则（按端点分段，有序执行）" style={{ margin: 16 }}
      headerExtraContent={<Button theme="solid" loading={saving} onClick={save}>保存</Button>}>
      <RuleSetEditor value={rules} onChange={setRules} />
      {state.state_file && (
        <div style={{ marginTop: 8, fontSize: 12, color: 'var(--semi-color-text-2)' }}>
          state_file：{state.state_file}{state.persisted ? '（已接管）' : '（首次保存后写入）'}
        </div>
      )}
    </Card>
  )
}
