import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import KeysPanel from './KeysPanel'
import { api, type KeyBinding, type KeeperAliasesResponse, type StateResponse } from '../api'
import { buildKeyOptions } from '../keyOptions'
import { createKeyOptions } from '../test/keyOptions'

vi.mock('../api', async (importOriginal) => {
 const actual = await importOriginal<typeof import('../api')>()
 return { ...actual, listCpaAuthFiles: vi.fn().mockResolvedValue([]), api: { ...actual.api, postKey: vi.fn() } }
})
const binding: KeyBinding = { key: 'sk-a', alias: '手填旧名', enabled: true, blocked: false, rules: { global: 'a=>b', claude: '', codex: '', openai: '' } }
const state: StateResponse = { version: 1, rules: { global: '', claude: '', codex: '', openai: '' }, key_bindings: [binding], persisted: true }
const ready = (alias = 'Keeper 最新'): KeeperAliasesResponse => ({ status: 'ready', items: [{ key: 'sk-a', alias }], fetched_at: '2026-09-05T00:00:00Z' })
const deferred = <T,>() => { let resolve!: (value: T) => void; const promise = new Promise<T>(r => { resolve = r }); return { promise, resolve } }
const refresh = vi.fn<() => Promise<KeeperAliasesResponse>>()
const source = () => createKeyOptions({ options: buildKeyOptions(['sk-a', 'sk-b'], [binding], []), keeper: ready('缓存旧名'), refreshAliases: refresh })
async function edit() { const user = userEvent.setup(); await user.click(screen.getByRole('button', { name: '编辑' })); return user }
function aliasInput() { return screen.getByPlaceholderText('别名（可选）') }
function saveButton() { return screen.getByRole('dialog', { name: /编辑绑定/ }).querySelector('.semi-modal-footer .semi-button-primary') as HTMLElement }
beforeEach(() => { refresh.mockResolvedValue(ready()); vi.mocked(api.postKey).mockResolvedValue(state) })
afterEach(() => { vi.clearAllMocks() })

describe('同步填入与保存', () => {
 it('主动覆盖旧别名；同步不保存，手改后保存最终值', async () => {
  render(<KeysPanel state={state} onSaved={vi.fn()} keyOptions={source()} />)
  const user = await edit()
  await user.click(screen.getByRole('button', { name: '从 Keeper 同步' }))
  await waitFor(() => expect(aliasInput()).toHaveValue('Keeper 最新'))
  expect(refresh).toHaveBeenCalledTimes(1)
  expect(api.postKey).not.toHaveBeenCalled()
  await user.clear(aliasInput()); await user.type(aliasInput(), '最终别名')
  await user.click(saveButton())
  await waitFor(() => expect(api.postKey).toHaveBeenCalledWith(expect.objectContaining({ key: 'sk-a', alias: '最终别名', rules: binding.rules })))
 })
 it('取消同步结果不保存，重新打开仍显示原别名', async () => {
  render(<KeysPanel state={state} onSaved={vi.fn()} keyOptions={source()} />)
  const user = await edit()
  await user.click(screen.getByRole('button', { name: '从 Keeper 同步' }))
  await waitFor(() => expect(aliasInput()).toHaveValue('Keeper 最新'))
  await user.click(within(screen.getByRole('dialog', { name: /编辑绑定/ })).getByRole('button', { name: 'cancel' }))
  await edit()
  expect(aliasInput()).toHaveValue('手填旧名')
  expect(api.postKey).not.toHaveBeenCalled()
 })
})

describe('同步失败及竞态', () => {
 for (const [name, response] of [
  ['空别名', ready(' ')], ['无对应 Key', { status: 'ready', items: [], fetched_at: '2026-09-05T00:00:00Z' }],
  ['认证失败', { status: 'unavailable', items: [], error_code: 'authentication_failed' }],
 ] as Array<[string, KeeperAliasesResponse]>) {
  it(`${name}时保留当前输入`, async () => {
   refresh.mockResolvedValue(response)
   render(<KeysPanel state={state} onSaved={vi.fn()} keyOptions={source()} />)
   const user = await edit(); await user.click(screen.getByRole('button', { name: '从 Keeper 同步' }))
   await waitFor(() => expect(refresh).toHaveBeenCalled())
   expect(aliasInput()).toHaveValue('手填旧名'); expect(api.postKey).not.toHaveBeenCalled()
  })
 }
 it('等待期间手改别名不会被迟到响应覆盖', async () => {
  const response = deferred<KeeperAliasesResponse>(); refresh.mockReturnValue(response.promise)
  render(<KeysPanel state={state} onSaved={vi.fn()} keyOptions={source()} />)
  const user = await edit(); await user.click(screen.getByRole('button', { name: '从 Keeper 同步' }))
  await user.clear(aliasInput()); await user.type(aliasInput(), '等待期间手改')
  await act(async () => response.resolve(ready()))
  expect(aliasInput()).toHaveValue('等待期间手改')
 })
 it('关闭重开相同 Key，旧响应不写入新弹窗', async () => {
  const response = deferred<KeeperAliasesResponse>(); refresh.mockReturnValue(response.promise)
  render(<KeysPanel state={state} onSaved={vi.fn()} keyOptions={source()} />)
  const user = await edit(); await user.click(screen.getByRole('button', { name: '从 Keeper 同步' }))
  await user.click(within(screen.getByRole('dialog', { name: /编辑绑定/ })).getByRole('button', { name: 'cancel' }))
  await edit(); await act(async () => response.resolve(ready()))
  expect(aliasInput()).toHaveValue('手填旧名')
 })
 it('同步结果保留等待期间修改的其它绑定字段', async () => {
  const response = deferred<KeeperAliasesResponse>(); refresh.mockReturnValue(response.promise)
  render(<KeysPanel state={state} onSaved={vi.fn()} keyOptions={source()} />)
  const user = await edit(); await user.click(screen.getByRole('button', { name: '从 Keeper 同步' }))
  await user.click(screen.getByRole('switch', { name: '编辑绑定：启用规则' }))
  await act(async () => response.resolve(ready()))
  expect(aliasInput()).toHaveValue('Keeper 最新')
  expect(screen.getByRole('switch', { name: '编辑绑定：启用规则' })).not.toBeChecked()
  await user.click(saveButton())
  await waitFor(() => expect(api.postKey).toHaveBeenCalledWith(expect.objectContaining({ alias: 'Keeper 最新', enabled: false })))
 })
 it('Key A→B→A 后丢弃旧响应', async () => {
  const response = deferred<KeeperAliasesResponse>(); refresh.mockReturnValue(response.promise)
  render(<KeysPanel state={state} onSaved={vi.fn()} keyOptions={source()} />)
  const user = await edit(); await user.click(screen.getByRole('button', { name: '从 Keeper 同步' }))
  for (const label of ['sk-b', '手填旧名 · sk-a']) {
   await user.click(within(screen.getByRole('dialog', { name: /编辑绑定/ })).getByRole('combobox', { name: 'API Key' }))
   const search = within(screen.getByRole('dialog', { name: /编辑绑定/ })).getByRole('combobox', { name: 'API Key' }).querySelector('input') as HTMLInputElement
   await user.clear(search)
   await user.type(search, label === 'sk-b' ? 'sk-b' : 'sk-a')
   await user.click(await screen.findByRole('option', { name: new RegExp(label) }))
   await waitFor(() => expect(within(screen.getByRole('dialog', { name: /编辑绑定/ })).getByRole('combobox', { name: 'API Key' })).toHaveTextContent(label))
  }
  await act(async () => response.resolve(ready()))
  expect(aliasInput()).toHaveValue('手填旧名')
 })
 it('未配置 Keeper 时禁用同步', async () => {
  render(<KeysPanel state={state} onSaved={vi.fn()} keyOptions={createKeyOptions()} />)
  await edit()
  expect(screen.getByRole('button', { name: '从 Keeper 同步' })).toBeDisabled()
 })
})
