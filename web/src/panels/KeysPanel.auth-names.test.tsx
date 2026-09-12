import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import KeysPanel from './KeysPanel'
import { api, listCpaCredentials, type StateResponse } from '../api'
import { createKeyOptions } from '../test/keyOptions'
import { Toast } from '@douyinfe/semi-ui'

vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>()
  return { ...actual, listCpaCredentials: vi.fn(), api: { ...actual.api,
    getKeeperAuthNames: vi.fn(), refreshKeeperAuthNames: vi.fn(), patchKeeperAuthName: vi.fn(), postKey: vi.fn(),
  } }
})

const NAME = { identity_id: '17', auth_index: 'idx-a', alias: '生产订阅', display_name: '生产订阅' }
const READY = { status: 'ready' as const, items: [NAME], fetched_at: '2026-09-12T00:00:00Z' }
const FILE = { id: 'auth-a', auth_index: 'idx-a', provider: 'codex', type: 'codex', source: 'auth-file' as const,
  label: '原名称', name: 'original.json', status: 'active', disabled: false }
const STATE: StateResponse = { version: 1, persisted: true,
  rules: { global: '', claude: '', codex: '', openai: '' },
  key_bindings: [{ key: 'fake-client-key', alias: 'K', enabled: true, blocked: false,
    rules: { global: '', claude: '', codex: '', openai: '' },
    channel_target: { enabled: true, suppliers: [], auth_ids: ['auth-a'] } }],
}
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(done => { resolve = done })
  return { promise, resolve }
}
async function open() {
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: '编辑' }))
  await user.click(screen.getByRole('tab', { name: '渠道定向' }))
  await screen.findByLabelText('凭据 auth-a')
  return user
}
const input = () => screen.getByRole('textbox', { name: '认证自定义名称' })
const sync = () => screen.getByRole('button', { name: '同步到 Keeper' })
const cancel = () => within(screen.getByRole('dialog')).getByRole('button', { name: 'cancel' })
function mount() { return render(<KeysPanel state={STATE} keyOptions={createKeyOptions()} onSaved={vi.fn()} />) }
beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(listCpaCredentials).mockResolvedValue([FILE])
  vi.mocked(api.getKeeperAuthNames).mockResolvedValue(READY)
  vi.mocked(api.refreshKeeperAuthNames).mockResolvedValue(READY)
})
afterEach(() => { Toast.destroyAll(); vi.unstubAllGlobals() })

it('S9 Key 重命名逐项保留 POST 与 DELETE 的审计警告并完成保存', async () => {
  // 真实写入 API 仅替换 fetch，名称/凭据读取沿本文件已有边界。
  const actual = await vi.importActual<typeof import('../api')>('../api')
  vi.mocked(api.postKey).mockImplementation(actual.api.postKey)
  vi.stubGlobal('fetch', vi.fn(async (_url: string, init: RequestInit) => ({ ok: true, status: 200,
    text: async () => JSON.stringify({ ...STATE, audit: { operation_id: init.method === 'POST' ? 'op-create' : 'op-delete', recorded: false } }),
  })))
  const onSaved = vi.fn()
  render(<KeysPanel state={STATE} keyOptions={createKeyOptions()} onSaved={onSaved} />)
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: '编辑' }))
  const select = screen.getByRole('combobox')
  await user.click(select)
  await user.type(select.querySelector('input')!, 'new-key')
  await user.click(await screen.findByRole('option', { name: /使用手动 Key：new-key/ }))
  await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'confirm' }))
  expect(await screen.findByText(/审计结果未写入.*op-create/)).toBeInTheDocument()
  expect(await screen.findByText(/审计结果未写入.*op-delete/)).toBeInTheDocument()
  expect(onSaved).toHaveBeenCalledOnce()
})

it.each([200, 400])('S9 Key 开关 HTTP%s 保留业务结果和独立审计警告', async status => {
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok: status === 200, status,
    text: async () => JSON.stringify({ ...STATE, error: 'Key 不存在', audit: { operation_id: `op-toggle-${status}`, recorded: false } }),
  })))
  const onSaved = vi.fn()
  render(<KeysPanel state={STATE} keyOptions={createKeyOptions()} onSaved={onSaved} />)
  await userEvent.setup().click(screen.getByRole('switch', { name: '启用规则：K' }))
  expect(await screen.findByText(new RegExp(`审计结果未写入.*op-toggle-${status}`))).toBeInTheDocument()
  if (status === 200) expect(onSaved).toHaveBeenCalledOnce()
  else {
    expect(onSaved).not.toHaveBeenCalled()
    expect(await screen.findByText('Key 不存在')).toBeInTheDocument()
  }
})

it('S1 默认显示 Keeper 名称且保留认证 ID 与原名称搜索', async () => {
  mount(); const user = await open()
  expect(await screen.findByText('生产订阅')).toBeInTheDocument()
  expect(screen.getByLabelText('凭据 auth-a')).toBeChecked()
  for (const query of ['生产订阅', '原名称', 'original.json', 'auth-a']) {
    await user.clear(screen.getByLabelText('搜索凭据'))
    await user.type(screen.getByLabelText('搜索凭据'), query)
    expect(screen.getByLabelText('凭据 auth-a')).toBeChecked()
  }
})

it.each([
  [{ status: 'disabled', items: [] }, '未配置 Keeper'],
  [{ status: 'unavailable', items: [], error_code: 'timeout' }, 'Keeper 名称暂不可用'],
  [{ ...READY, items: [] }, '未找到唯一匹配的 Keeper 身份'],
  [{ ...READY, items: [NAME, { ...NAME, identity_id: '18' }] }, '未找到唯一匹配的 Keeper 身份'],
])('S2 %j 保留 CPA 选择并解释不可同步原因', async (response, reason) => {
  vi.mocked(api.getKeeperAuthNames).mockResolvedValue(response as typeof READY)
  mount(); await open()
  expect(await screen.findByText(reason, { exact: false })).toBeInTheDocument()
  expect(screen.getByLabelText('凭据 auth-a')).toBeChecked()
  expect(screen.getByText('原名称')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: '同步到 Keeper' })).not.toBeInTheDocument()
})

it('S3 同步立即保存，取消绑定后重开仍展示已保存名称且不提交绑定', async () => {
  const updated = { ...NAME, alias: '新名称', display_name: '新名称' }
  vi.mocked(api.patchKeeperAuthName).mockResolvedValue({ status: 'ready', item: updated })
  mount(); const user = await open()
  await user.clear(input()); await user.type(input(), '新名称'); await user.click(sync())
  expect(await screen.findByText('已保存到 Keeper')).toBeInTheDocument()
  expect(api.patchKeeperAuthName).toHaveBeenCalledWith('idx-a', '新名称')
  expect(screen.getByLabelText('凭据 auth-a')).toBeChecked()
  expect(screen.getByLabelText('供应商 codex')).not.toBeChecked()
  await user.click(cancel()); await open()
  expect(await screen.findByText('新名称')).toBeInTheDocument()
  expect(api.postKey).not.toHaveBeenCalled()
})

it('S1 同名文件使用各自认证索引，名称编辑和同步不改变任何勾选值', async () => {
  vi.mocked(listCpaCredentials).mockResolvedValue([FILE, { ...FILE, id: 'auth-b', auth_index: 'idx-b' }])
  vi.mocked(api.patchKeeperAuthName).mockResolvedValue({ status: 'ready', item: { ...NAME, alias: '改名', display_name: '改名' } })
  mount(); const user = await open()
  expect(screen.getByText('生产订阅')).toBeInTheDocument()
  expect(screen.getByText('原名称')).toBeInTheDocument()
  expect(screen.getAllByRole('textbox', { name: '认证自定义名称' })).toHaveLength(1)
  await user.clear(input()); await user.type(input(), '改名'); await user.click(sync())
  await screen.findByText('已保存到 Keeper')
  expect(screen.getByLabelText('凭据 auth-a')).toBeChecked()
  expect(screen.getByLabelText('凭据 auth-b')).not.toBeChecked()
  expect(screen.getByLabelText('供应商 codex')).not.toBeChecked()
  expect(api.postKey).not.toHaveBeenCalled()
})

it('S4 切换配置后旧同步结果不覆盖新配置名称', async () => {
  const response = deferred<{ status: 'ready'; item: typeof NAME }>()
  vi.mocked(api.patchKeeperAuthName).mockReturnValue(response.promise)
  const { rerender } = mount(); const user = await open(); await user.click(sync())
  vi.mocked(api.getKeeperAuthNames).mockResolvedValue({ ...READY, items: [{ ...NAME, alias: '新配置名称', display_name: '新配置名称' }] })
  rerender(<KeysPanel state={{ ...STATE, state_file: '/new/state.json' }} keyOptions={createKeyOptions()} onSaved={vi.fn()} />)
  await screen.findByText('新配置名称')
  await act(async () => response.resolve({ status: 'ready', item: { ...NAME, alias: '旧配置迟到值', display_name: '旧配置迟到值' } }))
  expect(input()).toHaveValue('新配置名称')
  expect(screen.queryByText('旧配置迟到值')).not.toBeInTheDocument()
})

it('S3 空名称恢复 Keeper 默认名称，关闭定向也可以独立同步', async () => {
  vi.mocked(api.patchKeeperAuthName).mockResolvedValue({ status: 'ready', item: { ...NAME, alias: '', display_name: '默认名称' } })
  mount(); const user = await open()
  await user.click(screen.getByRole('switch', { name: '渠道定向总开关' }))
  await user.clear(input()); await user.click(sync())
  expect(await screen.findByText('默认名称')).toBeInTheDocument()
  expect(api.patchKeeperAuthName).toHaveBeenCalledWith('idx-a', '')
})

it.each(['invalid', 'not_found', 'unavailable'] as const)('S4 %s 保留输入且不报告已保存', async status => {
  vi.mocked(api.patchKeeperAuthName).mockResolvedValue({ status, error_code: 'test_failure' })
  mount(); const user = await open()
  await user.clear(input()); await user.type(input(), '未保存内容'); await user.click(sync())
  expect(await screen.findByText(/同步失败/)).toBeInTheDocument()
  expect(input()).toHaveValue('未保存内容')
  expect(screen.queryByText('已保存到 Keeper')).not.toBeInTheDocument()
})

it.each(['ready', 'unknown'] as const)('S4 %s 与审计失败同时清楚展示，不自动重试', async status => {
  vi.mocked(api.patchKeeperAuthName).mockResolvedValue({ status, ...(status === 'ready' ? { item: NAME } : {}),
    audit: { operation_id: 'op-1', recorded: false, error_code: 'audit_finish_failed' } })
  mount(); const user = await open(); await user.click(sync())
  expect(await screen.findByText(status === 'ready' ? '已保存到 Keeper' : '同步结果未确认，请刷新名称核对')).toBeInTheDocument()
  expect(within(screen.getByRole('dialog')).getByRole('alert')).toHaveTextContent('审计结果未写入')
  expect(api.patchKeeperAuthName).toHaveBeenCalledTimes(1)
})

it('S4 Unicode 字符计数和控制字符校验阻止非法提交', async () => {
  mount(); await open()
  fireEvent.change(input(), { target: { value: '😀'.repeat(51) } })
  expect(screen.getByText('51 / 50')).toBeInTheDocument()
  expect(sync()).toBeDisabled()
  fireEvent.change(input(), { target: { value: 'bad\u0001name' } })
  expect(sync()).toBeDisabled()
  expect(api.patchKeeperAuthName).not.toHaveBeenCalled()
})

it('S4 手改期间迟到响应不覆盖新输入', async () => {
  const response = deferred<{ status: 'ready'; item: typeof NAME }>()
  vi.mocked(api.patchKeeperAuthName).mockReturnValue(response.promise)
  mount(); const user = await open(); await user.click(sync())
  expect(sync()).toBeDisabled()
  await user.clear(input()); await user.type(input(), '更新草稿')
  await act(async () => response.resolve({ status: 'ready', item: { ...NAME, alias: '迟到值', display_name: '迟到值' } }))
  expect(input()).toHaveValue('更新草稿')
  expect(screen.queryByText('迟到值')).not.toBeInTheDocument()
  expect(sync()).not.toBeDisabled()
})

it('S4 关闭重开会话后的迟到响应不覆盖新编辑', async () => {
  const response = deferred<{ status: 'ready'; item: typeof NAME }>()
  vi.mocked(api.patchKeeperAuthName).mockReturnValue(response.promise)
  mount(); const user = await open(); await user.click(sync()); await user.click(cancel()); await open()
  await user.clear(input()); await user.type(input(), '新会话')
  await act(async () => response.resolve({ status: 'ready', item: { ...NAME, alias: '迟到值', display_name: '迟到值' } }))
  expect(input()).toHaveValue('新会话')
  expect(screen.queryByText('迟到值')).not.toBeInTheDocument()
})

it.each(['新刷新的名称', NAME.alias])('S4 同配置同身份刷新先完成时旧 PATCH 不得覆盖名称视图：%s', async refreshedAlias => {
  const response = deferred<Awaited<ReturnType<typeof api.patchKeeperAuthName>>>()
  vi.mocked(api.patchKeeperAuthName).mockReturnValue(response.promise)
  mount(); const user = await open()
  await user.clear(input()); await user.type(input(), '旧请求的名称'); await user.click(sync())
  await user.clear(input()); await user.type(input(), '刷新期间的手工草稿')
  vi.mocked(api.refreshKeeperAuthNames).mockResolvedValue({ ...READY,
    items: [{ ...NAME, alias: refreshedAlias, display_name: refreshedAlias }] })
  await user.click(screen.getByRole('button', { name: '刷新名称' }))
  await waitFor(() => expect(screen.getByRole('button', { name: '刷新名称' })).not.toBeDisabled())
  expect(screen.getByText(refreshedAlias)).toBeInTheDocument()
  expect(input()).toHaveValue('刷新期间的手工草稿')
  await act(async () => response.resolve({ status: 'ready',
    item: { ...NAME, alias: '旧请求的名称', display_name: '旧请求的名称' } }))
  expect(screen.getByText(refreshedAlias)).toBeInTheDocument()
  expect(input()).toHaveValue('刷新期间的手工草稿')
  expect(screen.queryByText('已保存到 Keeper')).not.toBeInTheDocument()
  expect(screen.getByLabelText('凭据 auth-a')).toBeChecked()
})

it('S4 新刷新先完成时旧 PATCH 不得覆盖名称视图', async () => {
  const response = deferred<Awaited<ReturnType<typeof api.patchKeeperAuthName>>>()
  vi.mocked(api.patchKeeperAuthName).mockReturnValue(response.promise)
  mount(); const user = await open()
  await user.clear(input()); await user.type(input(), '旧请求的名称'); await user.click(sync())
  vi.mocked(api.refreshKeeperAuthNames).mockResolvedValue({ ...READY,
    items: [{ ...NAME, alias: '新刷新的名称', display_name: '新刷新的名称' }] })
  await user.click(screen.getByRole('button', { name: '刷新名称' }))
  await screen.findByText('新刷新的名称')
  await act(async () => response.resolve({ status: 'ready',
    item: { ...NAME, alias: '旧请求的名称', display_name: '旧请求的名称' } }))
  expect(screen.getByText('新刷新的名称')).toBeInTheDocument()
  expect(screen.queryByText('已保存到 Keeper')).not.toBeInTheDocument()
  expect(screen.getByLabelText('凭据 auth-a')).toBeChecked()
  await user.click(cancel()); await open()
  expect(input()).toHaveValue('新刷新的名称')
})

it('S4 较早发起的刷新响应不覆盖后来同步成功的名称', async () => {
  const response = deferred<typeof READY>()
  vi.mocked(api.refreshKeeperAuthNames).mockReturnValue(response.promise)
  vi.mocked(api.patchKeeperAuthName).mockResolvedValue({ status: 'ready', item: { ...NAME, alias: '最新值', display_name: '最新值' } })
  mount(); const user = await open()
  await user.click(screen.getByRole('button', { name: '刷新名称' }))
  await user.clear(input()); await user.type(input(), '最新值'); await user.click(sync())
  await screen.findByText('已保存到 Keeper')
  await act(async () => response.resolve(READY))
  expect(screen.getByText('最新值')).toBeInTheDocument()
  expect(input()).toHaveValue('最新值')
  await waitFor(() => expect(screen.getByRole('button', { name: '刷新名称' })).not.toBeDisabled())
})
