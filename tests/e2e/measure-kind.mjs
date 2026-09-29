// Memory/latency measurement against the kind load fixture (scripts/kind-load.sh).
// Usage: node measure-kind.mjs <spk-ocular binary> <kind kubeconfig> <viewer kubeconfig> <scratch dir> [port]
// Starts browser mode with --test-api, drives it with Playwright and prints
// timings plus backend stats and Private_Dirty of the Ocular process.
import { chromium } from '@playwright/test'
import { spawn, execFileSync } from 'node:child_process'
import { mkdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

const [bin, kc, viewer, dir, portArg] = process.argv.slice(2)
const port = Number(portArg ?? 5251)
mkdirSync(join(dir, 'home'), { recursive: true })
const srv = spawn(bin, ['--browser', '--port', String(port), '--test-api'], {
  env: { ...process.env, SPK_OCULAR_HOME: join(dir, 'data'), HOME: join(dir, 'home'), KUBECONFIG: `${kc}:${viewer}`, LANG: 'en_US.UTF-8', LANGUAGE: '' },
  stdio: 'ignore',
})
const base = `http://127.0.0.1:${port}`
const privateMB = () => {
  const out = execFileSync('bash', [join(import.meta.dirname, '../../scripts/pss.sh'), String(srv.pid)], { encoding: 'utf8' })
  return out.match(/TOTAL PRIVATE: ([\d.]+) MB/)?.[1]
}
const rssMB = () => (Number(readFileSync(`/proc/${srv.pid}/status`, 'utf8').match(/VmRSS:\s+(\d+)/)[1]) / 1024).toFixed(1)
let token
const stats = async () => (await fetch(`${base}/api/_test/stats?token=${token}`)).json()
const gc = () => fetch(`${base}/api/_test/gc`, { method: 'POST', headers: { Authorization: `Bearer ${token}`, Origin: base } })
const report = async (label) => {
  const s = await stats()
  console.log(`${label.padEnd(34)} private=${privateMB()}MB rss=${rssMB()}MB heap=${(s.heap_inuse / 1048576).toFixed(1)}MB views=${s.views} caches=${s.caches_active ?? 0}+${s.caches_idle ?? 0}idle sessions=${s.sessions} goroutines=${s.goroutines}`)
}

for (let i = 0; i < 50; i++) {
  try { if ((await fetch(base)).ok) break } catch { /* not yet */ }
  await new Promise((r) => setTimeout(r, 100))
}
const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1500, height: 850 } })
try {
  await page.goto(base)
  token = await page.locator('meta[name="spk-ocular-api-token"]').getAttribute('content')
  await report('idle')
  const count = page.getByLabel('count')
  const openKind = async (kind, ns) => {
    await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: kind, exact: true }).click()
    await page.getByRole('combobox', { name: 'Namespace' }).selectOption(ns)
  }
  const timed = async (label, fn, min) => {
    const t0 = performance.now()
    await fn()
    await page.waitForFunction((m) => Number(document.querySelector('[aria-label=count]')?.textContent) >= m, min, { timeout: 120000 })
    console.log(`${label}: ${Math.round(performance.now() - t0)} ms to ${await count.textContent()} rows`)
  }
  await page.getByRole('option', { name: /^kind-ocular-dev/ }).click()
  await page.getByRole('navigation', { name: 'resources' }).waitFor()
  await timed('pods, all namespaces (cold)', () => openKind('Pods', ''), 3000)
  await report('pods all-ns open')
  await timed('configmaps, all namespaces (cold)', () => openKind('ConfigMaps', ''), 5000)
  await report('configmaps all-ns open')
  await timed('pods, all namespaces (warm)', () => openKind('Pods', ''), 3000)
  const combos = [['Pods', 'ocular-load'], ['Pods', 'ocular-demo'], ['Pods', 'kube-system'], ['Deployments', ''], ['Deployments', 'ocular-demo'],
    ['Services', ''], ['Events', ''], ['Events', 'ocular-load'], ['Secrets', ''], ['ConfigMaps', 'ocular-load'], ['StatefulSets', ''], ['DaemonSets', ''], ['Ingresses', '']]
  for (const [k, ns] of combos) {
    await openKind(k, ns)
    await page.waitForFunction(() => !document.body.textContent.includes('Loading…'), null, { timeout: 60000 })
  }
  await report('after 13 quick navigations')
  // Switch to the other context: the first session and its caches go away.
  await page.getByRole('option', { name: /^ocular-viewer/ }).click()
  await page.getByRole('grid', { name: 'resources' }).waitFor()
  await new Promise((r) => setTimeout(r, 1500))
  await gc()
  await report('after switching context + GC')
  await new Promise((r) => setTimeout(r, 65000))
  await gc()
  await report('after 65 s idle + GC')
} finally {
  await browser.close()
  srv.kill('SIGTERM')
}
