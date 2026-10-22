import {test, expect, type Browser} from '@playwright/test'
import {mkdirSync, mkdtempSync, writeFileSync, readFileSync, existsSync} from 'node:fs'
import {join} from 'node:path'
import {env, scratchRoot, openAppMenu} from './fixtures'
import {start, stop, freePort} from './restartApp'
import {fixture} from '../../web/src/krr/fixture'
import {languages, languageNames} from '../../web/src/languages'
async function launch(browser: Browser) {
  mkdirSync(scratchRoot, {recursive: true}); const root = mkdtempSync(join(scratchRoot, 'krr-')), port = await freePort(), profile = env(root), app = await start(port, profile)
  const page = await browser.newPage({viewport: {width: 1440, height: 1000}}), requests: {path: string; command?: string; uploadBytes?: number}[] = [], responses: string[] = []
  page.on('request', r => {const path=new URL(r.url()).pathname;if(path==='/api/KRR'){const body=r.postDataJSON();requests.push({path,command:body.command,uploadBytes:body.data?Buffer.from(body.data,'base64').length:0})}else requests.push({path})})
  page.on('response',r=>{if(r.url().endsWith('/api/KRR'))void r.text().then(t=>responses.push(t)).catch(()=>{})})
  await page.goto(`http://127.0.0.1:${port}`)
  return {page, requests, responses, close: async () => { await page.close(); await stop(app);for(const name of ['ocular.db','ocular.db-wal']){const path=join(profile.dataDir,name);if(existsSync(path)){const bytes=readFileSync(path);for(const literal of ['context-a','PRIVATE-URL','PRIVATE-KEY','PRIVATE-ANNOTATION'])expect(bytes.includes(Buffer.from(literal))).toBe(false)}} }}
}
const data = () => ({name: 'private-krr-file.json', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(fixture()))})
test('offline KRR import is explicit, literal, unbound, bounded and retained across reopen/reload', async ({browser}) => {
  const f = await launch(browser), {page, requests} = f
  try {
    await openAppMenu(page); await page.getByRole('button', {name: 'KRR report', exact: true}).click()
    const dialog = page.getByRole('dialog', {name: 'KRR report', exact: true}), input = dialog.getByLabel('Choose JSON report')
    await expect(input).toBeDisabled(); await dialog.getByRole('checkbox').check()
    // Genuine keyboard activation of the browser file chooser; no backend import API.
    await dialog.getByRole('button',{name:'Choose JSON report'}).focus(); const chooser = page.waitForEvent('filechooser'); const activate=page.keyboard.press('Enter'); await (await chooser).setFiles(data()); await activate
    await expect(dialog).toContainText('Rows: 1 · Reported errors: 1'); await expect(dialog).toContainText('0.125'); await expect(dialog).toContainText('Unavailable'); await expect(dialog).toContainText('Unset'); await expect(dialog).toContainText('operator-declared version'); await expect(dialog).toContainText('Actual sample count'); await expect(dialog).not.toContainText('PRIVATE-'); await expect(dialog.locator('a')).toHaveCount(0)
    const literal = fixture(); literal.scans[0].object.name = '<script>not run</script> https://example.invalid/not-opened'
    await input.setInputFiles({...data(), buffer: Buffer.from(JSON.stringify(literal))}); await expect(dialog).toContainText('<script>not run</script>'); await expect(dialog.locator('script,a')).toHaveCount(0)
    await input.setInputFiles({...data(), buffer: Buffer.from('{"scans":[]4096}')}); await expect(dialog.getByRole('alert')).toBeVisible(); await expect(dialog).not.toContainText('context-a')
    await input.setInputFiles({...data(), buffer: Buffer.alloc(1024 * 1024 + 1, 32)}); await expect(dialog.getByRole('alert')).toBeVisible()
    const empty = fixture(); empty.scans = []; await input.setInputFiles({...data(), buffer: Buffer.from(JSON.stringify(empty))}); await expect(dialog).toContainText('No rows reported')
    await dialog.getByRole('region',{name:'Imported evidence'}).getByRole('button', {name: 'Discard report'}).click(); await expect(dialog).not.toContainText('No rows reported')
    await input.setInputFiles(data()); await expect(dialog).toContainText('context-a'); await page.keyboard.press('Escape');await expect(dialog).not.toBeVisible()
    await openAppMenu(page); await page.getByRole('button', {name: 'KRR report', exact: true}).click(); await expect(dialog.getByRole('checkbox')).not.toBeChecked(); await expect(dialog).not.toContainText('context-a'); await dialog.getByRole('button',{name:'Open report'}).first().click(); await expect(dialog).toContainText('context-a')
    await page.reload(); await openAppMenu(page); await page.getByRole('button', {name: 'KRR report', exact: true}).click(); await expect(dialog).not.toContainText('context-a'); await dialog.getByRole('button',{name:'Open report'}).first().click(); await expect(dialog).toContainText('context-a')
    expect(f.responses.join('')).not.toMatch(/PRIVATE-/)
    expect(JSON.stringify(requests)).not.toMatch(/PRIVATE-|context-a|private-krr-file|not-opened|prometheus_url/)
    expect(requests.filter(r=>r.uploadBytes&&r.path!=='/api/KRR')).toHaveLength(0)
  } finally { writeFileSync(test.info().outputPath('krr-requests.json'), JSON.stringify({requests, rawFileTransferredOnlyToLocalBackend: true}, null, 2)); await f.close() }
})
test('offline KRR report keeps provenance and quantities readable in eight locales and two widths', async ({browser}) => {
  const f = await launch(browser), {page} = f
  try {
    for (const code of languages) {
      await openAppMenu(page); await page.locator('.language-menu button').click(); await page.getByRole('option', {name: languageNames[code], exact: true}).click(); await expect(page.locator('html')).toHaveAttribute('lang', code)
      await openAppMenu(page); const menu = page.locator('.app-utilities'); await menu.locator('button').filter({hasText: /KRR/}).click()
      const dialog = page.getByRole('dialog').last(); await dialog.getByRole('checkbox').check(); await dialog.locator('input[type=file]').setInputFiles(data()); await expect(dialog).toContainText('context-a')
      for (const width of [1440, 860]) {
        await page.setViewportSize({width, height: 1000}); await dialog.locator(':scope > div').evaluate(e=>{e.scrollTop=0});await dialog.getByRole('heading',{level:2}).scrollIntoViewIfNeeded(); expect(await dialog.evaluate(e => e.scrollWidth <= e.clientWidth)).toBe(true); expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
        await page.screenshot({path: join(scratchRoot, `krr-${code}-${width}.png`)})
        await dialog.locator('article').scrollIntoViewIfNeeded(); await page.screenshot({path: join(scratchRoot, `krr-${code}-${width}-provenance.png`)})
      }
      await page.keyboard.press('Escape'); await page.setViewportSize({width: 1440, height: 1000})
    }
    expect(JSON.stringify(f.requests)).not.toMatch(/PRIVATE-|context-a|private-krr-file|prometheus_url/);expect(f.responses.join('')).not.toContain('PRIVATE-')
  } finally { writeFileSync(test.info().outputPath('krr-requests.json'), JSON.stringify({requests: f.requests, rawFileTransferredOnlyToLocalBackend: true}, null, 2)); await f.close() }
})
