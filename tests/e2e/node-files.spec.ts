import {test,expect} from '@playwright/test'
import {readFileSync} from 'node:fs'
import {join,resolve} from 'node:path'
import {launchNodeTools} from './nodeToolsApp'
import {stop} from './restartApp'

async function review(page:import('@playwright/test').Page,label='Files',cancel='Cancel'){
 await page.getByRole('gridcell',{name:'worker-01',exact:true}).click()
 const toolbar=page.locator('[data-area=details] .drawer-tools')
 await expect(toolbar.getByRole('button').nth(1)).toHaveAccessibleName(label)
 await toolbar.getByRole('button',{name:label,exact:true}).click()
 const dialog=page.getByRole('dialog',{name:label+' worker-01',exact:true})
 await expect(dialog.getByRole('button',{name:cancel,exact:true})).toBeFocused()
 await expect(dialog.locator('.bg-danger, .text-danger')).toHaveCount(0)
 return dialog
}
async function openFiles(page:import('@playwright/test').Page){
 const d=await review(page);await expect(d).toContainText('root access');await expect(d).toContainText('host filesystem')
 await d.getByRole('button',{name:'Files',exact:true}).click()
 const files=page.getByRole('dialog',{name:'Files',exact:true});await expect(files.getByRole('treeitem',{name:'etc',exact:true})).toBeVisible();return files
}

test('node Files reviews root access, reuses a helper, edits host files and creates an empty file without overwrite',async({page})=>{
 const run=await launchNodeTools(page),errors:string[]=[];page.on('pageerror',e=>errors.push(e.message))
 try{
  const d=await review(page);expect(run.fixture.pods.size).toBe(0);await page.screenshot({path:join(run.root,'node-files-review.png')});await d.getByRole('button',{name:'Files',exact:true}).click()
  const files=page.getByRole('dialog',{name:'Files',exact:true});await files.getByRole('button',{name:'Expand /etc',exact:true}).click()
  await files.getByRole('treeitem',{name:'node.txt',exact:true}).dblclick();const editor=files.locator('.cm-content');await expect(editor).toContainText('node fixture text')
  await editor.click();await page.keyboard.press('Control+End');await page.keyboard.type('edited on host')
  await files.getByRole('button',{name:'Save',exact:true}).click();await expect.poll(()=>run.fixture.files.get('/etc/node.txt')).toContain('edited on host');await expect(files).toContainText('Saved to host')
  await page.screenshot({path:join(run.root,'node-files-editor.png')})
  await files.getByRole('treeitem',{name:'node.txt',exact:true}).click();await page.keyboard.press('F2')
  const rename=page.getByRole('dialog',{name:'Rename',exact:true});await rename.getByRole('textbox',{name:'File name',exact:true}).fill('renamed-node.txt');await page.screenshot({path:join(run.root,'node-files-rename.png')});await rename.getByRole('button',{name:'Rename',exact:true}).click();await expect(rename).toHaveCount(0)
  expect(run.fixture.files.has('/etc/node.txt')).toBe(false);expect(run.fixture.files.get('/etc/renamed-node.txt')).toContain('edited on host');await expect(files.locator('[data-file-document-header]')).toContainText('/etc/renamed-node.txt')

  await files.getByRole('button',{name:'New empty file',exact:true}).click()
  const create=page.getByRole('dialog',{name:'New empty file',exact:true});await expect(create.getByRole('textbox',{name:'File name',exact:true})).toBeFocused()
  await create.getByRole('textbox',{name:'File name',exact:true}).fill('renamed-node.txt');await create.getByRole('button',{name:'New empty file',exact:true}).click();await expect(create.getByRole('alert')).toContainText('already exists');expect(run.fixture.files.get('/etc/renamed-node.txt')).toContain('edited on host')
  await create.getByRole('textbox',{name:'File name',exact:true}).fill('empty ; $(literal).txt');await page.screenshot({path:join(run.root,'node-new-file.png')});await create.getByRole('button',{name:'New empty file',exact:true}).click();await expect(create).toHaveCount(0)
  await expect(files.locator('[data-file-document-header]')).toContainText('/etc/empty ; $(literal).txt');expect(run.fixture.files.get('/etc/empty ; $(literal).txt')).toBe('')
  await expect(files.getByRole('treeitem',{name:'empty ; $(literal).txt',exact:true})).toBeVisible()
  await files.getByRole('treeitem',{name:'empty ; $(literal).txt',exact:true}).click();await page.keyboard.press('Delete')
  const deletion=page.getByRole('alertdialog',{name:'Delete',exact:true});await expect(deletion.getByRole('button',{name:'Cancel',exact:true})).toBeFocused();await expect(deletion).toContainText('/etc/empty ; $(literal).txt');await page.screenshot({path:join(run.root,'node-files-delete.png')});await deletion.getByRole('button',{name:'Delete',exact:true}).click();await expect(deletion).toHaveCount(0);expect(run.fixture.files.has('/etc/empty ; $(literal).txt')).toBe(false);expect(run.fixture.files.has('/etc/renamed-node.txt')).toBe(true);await expect(files.locator('[data-file-document-header]')).toHaveCount(0)
  expect(run.fixture.pods.size).toBe(1);expect(run.fixture.calls.filter(c=>c.method==='POST'&&c.path.endsWith('/pods'))).toHaveLength(1)
  const commands=run.fixture.calls.filter(c=>c.method==='UPGRADE').map(c=>c.body!.argv as string[]);expect(commands.length).toBeGreaterThan(3);expect(commands.some(argv=>argv.some(arg=>arg.includes('ocular-atomic-rename')))).toBe(true);for(const argv of commands){expect(argv.slice(0,11)).toEqual(['nsenter','-t','1','-m','-u','-i','-n','-p','-r','-w','--']);expect(argv[11]).toBe('/bin/sh')}
  await files.getByRole('button',{name:'Close',exact:true}).click();await expect.poll(()=>run.fixture.pods.size).toBe(0);expect(errors).toEqual([])
 }finally{await run.close()}
})

test('node Files failed creation is not retried and closing a live inspector at application shutdown cleans its helper',async({page})=>{
 const run=await launchNodeTools(page)
 try{
  run.fixture.denyCreate(true);let files=await openFilesDenied()
  await expect(files.getByRole('alert')).toContainText('Forbidden');await files.getByRole('button',{name:'Refresh',exact:true}).click();await expect(files.getByRole('alert')).toContainText('Forbidden');expect(run.fixture.calls.filter(c=>c.method==='POST'&&c.path.endsWith('/pods'))).toHaveLength(1)
  await files.getByRole('button',{name:'Close',exact:true}).click();run.fixture.denyCreate(false)
  files=await openFiles(page);expect(run.fixture.pods.size).toBe(1);await stop(run.app);await expect.poll(()=>run.fixture.pods.size).toBe(0)
 }finally{await run.close()}
 async function openFilesDenied(){const d=await review(page);await d.getByRole('button',{name:'Files',exact:true}).click();return page.getByRole('dialog',{name:'Files',exact:true})}
})

test('node Files and empty-file creation retain all eight locales and fit a narrow Russian inspector',async({page})=>{
 const run=await launchNodeTools(page)
 try{
  for(const lang of ['en','ru','zh','es','de','fr','pt','ja']){
   const ui=lang==='en'?{'act.node-files':'Files','action.cancel':'Cancel','files.title':'Files','files.create':'New empty file','files.rename':'Rename','files.delete':'Delete','files.name':'File name','drawer.close':'Close'}:lang==='ru'?{'act.node-files':'Файлы','action.cancel':'Отмена','files.title':'Файлы','files.create':'Новый пустой файл','files.rename':'Переименовать','files.delete':'Удалить','files.name':'Имя файла','drawer.close':'Закрыть'}:JSON.parse(readFileSync(resolve(import.meta.dirname,`../../web/src/locales/${lang}.json`),'utf8')).ui
   await run.rpc('SetLanguage',{language:lang});await page.reload();const d=await review(page,ui['act.node-files'],ui['action.cancel']);await d.getByRole('button',{name:ui['act.node-files'],exact:true}).click()
   const files=page.getByRole('dialog',{name:ui['files.title'],exact:true});await expect(files.getByRole('treeitem',{name:'etc',exact:true})).toBeVisible()
   if(lang==='ru')await page.setViewportSize({width:375,height:900})
   await files.getByRole('button',{name:ui['files.create'],exact:true}).click();const create=page.getByRole('dialog',{name:ui['files.create'],exact:true});await expect(create.getByRole('textbox',{name:ui['files.name'],exact:true})).toBeFocused();expect(await create.evaluate(el=>el.scrollWidth<=el.clientWidth)).toBe(true);await page.screenshot({path:join(run.root,`new-file-${lang}.png`)})
   await page.keyboard.press('Escape');await expect(create).toHaveCount(0);await files.getByRole('treeitem',{name:'etc',exact:true}).click();await page.keyboard.press('F2');const rename=page.getByRole('dialog',{name:ui['files.rename'],exact:true});await expect(rename.getByRole('textbox',{name:ui['files.name'],exact:true})).toHaveValue('etc');await page.screenshot({path:join(run.root,`rename-${lang}.png`)});await page.keyboard.press('Escape');await expect(rename).toHaveCount(0);await files.getByRole('treeitem',{name:'etc',exact:true}).click();await page.keyboard.press('Delete');const deletion=page.getByRole('alertdialog',{name:ui['files.delete'],exact:true});await expect(deletion.getByRole('button',{name:ui['action.cancel'],exact:true})).toBeFocused();expect(await deletion.evaluate(el=>el.scrollWidth<=el.clientWidth)).toBe(true);await page.screenshot({path:join(run.root,`delete-${lang}.png`)});await page.keyboard.press('Escape');await expect(deletion).toHaveCount(0);await files.getByRole('button',{name:ui['drawer.close'],exact:true}).click();await expect.poll(()=>run.fixture.pods.size).toBe(0);await page.setViewportSize({width:1280,height:720})
  }
 }finally{await run.close()}
})

for(const mode of ['shell','files'] as const)test(`node ${mode} persists intent before CREATE and recovers its helper after an owned process crash`,async({page})=>{
 const run=await launchNodeTools(page)
 try{
  run.fixture.beforeCreate(()=>{const rows=run.helperRows();expect(rows).toHaveLength(1);expect(rows[0].uid).toBe('');expect(JSON.parse(rows[0].identity_json).namespaceUID).toBe('ns-kube-system')})
  if(mode==='files')await openFiles(page)
  else{
   await page.getByRole('gridcell',{name:'worker-01',exact:true}).click();await page.locator('[data-area=details]').getByRole('button',{name:'Node Shell',exact:true}).click();await page.getByRole('dialog',{name:'Node Shell worker-01',exact:true}).getByRole('button',{name:'Node Shell',exact:true}).click();await expect.poll(()=>run.fixture.execs).toBe(1)
  }
  const helper=[...run.fixture.pods.values()][0],rows=run.helperRows();expect(rows).toHaveLength(1);expect(rows[0].uid).toBe(helper.metadata.uid)
  const exited=new Promise<void>(resolve=>run.app.once('exit',()=>resolve()));run.app.kill('SIGKILL');await exited
  expect(run.fixture.pods.size).toBe(1);await run.restartAfterCrash();await expect(page.getByRole('button',{name:'Connect',exact:true})).toBeVisible();expect(run.fixture.pods.size).toBe(1)
  await run.rpc('ConnectTarget',{provider:'kubernetes',id:run.target.id});await expect.poll(()=>run.fixture.pods.size).toBe(0);await expect.poll(()=>run.helperRows().length).toBe(0)
  expect(run.fixture.calls.filter(c=>c.method==='POST'&&c.path.endsWith('/pods'))).toHaveLength(1);expect(run.fixture.calls.filter(c=>c.method==='DELETE'&&c.path.endsWith('/'+helper.metadata.name))).toHaveLength(1)
 }finally{await run.close()}
})
