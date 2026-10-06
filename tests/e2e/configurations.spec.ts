import { expect, test, type Page } from '@playwright/test'
import { spawn, type ChildProcess } from 'node:child_process'
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:net'
import { createServer as createHttpServer } from 'node:http'
import { join } from 'node:path'
import { scratchRoot as scratch, ONE, EXTRA, kubeconfig, env, noDockerEnv, writeAtomic } from './fixtures'

// This spec owns the app lifecycle and restarts it on the same data dir.
// It deliberately bypasses fixtures' reset of persisted page snapshots.
const bin = process.env.E2E_BIN ?? '../../build/bin/spk-ocular'

const freePort = (): Promise<number> =>
  new Promise((resolve, reject) => {
    const s = createServer()
    s.once('error', reject)
    s.listen(0, '127.0.0.1', () => {
      const p = (s.address() as { port: number }).port
      s.close((err) => err ? reject(err) : resolve(p))
    })
  })

const stop = async (p: ChildProcess) => {
  if (p.exitCode !== null || p.signalCode !== null || !p.pid) return
  await new Promise<void>((resolve) => {
    const timer = setTimeout(() => p.kill('SIGKILL'), 5000)
    p.once('exit', () => { clearTimeout(timer); resolve() })
    p.kill('SIGTERM')
  })
}

const start = async (port: number, e: ReturnType<typeof env>, language = 'en'): Promise<ChildProcess> => {
  const p = spawn(bin, ['--browser', '--port', String(port), '--test-api', '--test-synthetic'], {
    env: {
      ...process.env,
      SPK_OCULAR_HOME: e.dataDir,
      HOME: e.home,
      ...noDockerEnv,
      SPK_OCULAR_TEST_SYNTH_SECOND: '1',
      KUBECONFIG: e.one,
      LANG: 'en_US.UTF-8', LANGUAGE: language, LC_ALL: '', LC_MESSAGES: '',
      HTTPS_PROXY: '', HTTP_PROXY: '', https_proxy: '', http_proxy: '', ALL_PROXY: '', all_proxy: '',
    },
    stdio: ['ignore', 'ignore', 'pipe'],
  })
  let stderr = ''
  let spawnError: Error | undefined
  p.stderr?.on('data', (chunk: Buffer) => { stderr = (stderr + chunk.toString()).slice(-8000) })
  p.once('error', (error) => { spawnError = error })
  try {
    await expect.poll(async () => {
      if (spawnError) throw spawnError
      if (p.exitCode !== null || p.signalCode !== null) throw new Error(`app exited: ${stderr}`)
      try {
        return (await fetch(`http://127.0.0.1:${port}/`, { signal: AbortSignal.timeout(1000) })).ok
      } catch { return false }
    }, { timeout: 20000, message: 'browser app starts' }).toBe(true)
    return p
  } catch (error) {
    await stop(p)
    throw new Error(`app did not start: ${stderr}`, { cause: error })
  }
}


async function menuOn(page: Page, name: RegExp, item: string) {
 await page.getByRole('region',{name:'Kubernetes',exact:true}).getByRole('option',{name}).click({button:'right'})
 await page.getByRole('menuitem',{name:item,exact:true}).click()
}
async function draft(page: Page, text: string) {
 const editor=page.getByRole('dialog').locator('.cm-content')
 await editor.click();await page.keyboard.press('Control+Home');await page.keyboard.press('Control+Shift+End')
 await page.keyboard.insertText(text)
}

test('connection menus manage linked and encrypted YAML without a separate configuration list', async ({browser})=>{
 mkdirSync(scratch,{recursive:true});const root=mkdtempSync(join(scratch,'configs-targets-'));const e=env(root)
 writeAtomic(e.one,ONE);writeAtomic(e.extra,EXTRA)
 const port=await freePort(),url=`http://127.0.0.1:${port}/`
 const page=await browser.newPage({viewport:{width:1280,height:900}});let app:ChildProcess|undefined
 try {
  app=await start(port,e);await page.goto(url)
  const kube=page.getByRole('region',{name:'Kubernetes',exact:true})
  const onboarding=page.getByRole('dialog',{name:'Choose configurations to add'})
  await expect(onboarding.getByRole('checkbox').first()).not.toBeChecked()
  await onboarding.getByRole('checkbox',{name:/one.yaml/}).check()
  await onboarding.getByRole('button',{name:'Add selected',exact:true}).click()
  await expect(kube.getByRole('option',{name:/^prod\b/})).toBeVisible()
  await expect(kube.getByRole('option',{name:/^lab\b/})).toHaveCount(0)
  await kube.getByRole('option',{name:/^prod\b/}).click()
  await kube.getByRole('option',{name:/^prod\b/}).click({button:'right'})
  await expect(page.getByRole('menuitem',{name:'Open in file manager',exact:true})).toBeVisible()
  await page.screenshot({path:join(scratch,'target-config-menu.png')})
  const response=page.waitForResponse(r=>r.url().endsWith('/api/Configurations')&&r.request().postDataJSON()?.command==='inspect')
  await page.getByRole('menuitem',{name:'Open in inspector',exact:true}).click()
  expect((await response).headers()['cache-control']).toContain('no-store')
  await expect(page.getByRole('dialog').locator('.cm-content')).toContainText('SECRET-prod')
  await page.screenshot({path:join(scratch,'target-config-inspect.png')})
  await page.getByRole('button',{name:'Edit kubeconfig YAML',exact:true}).click()
  await draft(page,ONE+'\n# reviewed UI change\n')
  await page.getByRole('button',{name:'Save configuration',exact:true}).click()
  expect(readFileSync(e.one,'utf8')).toBe(ONE)
  await expect(page.getByRole('dialog')).toContainText(e.one)
  await page.screenshot({path:join(scratch,'target-config-save-review.png')})
  await page.getByRole('button',{name:'Confirm save',exact:true}).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  expect(readFileSync(e.one,'utf8')).toContain('# reviewed UI change')
  await menuOn(page,/^prod\b/,'Edit kubeconfig YAML')
  await draft(page,ONE+'\n# unsaved\n')
  await page.keyboard.press('Escape')
  await page.getByRole('button',{name:'Keep editing',exact:true}).click()
  await expect(page.getByRole('dialog').locator('.cm-content')).toContainText('# unsaved')
  await page.keyboard.press('Escape');await page.getByRole('button',{name:'Discard',exact:true}).click()
  await page.getByRole('button',{name:'Add kubeconfig',exact:true}).click()
  await expect(page.getByRole('menuitem')).toHaveCount(2)
  await page.screenshot({path:join(scratch,'target-config-add-menu.png')})
  await page.getByRole('menuitem',{name:'New configuration',exact:true}).click()
  await page.getByLabel('Master password',{exact:true}).fill('x')
  await page.getByLabel('Repeat master password',{exact:true}).fill('x')
  await page.getByRole('button',{name:'Create master password',exact:true}).click()
  await page.getByRole('textbox',{name:'Configuration name',exact:true}).fill('Private demo')
  const own=kubeconfig('','private-demo').replace('SECRET-private-demo','PRIVATE-DO-NOT-PERSIST').replace('https://private-demo.example:6443','http://127.0.0.1:1')
  await page.getByRole('textbox',{name:'Kubeconfig YAML',exact:true}).fill(own)
  await page.getByRole('button',{name:'Save configuration',exact:true}).click()
  await expect(kube.getByRole('option',{name:/^Private demo\b/})).toBeVisible()
  await menuOn(page,/^Private demo\b/,'Edit kubeconfig YAML')
  await draft(page,own+'\n# encrypted edit\n')
  await page.getByRole('button',{name:'Save configuration',exact:true}).click()
  await page.getByRole('button',{name:'Confirm save',exact:true}).click()
  expect(readFileSync(join(e.dataDir,'configurations/kubeconfigs.json'),'utf8')).not.toContain('PRIVATE-DO-NOT-PERSIST')
  await stop(app);app=await start(port,e);await page.goto(url)
  await expect(kube.getByRole('option',{name:/^Private demo\b/})).toBeVisible()
  await expect(kube.getByRole('option',{name:/^Private demo\b/}).locator('[data-encrypted-lock]')).toBeVisible()
  await expect(page.getByRole('button',{name:/Stored configurations are locked/})).toHaveCount(0)
  await kube.getByRole('option',{name:/^Private demo\b/}).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await page.screenshot({path:join(scratch,'connections-locked.png')})
  await page.getByRole('button',{name:'Connect',exact:true}).click()
  await page.getByLabel('Master password',{exact:true}).fill('wrong')
  await page.getByRole('button',{name:'Unlock',exact:true}).click()
  await expect(page.getByRole('alert')).toContainText('incorrect master password')
  await page.getByLabel('Master password',{exact:true}).fill('x')
  await page.getByRole('button',{name:'Unlock',exact:true}).click()
  await expect(kube.locator('[data-encrypted-lock]')).toHaveCount(0)
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await page.screenshot({path:join(scratch,'connections-unlocked.png')})
  await kube.getByRole('option',{name:/^Private demo\b/}).click()
  await page.getByRole('listbox',{name:'targets'}).focus();await page.keyboard.press('Shift+F10')
  await expect(page.getByRole('menuitem',{name:'Open in file manager',exact:true})).toHaveCount(0)
  await page.getByRole('menuitem',{name:'Open in inspector',exact:true}).click()
  await expect(page.getByRole('dialog').locator('.cm-content')).toContainText('# encrypted edit')
  await page.getByRole('button',{name:'Close',exact:true}).click()
  await expect(page.locator('.cm-content')).toHaveCount(0)
  await menuOn(page,/^Private demo\b/,'Rename')
  await expect(page.getByRole('textbox',{name:'Connection name',exact:true})).toBeFocused()
  await expect(page.getByRole('textbox',{name:'Connection name',exact:true})).toHaveValue('Private demo')
  await page.getByRole('textbox',{name:'Connection name',exact:true}).fill('Renamed demo')
  await page.getByRole('button',{name:'Rename',exact:true}).click()
  await expect(kube.getByRole('option',{name:/^Renamed demo\b/})).toBeVisible()
  await menuOn(page,/^Renamed demo\b/,'Remove')
  await expect(page.getByRole('dialog').getByRole('button',{name:'Cancel',exact:true})).toBeFocused()
  await page.getByRole('dialog').getByRole('button',{name:'Cancel',exact:true}).click()
  await expect(kube.getByRole('option',{name:/^Renamed demo\b/})).toBeVisible()
  await menuOn(page,/^Renamed demo\b/,'Remove')
  await page.getByRole('button',{name:'Remove configuration',exact:true}).click()
  await expect(kube.getByRole('option',{name:/^Renamed demo\b/})).toHaveCount(0)
  await menuOn(page,/^prod\b/,'Rename')
  const rename=page.getByRole('textbox',{name:'Connection name',exact:true})
  await expect(rename).toBeFocused();await expect(rename).toHaveValue('prod')
  await page.screenshot({path:join(scratch,'connection-rename-focused.png')})
  await rename.fill('Production')
  await page.getByRole('button',{name:'Rename',exact:true}).click()
  await expect(kube.getByRole('option',{name:/^Production\b/})).toBeVisible()
  await expect(kube.getByRole('option',{name:/^staging\b/})).toBeVisible()
  expect(readFileSync(e.one,'utf8')).toContain('name: "prod"')
  await menuOn(page,/^Production\b/,'Remove')
  await expect(page.getByRole('dialog')).toContainText('original kubeconfig file will not be changed')
  await page.getByRole('button',{name:'Remove configuration',exact:true}).click()
  await expect(kube.getByRole('option')).toHaveCount(0)
  expect(readFileSync(e.one,'utf8')).toContain('# reviewed UI change')
 } finally {await page.close();if(app)await stop(app)}
})

test('Russian add, password and YAML workflows fit a narrow window',async({browser})=>{
 mkdirSync(scratch,{recursive:true});const root=mkdtempSync(join(scratch,'configs-targets-ru-'));const e=env(root)
 writeAtomic(e.one,ONE);const port=await freePort();const page=await browser.newPage({viewport:{width:600,height:800},locale:'ru-RU'});let app:ChildProcess|undefined
 try {
  app=await start(port,e,'ru');await page.goto(`http://127.0.0.1:${port}/`)
  await page.getByRole('button',{name:'Новый конфиг',exact:true}).click()
  await page.getByLabel('Мастер-пароль',{exact:true}).fill('я')
  await page.getByLabel('Повторите мастер-пароль',{exact:true}).fill('я')
  await page.screenshot({path:join(scratch,'target-config-master-narrow-ru.png')})
  await page.getByRole('button',{name:'Создать мастер-пароль',exact:true}).click()
  await page.getByRole('textbox',{name:'Название конфигурации'}).fill('Демо')
  await page.getByRole('textbox',{name:'Kubeconfig YAML'}).fill(kubeconfig('','demo'))
  await page.getByRole('button',{name:'Сохранить конфигурацию',exact:true}).click()
  await page.getByRole('option',{name:/^Демо/}).click({button:'right'})
  await page.getByRole('menuitem',{name:'Редактировать kubeconfig YAML',exact:true}).click()
  await expect(page.getByRole('dialog').locator('.cm-content')).toContainText('apiVersion: v1')
  expect(await page.getByRole('dialog').evaluate(el=>el.scrollWidth<=el.clientWidth)).toBe(true)
  await page.screenshot({path:join(scratch,'target-config-editor-narrow-ru.png')})
  await page.keyboard.press('Escape');await expect(page.getByRole('dialog')).toHaveCount(0)
  await page.getByRole('button',{name:'Добавить kubeconfig',exact:true}).click()
  await expect(page.getByRole('menuitem')).toHaveCount(3)
  await page.getByRole('menuitem',{name:'Из системных файлов',exact:true}).click()
  await expect(page.getByRole('dialog',{name:'Выберите конфигурации для добавления'})).toBeVisible()
  expect(await page.getByRole('dialog').evaluate(el=>el.scrollWidth<=el.clientWidth)).toBe(true)
 }finally{await page.close();if(app)await stop(app)}
})


test('Cancel stops a real Kubernetes request and clears its yellow connection indicator',async({browser})=>{
 mkdirSync(scratch,{recursive:true});const root=mkdtempSync(join(scratch,'configs-cancel-'));const e=env(root)
 let pending=0
 const fake=createHttpServer((req,res)=>{
  if(req.url?.startsWith('/api/v1/namespaces')){
   pending++;res.on('close',()=>pending--)
   return
  }
  res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify(req.url==='/api'?{kind:'APIVersions',versions:['v1']}:{kind:'APIGroupList',groups:[]}))
 })
 await new Promise<void>(resolve=>fake.listen(0,'127.0.0.1',resolve))
 const serverPort=(fake.address() as {port:number}).port
 writeAtomic(e.one,kubeconfig('','pending').replace('https://pending.example:6443',`http://127.0.0.1:${serverPort}`))
 const port=await freePort();const page=await browser.newPage({viewport:{width:1280,height:900}});let app:ChildProcess|undefined
 try {
  app=await start(port,e);await page.goto(`http://127.0.0.1:${port}/`)
  await page.getByRole('checkbox',{name:/one.yaml/}).check()
  await page.getByRole('button',{name:'Add selected',exact:true}).click()
  const target=page.getByRole('region',{name:'Kubernetes',exact:true}).getByRole('option',{name:/^pending\b/})
  await target.click();await page.getByRole('button',{name:'Connect',exact:true}).click()
  await expect.poll(()=>pending).toBeGreaterThan(0)
  await expect(target.locator('[data-connection-state]')).toHaveAttribute('data-connection-state','connecting')
  await page.screenshot({path:join(scratch,'target-connection-pending.png')})
  await page.getByRole('button',{name:'Cancel',exact:true}).click()
  await expect(page.getByRole('button',{name:'Connect',exact:true})).toBeVisible({timeout:3000})
  await expect.poll(()=>pending,{timeout:3000}).toBe(0)
  await expect(target.locator('[data-connection-state]')).toHaveAttribute('data-connection-state','disconnected')
  await expect(page.getByText('Connection cancelled',{exact:true})).toBeVisible()
  await page.screenshot({path:join(scratch,'target-connection-cancelled.png')})
 }finally{
  await page.close();if(app)await stop(app)
  fake.closeAllConnections();await new Promise<void>(resolve=>fake.close(()=>resolve()))
 }
})

test('locked rows, Connect-only global unlock, Cancel and reviewed master reset',async({browser})=>{
 mkdirSync(scratch,{recursive:true});const root=mkdtempSync(join(scratch,'configs-locks-'));const e=env(root)
 writeAtomic(e.one,ONE)
 const port=await freePort(),url=`http://127.0.0.1:${port}/`
 const page=await browser.newPage({viewport:{width:1280,height:900}});let app:ChildProcess|undefined
 try{
  app=await start(port,e);await page.goto(url)
  await page.getByRole('dialog').getByRole('checkbox').first().check()
  await page.getByRole('button',{name:'Add selected',exact:true}).click()
  for(const [index,name] of ['Private A','Private B'].entries()){
   await page.getByRole('button',{name:'Add kubeconfig',exact:true}).click()
   await page.getByRole('menuitem',{name:'New configuration',exact:true}).click()
   if(index===0){
    await page.getByLabel('Master password',{exact:true}).fill('x')
    await page.getByLabel('Repeat master password',{exact:true}).fill('x')
    await page.getByRole('button',{name:'Create master password',exact:true}).click()
   }
   await page.getByRole('textbox',{name:'Configuration name',exact:true}).fill(name)
   await page.getByRole('textbox',{name:'Kubeconfig YAML',exact:true}).fill(kubeconfig('',index===0?'alpha':'beta').replace(/https:\/\/[^"\s]+/g,'http://127.0.0.1:1'))
   await page.getByRole('button',{name:'Save configuration',exact:true}).click()
   await expect(page.getByRole('dialog')).toHaveCount(0)
  }
  writeFileSync(join(scratch,'native-locked-registry.json'),readFileSync(join(e.dataDir,'configurations/kubeconfigs.json')))
  await stop(app);app=await start(port,e);await page.goto(url)
  const kube=page.getByRole('region',{name:'Kubernetes',exact:true})
  await expect(kube.locator('[data-encrypted-lock]')).toHaveCount(2)
  await kube.getByRole('option',{name:/^Private A\b/}).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await page.getByRole('button',{name:'Connect',exact:true}).click()
  await page.getByRole('dialog').getByRole('button',{name:'Cancel',exact:true}).click()
  await expect(kube.locator('[data-encrypted-lock]')).toHaveCount(2)
  await expect(page.getByRole('button',{name:'Connect',exact:true})).toBeVisible()
  await page.getByRole('button',{name:'Connect',exact:true}).click()
  await page.getByRole('button',{name:'Reset master password',exact:true}).click()
  await expect(page.getByRole('dialog')).toContainText('permanently deleted')
  await expect(page.getByRole('dialog')).toContainText('Private A')
  await expect(page.getByRole('dialog')).toContainText('Private B')
  await page.getByRole('dialog').getByRole('button',{name:'Cancel',exact:true}).click()
  await expect(kube.locator('[data-encrypted-lock]')).toHaveCount(2)
  await page.getByRole('button',{name:'Connect',exact:true}).click()
  await page.getByLabel('Master password',{exact:true}).fill('x')
  await page.getByRole('button',{name:'Unlock',exact:true}).click()
  await expect(kube.locator('[data-encrypted-lock]')).toHaveCount(0)
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(kube.getByRole('option',{name:/^Private B\b/})).toBeVisible()
  await page.getByRole('button',{name:'Add kubeconfig',exact:true}).click()
  await page.getByRole('menuitem',{name:'Reset master password',exact:true}).click()
  await expect(page.getByRole('dialog',{name:'Reset master password',exact:true})).toBeVisible()
  await page.screenshot({path:join(scratch,'master-reset-review.png')})
  await page.getByRole('button',{name:'Delete encrypted configurations and reset',exact:true}).click()
  await expect(kube.getByRole('option',{name:/^Private [AB]\b/})).toHaveCount(0)
  await expect(kube.getByRole('option',{name:/^prod\b/})).toBeVisible()
  await stop(app);app=await start(port,e);await page.goto(url)
  await expect(kube.getByRole('option',{name:/^Private [AB]\b/})).toHaveCount(0)
  await page.getByRole('button',{name:'Add kubeconfig',exact:true}).click()
  await page.getByRole('menuitem',{name:'New configuration',exact:true}).click()
  await expect(page.getByRole('button',{name:'Create master password',exact:true})).toBeVisible()
 }finally{await page.close();if(app)await stop(app)}
})
