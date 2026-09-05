import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import path from 'node:path'
import { execFileSync } from 'node:child_process'
import { chromium } from 'playwright'
const base='http://127.0.0.1:18317', prefix='/v0/management/plugins/model-mapper-plus'
const headers={Authorization:'Bearer keeper-aliases-management-test','Content-Type':'application/json'}
const out=path.resolve('.spec-dev/2026-09-05-01-keeper-key-aliases/acceptance')
const records=[]
async function cpa(url,method='GET',body){const r=await fetch(base+url,{headers,method,body:body===undefined?undefined:JSON.stringify(body)});assert.equal(r.status,200);return r.json()}
const before=await cpa(prefix+'/state');assert(before.key_bindings.some(k=>k.alias==='插件本地优先'))
const bytes=await fs.readFile('.test-cpa/keeper-aliases/cpa-data/state.json','utf8')
assert(!bytes.includes('usage_keeper_'));assert(!bytes.includes('keeper-aliases-login-test'))
execFileSync('docker',['restart','model-mapper-keeper-aliases-qa'])
for(let i=0;i<50;i++){try{assert.deepEqual((await cpa(prefix+'/state')).key_bindings,before.key_bindings);break}catch(e){if(i===49)throw e;await new Promise(r=>setTimeout(r,200))}}
records.push({name:'CPA 重启持久化',status:'pass',detail:'重启独立 Linux CPA，所有绑定别名与完整 Key 保留；state 无 Keeper 配置或密码'})
const browser=await chromium.launch({executablePath:'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',headless:true})
const page=await browser.newPage({viewport:{width:1360,height:1000}})
try{
 await page.goto(base+'/v0/resource/plugins/model-mapper-plus/index.html')
 await page.getByPlaceholder('CPA management key').fill('keeper-aliases-management-test');await page.getByRole('button',{name:'登录',exact:true}).click()
 await page.getByText('已连接',{exact:true}).waitFor();await page.getByText('Key 绑定',{exact:true}).click()
 await page.getByRole('button',{name:'编辑',exact:true}).first().click()
 const modal=page.getByRole('dialog',{name:/编辑绑定/});await modal.getByLabel('绑定别名').fill('失败时保留草稿')
 // Only the dedicated test process is stopped, by the PID listening on our port.
 const pids=execFileSync('lsof',['-tiTCP:31808','-sTCP:LISTEN'],{encoding:'utf8'}).trim().split(/\s+/)
 assert.equal(pids.length,1);process.kill(Number(pids[0]),'SIGTERM')
 await modal.getByRole('button',{name:'从 Keeper 同步'}).click()
 await page.getByText('无法连接 Keeper',{exact:true}).first().waitFor()
 assert.equal(await modal.getByLabel('绑定别名').inputValue(),'失败时保留草稿')
 assert.equal(await page.getByPlaceholder('CPA management key').count(),0)
 await modal.getByRole('button',{name:'confirm',exact:true}).click()
 for(let i=0;i<40;i++){if((await cpa(prefix+'/state')).key_bindings.some(k=>k.alias==='失败时保留草稿'))break;await new Promise(r=>setTimeout(r,100));if(i===39)throw Error('save failed')}
 records.push({name:'真实 Keeper 停机隔离',status:'pass',detail:'停机后同步提示连接失败，保留草稿，CPA 登录与绑定保存正常'})
 await page.getByText('规则试跑',{exact:true}).click();await page.getByRole('combobox',{name:'API Key'}).click()
 await page.getByRole('option',{name:/0002/}).waitFor()
 await page.screenshot({path:path.join(out,'keeper-offline.png'),fullPage:true})
 records.push({name:'故障下 CPA 列表仍可用',status:'pass',detail:'试跑页保留两个 CPA Key，Keeper 不可用不清空列表'})
 console.log(records)
}finally{await browser.close();await fs.writeFile(path.join(out,'failure-restart-results.json'),JSON.stringify({at:new Date().toISOString(),records},null,2))}
