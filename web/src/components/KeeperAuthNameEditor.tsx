import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { Button, Input } from '@douyinfe/semi-ui'
import { api, type KeeperAuthName, type ManagementAPIError } from '../api'

interface Props {
  authIndex: string
  name: KeeperAuthName
  viewRevision?: number
  onSaved: (name: KeeperAuthName) => void
}

export default function KeeperAuthNameEditor(props: Props) {
  return <NameEditor key={`${props.authIndex}:${props.name.identity_id}`} {...props} />
}

function NameEditor({ authIndex, name, viewRevision = 0, onSaved }: Props) {
  const [alias, setAlias] = useState(name.alias)
  const [saving, setSaving] = useState(false)
  const [notice, setNotice] = useState('')
  const [auditWarning, setAuditWarning] = useState(false)
  const version = useRef(0)
  const alive = useRef(true)
  const requestSequence = useRef(0)
  const pending = useRef<number | null>(null)
  const dirty = useRef(false)
  useEffect(() => { alive.current = true; return () => { alive.current = false; version.current++ } }, [])
  useLayoutEffect(() => {
    // A newer read invalidates responses, but keeps this editor and its manual draft.
    version.current++
    pending.current = null
    setSaving(false)
    setNotice('')
    setAuditWarning(false)
  }, [viewRevision])
  useEffect(() => {
    if (!dirty.current && pending.current === null) setAlias(name.alias)
  }, [name.alias, viewRevision])
  const count = Array.from(alias).length
  const invalid = count > 50 || /[\u0000-\u001f\u007f-\u009f]/u.test(alias)
  const submit = async () => {
    if (pending.current !== null || invalid) return
    const request = ++requestSequence.current
    pending.current = request
    const submittedVersion = version.current
    const isCurrent = () => alive.current && submittedVersion === version.current
    setSaving(true)
    setNotice('')
    setAuditWarning(false)
    try {
      const response = await api.patchKeeperAuthName(authIndex, alias)
      if (!isCurrent()) return
      setAuditWarning(response.audit?.recorded === false)
      if (response.status === 'ready' && response.item?.auth_index === authIndex) {
        dirty.current = false
        setAlias(response.item.alias)
        onSaved(response.item)
        setNotice('已保存到 Keeper')
      } else if (response.status === 'unknown' || response.status === 'ready') {
        setNotice('同步结果未确认，请刷新名称核对')
      } else {
        setNotice(response.status === 'invalid' ? '同步失败：名称不合法，已保留输入' :
          response.status === 'not_found' ? '同步失败：未找到唯一匹配的 Keeper 身份，已保留输入' :
            '同步失败：Keeper 暂不可用，已保留输入')
      }
    } catch (error) {
      if (isCurrent()) {
        const failure = error as ManagementAPIError
        setAuditWarning(failure.audit?.recorded === false)
        setNotice(failure.name === 'ManagementAPIError' ? `同步失败：${failure.message}` : '同步结果未确认，请刷新名称核对')
      }
    } finally {
      if (pending.current === request) {
        pending.current = null
        if (alive.current) setSaving(false)
      }
    }
  }
  return <div className="keeper-auth-name-editor">
    <div className="keeper-auth-name-controls">
      <Input aria-label="认证自定义名称" value={alias} placeholder="留空恢复默认名称"
        onChange={value => { version.current++; dirty.current = true; setAlias(value); setNotice('') }} />
      <Button disabled={saving || invalid} loading={saving} onClick={() => { void submit() }}>同步到 Keeper</Button>
    </div>
    <div className="keeper-auth-name-help"><span>立即保存到 Keeper，取消绑定不会撤销</span><span>{count} / 50</span></div>
    {invalid && <p role="alert">名称最多 50 个 Unicode 字符，且不能包含控制字符</p>}
    {notice && <p role="status">{notice}</p>}
    {auditWarning && <p role="alert" className="keeper-auth-name-warning">审计结果未写入</p>}
  </div>
}
