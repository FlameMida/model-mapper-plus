import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, listCpaApiKeys, type KeeperAliasesResponse } from './api'
import { useKeyOptions } from './useKeyOptions'

vi.mock('./api', async (importOriginal) => {
 const actual = await importOriginal<typeof import('./api')>()
 return { ...actual, listCpaApiKeys: vi.fn(), api: { ...actual.api, getKeeperAliases: vi.fn(), refreshKeeperAliases: vi.fn() } }
})
const ready = (alias: string): KeeperAliasesResponse => ({ status: 'ready', items: [{ key: 'sk-a', alias }], fetched_at: '2026-09-05T00:00:00Z' })
const deferred = <T,>() => { let resolve!: (value: T) => void; const promise = new Promise<T>(r => { resolve = r }); return { promise, resolve } }
beforeEach(() => { vi.mocked(listCpaApiKeys).mockResolvedValue(['sk-a']); vi.mocked(api.getKeeperAliases).mockResolvedValue(ready('Keeper')) })
afterEach(() => vi.clearAllMocks())

describe('Key 数据独立加载与失败隔离', () => {
 it('Keeper 尚未返回或失败时仍可使用 CPA Key', async () => {
  const response = deferred<KeeperAliasesResponse>(); vi.mocked(api.getKeeperAliases).mockReturnValue(response.promise)
  const { result } = renderHook(() => useKeyOptions([], true))
  await waitFor(() => expect(result.current.options[0]?.value).toBe('sk-a'))
  expect(result.current.keeperLoading).toBe(true)
  await act(async () => response.resolve({ status: 'unavailable', items: [], error_code: 'authentication_failed' }))
  expect(result.current.options[0].value).toBe('sk-a')
  expect(result.current.keeper?.status).toBe('unavailable')
 })
 it('手动刷新覆盖旧 GET，旧回包不能覆盖刷新结果', async () => {
  const response = deferred<KeeperAliasesResponse>(); vi.mocked(api.getKeeperAliases).mockReturnValue(response.promise)
  vi.mocked(api.refreshKeeperAliases).mockResolvedValue(ready('最新'))
  const { result } = renderHook(() => useKeyOptions([], true))
  await waitFor(() => expect(result.current.options).toHaveLength(1))
  await act(async () => { await result.current.refreshAliases() })
  expect(result.current.options[0].label).toContain('最新')
  await act(async () => response.resolve(ready('旧数据')))
  expect(result.current.options[0].label).toContain('最新')
 })
 it('退出后清理，旧请求不能写入重新登录的状态', async () => {
  const response = deferred<KeeperAliasesResponse>(); vi.mocked(api.getKeeperAliases).mockReturnValueOnce(response.promise)
  const { result, rerender } = renderHook(({ enabled }) => useKeyOptions([], enabled), { initialProps: { enabled: true } })
  await waitFor(() => expect(result.current.options).toHaveLength(1))
  rerender({ enabled: false })
  expect(result.current.options).toEqual([])
  expect(result.current.keeper).toBeNull()
  vi.mocked(api.getKeeperAliases).mockResolvedValue(ready('新会话'))
  rerender({ enabled: true })
  await waitFor(() => expect(result.current.options[0]?.label).toContain('新会话'))
  await act(async () => response.resolve(ready('旧会话')))
  expect(result.current.options[0].label).toContain('新会话')
 })
 it('刷新网络错误回退展示，保留 CPA Keys', async () => {
  vi.mocked(api.refreshKeeperAliases).mockRejectedValue(new Error('failed'))
  const { result } = renderHook(() => useKeyOptions([], true))
  await waitFor(() => expect(result.current.options[0]?.label).toContain('Keeper'))
  await act(async () => { await result.current.refreshAliases() })
  expect(result.current.options[0].label).toBe('sk-a')
  expect(result.current.keeper?.status).toBe('unavailable')
 })
})

it('重新进入页面时重试 CPA 列表并同步增删', async () => {
 vi.mocked(listCpaApiKeys).mockRejectedValueOnce(new Error('暂时失败'))
 const { result, rerender } = renderHook(({ page }) => useKeyOptions([], true, page), { initialProps: { page: 'keys' } })
 await waitFor(() => expect(result.current.keysError).toBe('暂时失败'))
 rerender({ page: 'preview' })
 await waitFor(() => expect(result.current.options[0]?.value).toBe('sk-a'))
 expect(result.current.keysError).toBe('')
 vi.mocked(listCpaApiKeys).mockResolvedValue(['sk-b'])
 rerender({ page: 'keys' })
 await waitFor(() => expect(result.current.options.map(o => o.value)).toEqual(['sk-b']))
})
