import { useId, useState, type CSSProperties } from 'react'
import { Button, Select, Typography } from '@douyinfe/semi-ui'
import { maskKey } from '../keyOptions'
import { keeperStatusText, type KeyOptionsState } from '../useKeyOptions'

interface Props {
  source: KeyOptionsState
  value: string
  onChange: (key: string) => void
  allowCreate?: boolean
  showClear?: boolean
  placeholder?: string
  style?: CSSProperties
}

export default function ApiKeySelect({ source, value, onChange, allowCreate, showClear, placeholder = '选择或输入 API key', style }: Props) {
  const labelId = useId()
  const [search, setSearch] = useState('')
  const matches = (input: string, option: { searchText?: unknown; value?: unknown }) => String(option.searchText ?? option.value ?? '').toLocaleLowerCase().includes(input.trim().toLocaleLowerCase())
  // Semi's native allowCreate reuses stale options after asynchronous updates.
  // Use its regular controlled list and append an explicit manual Key choice for a new full value.
  let options = source.options
  if (allowCreate && value && !options.some(option => option.value === value)) {
    options = [...options, { value, label: maskKey(value), searchText: value.toLocaleLowerCase() }]
  }
  const manualKey = search.trim()
  if (allowCreate && manualKey && !options.some(option => option.value === manualKey)) {
    options = [...options, { value: manualKey, label: `使用手动 Key：${manualKey}`, searchText: manualKey.toLocaleLowerCase() }]
  }
  return (
    <div style={{ minWidth: 0, ...style }}>
      <span id={labelId} style={{ position: 'absolute', width: 1, height: 1, overflow: 'hidden', clipPath: 'inset(50%)' }}>API Key</span>
      <Select
        aria-labelledby={labelId}
        style={{ width: '100%' }}
        dropdownClassName="api-key-options"
        dropdownStyle={{ maxWidth: 'min(810px, calc(100vw - 32px))' }}
        loading={source.keysLoading}
        showClear={showClear}
        placeholder={placeholder}
        value={value || undefined}
        onChange={next => onChange(typeof next === 'string' ? next : '')}
        filter={matches}
        onSearch={setSearch}
        onDropdownVisibleChange={visible => { if (!visible) setSearch('') }}
        // Filter the controlled list too: Semi resets its internal filter when
        // asynchronous options change, which can otherwise focus an unrelated Key.
        optionList={search ? options.filter(option => matches(search, option)) : options}
      />
      <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', marginTop: 4 }}>
        <Typography.Text size="small" type={source.keeper?.status === 'unavailable' ? 'warning' : 'tertiary'}>
          {source.keeperLoading ? 'Keeper 别名加载中' : keeperStatusText(source.keeper)}
        </Typography.Text>
        {source.keeper?.status !== 'disabled' && (
          <Button size="small" theme="borderless" loading={source.keeperLoading} onClick={() => { void source.refreshAliases() }}>刷新别名</Button>
        )}
      </div>
      {source.keysError && <Typography.Text size="small" type="danger">{source.keysError}</Typography.Text>}
    </div>
  )
}
