import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import path from 'node:path'
import { chromium } from 'playwright'
const base='http://127.0.0.1:18317', keeper='http://127.0.0.1:31808/keeper'
const prefix='/v0/management/plugins/model-mapper-plus'
const headers={Authorization:'Bearer keeper-aliases-management-test','Content-Type':'application/json'}
const evidence=path.resolve('.spec-dev/2026-09-05-01-keeper-key-aliases/acceptance')
await fs.mkdir(evidence,{recursive:true})
const records=[]
async function cpa(url,method='GET',body) { const r=await fetch(base+url,{method,headers,body:body===undefined?undefined:JSON.stringify(body)}); assert.equal(r.status,200,await r.clone().text()); return r.json() }
function check(name,detail){records.push({name,status:'pass',detail});console.log('PASS',name)}
const meta=await cpa('/v0/management/plugins'); const fields=meta.plugins[0].config_fields
assert(fields.some(f=>f.name==='usage_keeper_url'&&f.type==='string')); assert(fields.some(f=>f.name==='usage_keeper_password_env'&&f.type==='string'))
check('CPA 配置元数据可编辑','两个字段均为 string')
await cpa(prefix+'/config','PATCH',{usage_keeper_url:'',usage_keeper_password_env:''})
for(let i=0;i<40;i++){if((await cpa(prefix+'/keeper/key-aliases')).status==='disabled')break;await new Promise(r=>setTimeout(r,150));if(i===39)throw Error('configuration disable timeout')}
assert.equal((await cpa(prefix+'/config')).usage_keeper_url,'')
await cpa(prefix+'/config','PATCH',{usage_keeper_url:'http://host.docker.internal:31808/keeper',usage_keeper_password_env:''})
for(let i=0;i<40;i++){if((await cpa(prefix+'/keeper/key-aliases')).status==='ready')break;await new Promise(r=>setTimeout(r,150));if(i===39)throw Error('configuration enable timeout')}
check('CPA 设置接口保存与热更新','禁用→启用立即生效；空密码字段使用默认环境变量')
const login=await fetch(keeper+'/api/v1/auth/login',{method:'POST',headers:{'Content-Type':'application/json','X-CPA-Usage-Keeper-Request':'fetch'},body:JSON.stringify({password:'keeper-aliases-login-test'})});assert.equal(login.status,204)
const kh={'Content-Type':'application/json','X-CPA-Usage-Keeper-Request':'fetch',Cookie:login.headers.getSetCookie().map(v=>v.split(';')[0]).join('; ')}
const settings=await (await fetch(keeper+'/api/v1/usage/api-keys/settings',{headers:kh})).json();assert.equal(settings.items.length,2)
const first=settings.items.find(i=>i.apiKey.endsWith('0001')), second=settings.items.find(i=>i.apiKey.endsWith('0002'))
async function alias(item,value){ const r=await fetch(keeper+'/api/v1/usage/api-keys/'+item.id,{method:'PATCH',headers:kh,body:JSON.stringify({keyAlias:value})});assert.equal(r.status,200) }
await alias(first,'生产服务'); await alias(second,'开发服务')
await cpa(prefix+'/keeper/key-aliases/refresh','POST')
for (const binding of (await cpa(prefix+'/state')).key_bindings) await cpa(prefix+'/keys?key='+encodeURIComponent(binding.key),'DELETE')
check('真实 Keeper 登录、子路径与别名读取','AUTH_ENABLED=true，APP_BASE_PATH=/keeper，使用独立测试数据')
const browser=await chromium.launch({executablePath:'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',headless:true})
const page=await browser.newPage({viewport:{width:1360,height:1000}})
const errors=[];page.on('pageerror',e=>errors.push(e.message))
const ui=base+'/v0/resource/plugins/model-mapper-plus/index.html'
async function loginUI(){await page.goto(ui);if(await page.getByPlaceholder('CPA management key').isVisible()){await page.getByPlaceholder('CPA management key').fill('keeper-aliases-management-test');await page.getByRole('button',{name:'登录',exact:true}).click()}await page.getByText('已连接',{exact:true}).waitFor()}
async function openNew(){await page.getByText('Key 绑定',{exact:true}).click();await page.getByRole('button',{name:'+ 新增绑定'}).click()}
const modal=()=>page.getByRole('dialog',{name:/新增绑定|编辑绑定/})
async function selectKey(query,name){await modal().getByRole('combobox',{name:'API Key'}).click();await page.locator('.semi-select-input input').fill(query);await page.getByRole('option',{name}).click();await page.waitForFunction(()=>!document.querySelector('.semi-select-option-list'))}
try {
 await loginUI();await openNew(); await selectKey('生产',/生产\s*服务.*0001/)
 await modal().getByLabel('绑定别名').fill('本地草稿')
 await modal().getByRole('button',{name:'从 Keeper 同步'}).click()
 await page.waitForFunction(()=>document.querySelector('[aria-label="绑定别名"]')?.value==='生产服务')
 assert.equal((await cpa(prefix+'/state')).key_bindings.length,0)
 await modal().getByRole('button',{name:'cancel',exact:true}).click()
 assert.equal((await cpa(prefix+'/state')).key_bindings.length,0)
 check('同步仅修改草稿且取消不保存','真实 Keeper 值覆盖本地草稿，state 仍为空')
 await openNew();await selectKey('生产',/生产\s*服务.*0001/)
 await modal().getByRole('button',{name:'从 Keeper 同步'}).click()
 await page.waitForFunction(()=>document.querySelector('[aria-label="绑定别名"]')?.value==='生产服务')
 await modal().getByRole('button',{name:'confirm',exact:true}).click()
 await page.getByRole('button',{name:'编辑',exact:true}).first().waitFor()
 let state=await cpa(prefix+'/state');assert.equal(state.key_bindings[0].key,first.apiKey);assert.equal(state.key_bindings[0].alias,'生产服务');assert.equal(state.persisted,true)
 check('同步保存完整 Key 与别名','绑定保存 API 与 state 持久化成功')
 await alias(first,'Keeper 最新 & <中文>');await page.getByRole('button',{name:'编辑',exact:true}).first().click()
 await modal().getByLabel('绑定别名').fill('临时手改')
 await modal().getByRole('button',{name:'从 Keeper 同步'}).click()
 await page.waitForFunction(()=>document.querySelector('[aria-label="绑定别名"]')?.value==='Keeper 最新 & <中文>')
 await modal().getByRole('button',{name:'confirm',exact:true}).click()
 await page.getByRole('button',{name:'编辑',exact:true}).first().waitFor()
 check('主动同步绕过缓存并保留中文和特殊字符','Keeper 修改后立即同步当前值')
 await page.getByRole('button',{name:'编辑',exact:true}).first().click();await modal().getByLabel('绑定别名').fill('插件本地优先');await modal().getByLabel('编辑绑定：Fast 允许').click();await modal().getByRole('button',{name:'confirm',exact:true}).click()
 await page.getByText('规则试跑',{exact:true}).click()
 const keySelect=page.getByRole('combobox',{name:'API Key'})
 await keySelect.click();await page.locator('.semi-select-input input').fill('Keeper 最新')
 await page.getByRole('option',{name:/插件本地优先.*0001/}).click()
 await page.waitForFunction(()=>!document.querySelector('.semi-select-option-list'))
 await page.waitForFunction(()=>document.querySelector('[aria-labelledby] .semi-select-selection-text')?.textContent?.includes('插件本地优先'))
 await page.getByPlaceholder('模型，如 claude-opus-4-5(max)').fill('test-model')
 let request=page.waitForRequest(r=>r.url().endsWith('/preview')&&r.method()==='POST')
 await page.getByRole('button',{name:'试跑',exact:true}).click();assert.equal((await request).postDataJSON().key,first.apiKey)
 await keySelect.hover();await keySelect.locator('.semi-select-clear').click()
 await page.waitForFunction(()=>document.querySelector('.semi-select-selection-placeholder')?.textContent?.includes('Key'))
 request=page.waitForRequest(r=>r.url().endsWith('/preview')&&r.method()==='POST')
 await page.getByRole('button',{name:'试跑',exact:true}).click();assert.equal((await request).postDataJSON().key,undefined)
 assert.equal((await cpa(prefix+'/state')).key_bindings.find(k=>k.key===first.apiKey).fast_allowed,false)
 check('试跑搜索双别名、本地优先、完整 Key 与清空','搜索 Keeper 别名显示本地别名，传入完整 Key；清空后不传 Key')
 await page.screenshot({path:path.join(evidence,'preview-light.png'),fullPage:true})
 await openNew();await modal().getByRole('combobox',{name:'API Key'}).click();await page.locator('.semi-select-input input').fill('manual-key-xyz');await page.locator('.semi-select-input input').press('Enter')
 await page.waitForFunction(()=>!document.querySelector('.semi-select-option-list'))
 assert((await modal().getByRole('combobox',{name:'API Key'}).innerText()).includes('manu'))
 await modal().getByLabel('绑定别名').fill('手动测试');await modal().getByRole('button',{name:'confirm',exact:true}).click()
 state=await cpa(prefix+'/state');assert(state.key_bindings.some(k=>k.key==='manual-key-xyz'))
 check('手动完整 Key 键盘创建','无匹配输入后 Enter 不选择旧 Key')
 await alias(second,'长别名测试'.repeat(24));await page.getByRole('button',{name:'+ 新增绑定'}).click();await modal().getByRole('button',{name:'刷新别名'}).click();await page.getByText('Keeper 别名已加载',{exact:true}).waitFor()
 await modal().getByRole('combobox',{name:'API Key'}).click()
 await page.waitForFunction(()=>document.querySelectorAll('.semi-select-option').length===2)
 await page.waitForTimeout(400)
 assert(await page.evaluate(()=>document.documentElement.scrollWidth <= window.innerWidth))
 await page.screenshot({path:path.join(evidence,'binding-light-long.png'),fullPage:true})
 await page.evaluate(()=>{document.documentElement.setAttribute('data-theme','dark');document.body.setAttribute('theme-mode','dark')})
 await page.waitForTimeout(200)
 await page.screenshot({path:path.join(evidence,'binding-dark-long.png'),fullPage:true})
 check('明暗主题与长别名渲染','已生成真实浏览器截图，视觉判读单独记录')
 assert.deepEqual(errors,[]);check('浏览器运行错误','pageerror 为空；真实 Select 动画启用')
} catch (e) { console.log('FAILURE_BODY', (await page.locator('body').innerText()).slice(-2500)); await page.screenshot({path:path.join(evidence,'failure.png'),fullPage:true}); throw e } finally {await fs.writeFile(path.join(evidence,'live-results.json'),JSON.stringify({at:new Date().toISOString(),browser:browser.version(),records,errors},null,2));await browser.close()}
