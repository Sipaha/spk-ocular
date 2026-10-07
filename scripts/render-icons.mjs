// Rasterize the SVG at 4x each output size, then downsample with Catmull-Rom.
import { createRequire } from 'node:module'
import { readFileSync, mkdtempSync, mkdirSync, writeFileSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { execFileSync } from 'node:child_process'

const root = resolve(fileURLToPath(new URL('..', import.meta.url)))
const scratch = process.env.OCULAR_SCRATCH_DIR
if (!scratch || !process.env.TMPDIR) throw new Error('Set OCULAR_SCRATCH_DIR and TMPDIR to solution scratch directories')
const input = mkdtempSync(join(scratch, 'icon-render-'))
const require = createRequire(join(root, 'tests/e2e/package.json'))
const { chromium } = require('@playwright/test')
const browser = await chromium.launch()
try {
 const svg = readFileSync(join(root, 'internal/appfiles/icons/icon.svg'), 'utf8')
 mkdirSync(join(root, 'web/public'), { recursive: true })
 writeFileSync(join(root, 'web/public/icon.svg'), svg)
 for (const size of [16, 24, 32, 48, 64, 128, 256]) {
  const full = size * 4
  const page = await browser.newPage({ viewport: { width: full, height: full }, deviceScaleFactor: 1 })
  await page.setContent(`<style>html,body{margin:0;padding:0;background:transparent}svg{width:${full}px;height:${full}px}</style>${svg}`)
  await page.locator('svg').screenshot({ path: join(input, `${size}.png`), omitBackground: true })
  await page.close()
 }
} finally {
 await browser.close()
}
execFileSync('go', ['run', 'scripts/render-icons.go', input, 'internal/appfiles/icons'], { cwd: root, stdio: 'inherit' })
console.log('Rendered 16/24/32/48/64/128/256 px icons with 4x supersampling')
