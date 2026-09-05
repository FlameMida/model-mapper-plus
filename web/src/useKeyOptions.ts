import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, listCpaApiKeys, type KeyBinding, type KeeperAliasesResponse } from './api'
import { buildKeyOptions, type KeyOption } from './keyOptions'

export interface KeyOptionsState {
  options: KeyOption[]
  keysLoading: boolean
  keysError: string
  keeper: KeeperAliasesResponse | null
  keeperLoading: boolean
  refreshAliases: () => Promise<KeeperAliasesResponse>
}
const unavailable = (): KeeperAliasesResponse => ({ status: 'unavailable', items: [], error_code: 'connection_failed' })

// App owns this hook so both panels see the same successful refresh. Generation
// checks isolate logout/relogin and a manual refresh from earlier pending reads.
export function useKeyOptions(bindings: KeyBinding[], enabled: boolean, page = ''): KeyOptionsState {
  const [keys, setKeys] = useState<string[]>([])
  const [keysLoading, setKeysLoading] = useState(false)
  const [keysError, setKeysError] = useState('')
  const [keeper, setKeeper] = useState<KeeperAliasesResponse | null>(null)
  const [keeperLoading, setKeeperLoading] = useState(false)
  const generation = useRef(0)
  const aliasRequest = useRef(0)
  const keyRequest = useRef(0)

  const loadAliases = useCallback(async (force: boolean, session: number) => {
    const request = ++aliasRequest.current
    setKeeperLoading(true)
    let result: KeeperAliasesResponse
    try { result = await (force ? api.refreshKeeperAliases() : api.getKeeperAliases()) }
    catch { result = unavailable() }
    if (generation.current === session && aliasRequest.current === request) {
      setKeeper(result)
      setKeeperLoading(false)
    }
    return result
  }, [])

  useEffect(() => {
    const session = ++generation.current
    setKeys([]); setKeysError(''); setKeeper(null)
    setKeysLoading(enabled); setKeeperLoading(enabled)
    if (enabled) {
      void loadAliases(false, session)
    }
    return () => { generation.current++ }
  }, [enabled, loadAliases])

  useEffect(() => {
    if (!enabled) return
    const session = generation.current
    const request = ++keyRequest.current
    const current = () => generation.current === session && keyRequest.current === request
    setKeysLoading(true); setKeysError('')
    listCpaApiKeys().then(value => {
      if (current()) setKeys(value)
    }).catch((error: Error) => {
      if (current()) setKeysError(error.message)
    }).finally(() => {
      if (current()) setKeysLoading(false)
    })
    return () => { keyRequest.current++ }
  }, [enabled, page])

  const refreshAliases = useCallback(() => enabled
    ? loadAliases(true, generation.current)
    : Promise.resolve(unavailable()), [enabled, loadAliases])
  const options = useMemo(() => buildKeyOptions(keys, bindings, keeper?.status === 'ready' ? keeper.items : []), [keys, bindings, keeper])
  return { options, keysLoading, keysError, keeper, keeperLoading, refreshAliases }
}

export function keeperStatusText(result: KeeperAliasesResponse | null): string {
  if (!result) return 'Keeper 别名加载中'
  if (result.status === 'disabled') return 'Keeper 未配置'
  if (result.status === 'ready') return 'Keeper 别名已加载'
  const messages = {
    configuration_error: 'Keeper 地址或密码环境变量未正确配置',
    authentication_failed: 'Keeper 认证失败，请检查登录密码',
    rate_limited: 'Keeper 请求受限',
    timeout: 'Keeper 读取超时',
    invalid_response: 'Keeper 返回的数据格式不正确',
    connection_failed: '无法连接 Keeper',
  }
  const retry = result.retry_after_seconds ? `，${result.retry_after_seconds} 秒后重试` : ''
  return messages[result.error_code] + retry
}
