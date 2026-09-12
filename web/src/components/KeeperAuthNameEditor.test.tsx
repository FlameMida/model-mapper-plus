import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import KeeperAuthNameEditor from './KeeperAuthNameEditor'
import { api, type KeeperAuthNameUpdateResponse } from '../api'

vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>()
  return { ...actual, api: { ...actual.api, patchKeeperAuthName: vi.fn() } }
})
const NAME = { identity_id: '17', auth_index: 'idx-a', alias: '旧别名', display_name: '旧别名' }
const input = () => screen.getByRole('textbox', { name: '认证自定义名称' })
const button = () => screen.getByRole('button', { name: '同步到 Keeper' })
function deferred() {
  let resolve!: (value: KeeperAuthNameUpdateResponse) => void
  const promise = new Promise<KeeperAuthNameUpdateResponse>(done => { resolve = done })
  return { promise, resolve }
}
beforeEach(() => vi.resetAllMocks())
afterEach(() => vi.unstubAllGlobals())

it.each([false, true])('S9 非2xx显示原错误，仅存在audit时提示审计失败：%s', async withAudit => {
  const actual = await vi.importActual<typeof import('../api')>('../api')
  vi.mocked(api.patchKeeperAuthName).mockImplementation(actual.api.patchKeeperAuthName)
  vi.stubGlobal('fetch', vi.fn(async () => ({ ok: false, status: withAudit ? 400 : 503,
    text: async () => JSON.stringify({ error: withAudit ? '名称不合法' : 'audit_unavailable',
      ...(withAudit ? { audit: { operation_id: 'name-op', recorded: false } } : {}) }),
  })))
  const onSaved = vi.fn()
  render(<KeeperAuthNameEditor authIndex="idx-a" name={NAME} onSaved={onSaved} />)
  await userEvent.setup().click(button())
  expect(await screen.findByText(withAudit ? '同步失败：名称不合法' : '同步失败：audit_unavailable')).toBeInTheDocument()
  if (withAudit) expect(screen.getByRole('alert')).toHaveTextContent('审计结果未写入')
  else expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  expect(screen.queryByText(/同步结果未确认|已保存到 Keeper/)).not.toBeInTheDocument()
  expect(onSaved).not.toHaveBeenCalled()
})

it('S4 索引 A→B→A 丢弃旧提交结果，保留新编辑', async () => {
  const response = deferred(); vi.mocked(api.patchKeeperAuthName).mockReturnValue(response.promise)
  const onSaved = vi.fn(); const user = userEvent.setup()
  const { rerender } = render(<KeeperAuthNameEditor authIndex="idx-a" name={NAME} onSaved={onSaved} />)
  await user.click(button())
  rerender(<KeeperAuthNameEditor authIndex="idx-b" name={{ ...NAME, auth_index: 'idx-b', identity_id: '18', alias: 'B' }} onSaved={onSaved} />)
  expect(input()).toHaveValue('B')
  rerender(<KeeperAuthNameEditor authIndex="idx-a" name={NAME} onSaved={onSaved} />)
  await user.clear(input()); await user.type(input(), '新编辑')
  await act(async () => response.resolve({ status: 'ready', item: { ...NAME, alias: '旧提交' } }))
  expect(input()).toHaveValue('新编辑'); expect(onSaved).not.toHaveBeenCalled()
})

it('S4 同步网络中断明确结果未确认，不重试且保留输入', async () => {
  vi.mocked(api.patchKeeperAuthName).mockRejectedValue(new Error('network error'))
  const onSaved = vi.fn(); const user = userEvent.setup()
  render(<KeeperAuthNameEditor authIndex="idx-a" name={NAME} onSaved={onSaved} />)
  await user.click(button())
  expect(await screen.findByText('同步结果未确认，请刷新名称核对')).toBeInTheDocument()
  expect(input()).toHaveValue('旧别名'); expect(onSaved).not.toHaveBeenCalled()
  expect(api.patchKeeperAuthName).toHaveBeenCalledTimes(1)
})

it('S4 50个非BMP字符允许提交，重复点击只发出一次PATCH', async () => {
  const response = deferred(); vi.mocked(api.patchKeeperAuthName).mockReturnValue(response.promise)
  const onSaved = vi.fn(); const user = userEvent.setup()
  render(<KeeperAuthNameEditor authIndex="idx-a" name={NAME} onSaved={onSaved} />)
  fireEvent.change(input(), { target: { value: '😀'.repeat(50) } })
  expect(screen.getByText('50 / 50')).toBeInTheDocument()
  await user.dblClick(button())
  expect(api.patchKeeperAuthName).toHaveBeenCalledTimes(1)
  expect(api.patchKeeperAuthName).toHaveBeenCalledWith('idx-a', '😀'.repeat(50))
  await act(async () => response.resolve({ status: 'ready', item: { ...NAME, alias: '😀'.repeat(50), display_name: '😀'.repeat(50) } }))
  expect(screen.getByText('已保存到 Keeper')).toBeInTheDocument()
})

it('S4 HTTP成功但ready缺少匹配item时仍不可报告已保存', async () => {
  vi.mocked(api.patchKeeperAuthName).mockResolvedValue({ status: 'ready', item: { ...NAME, auth_index: 'idx-b' } })
  const onSaved = vi.fn(); const user = userEvent.setup()
  render(<KeeperAuthNameEditor authIndex="idx-a" name={NAME} onSaved={onSaved} />)
  await user.click(button())
  expect(await screen.findByText('同步结果未确认，请刷新名称核对')).toBeInTheDocument()
  expect(onSaved).not.toHaveBeenCalled()
})
