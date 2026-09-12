import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import path from 'node:path'
import os from 'node:os'
import { createRequire } from 'node:module'

const toolRequire = createRequire(path.join(process.env.MAPPER_BROWSER_TOOLS, 'package.json'))
const { chromium } = toolRequire('playwright')
const { expect } = toolRequire('playwright/test')
const base = 'http://127.0.0.1:31819'
const prefix = '/v0/management/plugins/model-mapper-plus'
const run = process.env.MAPPER_ACCEPTANCE_RUN || 'run-01'
assert.match(run, /^run-\d{2}$/)
const output = path.resolve('.spec-dev/2026-09-12-01-admin-audit-keeper-names/acceptance/browser', run)
await fs.mkdir(output, { recursive: true })
const records = [], errors = [], network = []
const headers = { 'Content-Type': 'application/json', Authorization: 'Bearer local-admin' }
async function api(endpoint, method = 'GET', body) {
  const url = new URL(endpoint, base)
  assert.equal(url.origin, base)
  const response = await fetch(url, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) })
  const text = await response.text()
  assert.equal(response.status, 200, `${method} ${endpoint}: ${response.status} ${text}`)
  return JSON.parse(text)
}
async function control(mode) {
  const response = await fetch(base + '/fixture-control', { method: 'POST', headers, body: JSON.stringify({ mode }) })
  assert.equal(response.status, 204)
}
async function keeper() {
  const url = new URL(process.env.MAPPER_FAKE_KEEPER_URL)
  assert.equal(url.hostname, '127.0.0.1')
  return (await (await fetch(url + 'api/v1/usage/identities')).json()).identities[0]
}
async function check(name, fn) {
  try { await fn(); records.push({ name, result: 'pass' }); console.log('PASS', name) }
  catch (error) { records.push({ name, result: 'fail', error: String(error.stack || error) }); console.error('FAIL', name, error.message); throw error }
}
const browser = await chromium.launch({ executablePath: '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', headless: true })
const context = await browser.newContext({ viewport: { width: 1360, height: 1000 } })
await context.route('**/*', route => {
  const url = new URL(route.request().url())
  return url.hostname === '127.0.0.1' || ['data:', 'blob:'].includes(url.protocol) ? route.continue() : route.abort()
})
const page = await context.newPage()
page.setDefaultTimeout(15000)
page.on('pageerror', error => errors.push({ kind: 'pageerror', message: error.message }))
page.on('console', message => { if (message.type() === 'error') errors.push({ kind: 'console', message: message.text() }) })
page.on('response', response => network.push({ method: response.request().method(), url: response.url(), status: response.status() }))
page.on('request', request => { if (request.method() !== 'GET') network.push({ method: request.method(), url: request.url(), phase: 'request' }) })
let ui
async function load(theme = 'white', width = 1360) {
  await page.setViewportSize({ width, height: 1000 })
  await page.goto(base + '/fixture?theme=' + theme)
  ui = page.frameLocator('#mapper')
  await ui.getByPlaceholder('CPA management key').or(ui.getByText('已连接', { exact: true })).first().waitFor({ state: 'visible' })
  if (await ui.getByPlaceholder('CPA management key').isVisible()) {
    await ui.getByPlaceholder('CPA management key').fill('local-admin')
    await ui.getByRole('button', { name: '登录', exact: true }).click()
  }
  await expect(ui.getByText('已连接', { exact: true })).toBeVisible()
}
const nav = name => ui.getByRole('menu').getByText(name, { exact: true }).click()
async function openOriginal() {
  await nav('Key 绑定')
  await ui.getByRole('row').filter({ hasText: '验收Key' }).getByRole('button', { name: '编辑', exact: true }).click()
  await ui.getByRole('tab', { name: '渠道定向', exact: true }).click()
  await expect(ui.getByLabel('凭据 auth-a')).toBeChecked()
}
const nameInput = () => ui.getByRole('textbox', { name: '认证自定义名称' })
const sync = () => ui.getByRole('button', { name: '同步到 Keeper', exact: true }).click()
const cancel = () => ui.getByRole('dialog').getByRole('button', { name: 'cancel', exact: true }).click()
async function screenshot(name) { await page.screenshot({ path: path.join(output, name), fullPage: true, animations: 'disabled' }) }
async function geometry() {
  const frame = page.frames().find(frame => frame.url().includes('/v0/resource/plugins/model-mapper-plus/index.html'))
  assert.ok(frame)
  return frame.evaluate(() => ({ width: innerWidth, scrollWidth: document.documentElement.scrollWidth,
    theme: document.body.getAttribute('theme-mode'),
    dialog: [...document.querySelectorAll('[role=dialog]')].map(el => { const r = el.getBoundingClientRect(); return { left: r.left, right: r.right, width: r.width } }) }))
}
try {
  await control('')
  const existing = await api(prefix + '/state')
  if (existing.key_bindings.some(item => item.key === 'fake-browser-extra')) await api(prefix + '/keys?key=fake-browser-extra', 'DELETE')
  await api(prefix + '/rules', 'PUT', { global: '', claude: '', codex: '', openai: '' })
  const prepared = await api(prefix + '/keeper/auth-names', 'PATCH', { auth_index: 'idx-a', alias: '验收订阅' })
  assert.equal(prepared.status, 'ready')
  await load()
  await check('默认名称、立即同步及取消绑定独立', async () => {
    await openOriginal()
    await expect(nameInput()).toHaveValue('验收订阅')
    await nameInput().fill('浏览器验收 & 中文')
    await sync()
    await expect(ui.getByText('已保存到 Keeper', { exact: true })).toBeVisible()
    assert.equal((await keeper()).alias, '浏览器验收 & 中文')
    await expect(ui.getByLabel('凭据 auth-a')).toBeChecked()
    await cancel()
    const state = await api(prefix + '/state')
    assert.equal(state.key_bindings[0].alias, '验收Key')
    assert.deepEqual(state.key_bindings[0].channel_target.auth_ids, ['auth-a'])
    await openOriginal()
    await expect(nameInput()).toHaveValue('浏览器验收 & 中文')
    await screenshot('name-saved.png')
  })
  await check('清空名称恢复 Keeper 默认显示', async () => {
    await nameInput().fill('')
    await sync()
    await expect(ui.getByText('已保存到 Keeper', { exact: true })).toBeVisible()
    const identity = await keeper()
    assert.equal(identity.alias, '')
    assert.equal(identity.displayName, '原名称')
    await cancel()
  })
  await check('规则真实保存及试跑不产生审计', async () => {
    await nav('规则管理')
    await fs.writeFile(path.join(output, 'rule-aria.yaml'), await ui.locator('body').ariaSnapshot())
    await ui.getByRole('button', { name: /添加映射/ }).click()
    await ui.getByPlaceholder('find（* 捕获，(max) 等后缀可参与）').fill('before')
    await ui.getByPlaceholder('replace（$1 引用捕获）').fill('after')
    await ui.getByRole('button', { name: '保存', exact: true }).click()
    await expect(ui.getByText('规则已保存', { exact: true })).toBeVisible()
    assert.equal((await api(prefix + '/state')).rules.global, 'before=>after')
    const count = (await api(prefix + '/audit')).total
    const preview = await api(prefix + '/preview', 'POST', { model: 'before', format: 'openai-response' })
    assert.equal(preview.final, 'after')
    assert.equal((await api(prefix + '/audit')).total, count)
  })
  await check('Key 新增、编辑、开关及删除均记录', async () => {
    await nav('Key 绑定')
    await ui.getByRole('button', { name: '+ 新增绑定', exact: true }).click()
    const dialog = ui.getByRole('dialog')
    const select = dialog.getByRole('combobox').first()
    await select.click()
    await select.locator('input').fill('fake-browser-extra')
    await fs.writeFile(path.join(output, 'key-option-aria.yaml'), await ui.locator('body').ariaSnapshot())
    await ui.getByRole('option', { name: /使用手动 Key：\s*fake-browser-extra/ }).click()
    await expect(ui.getByRole('option', { name: /使用手动 Key：\s*fake-browser-extra/ })).toBeHidden()
    await expect(ui.getByText('同步会替换输入框内容，保存绑定后生效', { exact: true })).toBeVisible()
    await dialog.getByLabel('绑定别名').fill('浏览器新增')
    await dialog.getByRole('button', { name: 'confirm', exact: true }).click()
    await fs.writeFile(path.join(output, 'after-create-click.txt'), await ui.locator('body').innerText())
    let row = ui.getByRole('row').filter({ hasText: '浏览器新增' })
    await expect(row).toBeVisible()
    await row.getByRole('button', { name: '编辑', exact: true }).click()
    await ui.getByRole('dialog').getByLabel('绑定别名').fill('浏览器修改')
    await ui.getByRole('dialog').getByRole('button', { name: 'confirm', exact: true }).click()
    row = ui.getByRole('row').filter({ hasText: '浏览器修改' })
    await row.getByRole('switch', { name: '启用规则：浏览器修改', exact: true }).click()
    await expect(row.getByRole('switch', { name: '启用规则：浏览器修改', exact: true })).not.toBeChecked()
    await row.getByRole('button', { name: '删除', exact: true }).click()
    await ui.getByRole('dialog').getByRole('button', { name: 'confirm', exact: true }).click()
    await expect(row).toHaveCount(0)
    assert.equal((await api(prefix + '/state')).key_bindings.length, 1)
    const log = await api(prefix + '/audit')
    assert.ok(log.items.some(item => item.action === 'create'))
    assert.ok(log.items.some(item => item.action === 'delete'))
    assert.ok(log.items.some(item => item.changes.enabled?.after === false))
  })
  await check('日志目录不可写时 UI 报错且零副作用', async () => {
    const before = await api(prefix + '/state')
    const statePath = before.state_file
    assert.ok(statePath.startsWith(os.tmpdir()) && statePath.includes('/TestAdminAcceptanceServe'))
    const dir = path.join(path.dirname(statePath), 'model-mapper-plus-audit'), backup = dir + '.saved'
    await fs.rename(dir, backup)
    try {
      await fs.writeFile(dir, 'owned local fixture barrier')
      await nav('规则管理')
      await ui.getByRole('button', { name: '保存', exact: true }).click()
      await expect(ui.getByText('audit_unavailable', { exact: true })).toBeVisible()
      assert.deepEqual(await api(prefix + '/state'), before)
      await screenshot('audit-blocked.png')
    } finally { await fs.unlink(dir); await fs.rename(backup, dir) }
  })
  await check('Keeper 写入失联返回未知而非回滚', async () => {
    await openOriginal()
    await nameInput().fill('未知结果验收')
    await control('timeout')
    await sync()
    await expect(ui.getByText('同步结果未确认，请刷新名称核对', { exact: true })).toBeVisible({ timeout: 15000 })
    assert.equal((await keeper()).alias, '未知结果验收')
    const log = await api(prefix + '/audit')
    assert.equal(log.items[0].outcome, 'unknown')
    assert.equal(log.items[0].changed, null)
    await screenshot('keeper-unknown.png')
    await cancel()
  })
  await check('业务成功与审计终态写入失败分别显示', async () => {
    await openOriginal()
    await nameInput().fill('同步成功但审计告警')
    await control('finish_sync')
    await sync()
    await expect(ui.getByText('已保存到 Keeper', { exact: true })).toBeVisible()
    await expect(ui.getByText('审计结果未写入', { exact: true })).toBeVisible()
    assert.equal((await keeper()).alias, '同步成功但审计告警')
    assert.equal((await api(prefix + '/audit')).items[0].outcome, 'unknown')
    await screenshot('keeper-audit-warning.png')
    await cancel()
  })
  await check('审计日期、分页、详情和直接日文件', async () => {
    const current = await api(prefix + '/state')
    for (let i = 0; i < 21; i++) await api(prefix + '/rules', 'PUT', current.rules)
    const log = await api(prefix + '/audit')
    await nav('操作审计')
    await expect(ui.getByText(`共 ${log.total} 项 · 第 1 页`, { exact: true })).toBeVisible()
    await ui.getByRole('button', { name: '下一页', exact: true }).click()
    await expect(ui.getByText(`共 ${log.total} 项 · 第 2 页`, { exact: true })).toBeVisible()
    const nameRow = ui.getByRole('row').filter({ hasText: 'keeper_auth_name' }).filter({ hasText: '成功' }).first()
    await nameRow.getByRole('button', { name: '查看详情', exact: true }).click()
    const detail = ui.getByRole('dialog', { name: '操作详情' })
    await expect(detail.getByText('变更前', { exact: true }).first()).toBeVisible()
    await expect(detail.getByText('变更后', { exact: true }).first()).toBeVisible()
    await screenshot('audit-details.png')
    await detail.getByRole('button', { name: '关闭', exact: true }).click()
    await ui.getByLabel('审计日期').fill('2001-01-01')
    await expect(ui.getByText('当天暂无操作记录', { exact: true })).toBeVisible()
    await ui.getByLabel('审计日期').fill(log.date)
    await expect(ui.getByText(`共 ${log.total} 项 · 第 1 页`, { exact: true })).toBeVisible()
    const file = path.join(path.dirname(current.state_file), 'model-mapper-plus-audit', log.date + '.jsonl')
    const content = await fs.readFile(file, 'utf8')
    assert.ok(content.trim().split('\n').length >= log.total * 2)
    assert.equal(content.includes('fake-client-key'), false)
    assert.equal(content.includes('fake-browser-extra'), false)
    await fs.writeFile(path.join(output, 'daily-log.jsonl'), content)
    await fs.writeFile(path.join(output, 'audit.json'), JSON.stringify(await api(prefix + '/audit?page_size=100'), null, 2))
  })
  const longName = '长名称' + '订阅'.repeat(20)
  await api(prefix + '/keeper/auth-names', 'PATCH', { auth_index: 'idx-a', alias: longName })
  for (const width of [1360, 390]) for (const theme of ['white', 'dark']) {
    const tag = `${width}-${theme}`
    await load(theme, width)
    await openOriginal()
    await expect(nameInput()).toHaveValue(longName)
    const nameGeometry = await geometry()
    await screenshot(`names-${tag}.png`)
    await cancel()
    await nav('操作审计')
    await expect(ui.getByText(/共 \d+ 项 · 第 1 页/)).toBeVisible()
    const auditGeometry = await geometry()
    await screenshot(`audit-${tag}.png`)
    const okay = [nameGeometry, auditGeometry].every(g => g.scrollWidth <= g.width + 1 && g.dialog.every(d => d.left >= -1 && d.right <= g.width + 1))
    assert.equal(nameGeometry.theme, theme === 'dark' ? 'dark' : null)
    records.push({ name: `布局 ${tag}`, result: okay ? 'pass' : 'fail', nameGeometry, auditGeometry })
    console.log(okay ? 'PASS' : 'FAIL', 'layout', tag, JSON.stringify({ nameGeometry, auditGeometry }))
  }
  assert.equal(errors.filter(error => error.kind === 'pageerror').length, 0, JSON.stringify(errors))
} catch (error) {
  await screenshot('failure.png').catch(() => {})
  await fs.writeFile(path.join(output, 'failure-dom.txt'), await (ui ? ui.locator('body') : page.locator('body')).innerText()).catch(() => {})
  if (!records.some(record => record.result === 'fail')) records.push({ name: 'workflow', result: 'fail', error: String(error.stack || error) })
  process.exitCode = 1
} finally {
  await fs.writeFile(path.join(output, 'results.json'), JSON.stringify({ browser: browser.version(), run, records, errors, network }, null, 2))
  await browser.close()
}
if (records.some(record => record.result === 'fail')) process.exitCode = 1
