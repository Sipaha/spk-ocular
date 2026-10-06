import { expect, test as base, type Locator, type Page } from '@playwright/test'
import { createHash } from 'node:crypto'
import { mkdirSync, renameSync, writeFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'

/** Shared scratch root: CI can keep it inside the checkout. */
export const scratchRoot = resolve(process.env.OCULAR_SCRATCH_DIR ?? join(import.meta.dirname, '../../../.agents/tmp'))

/**
 * The e2e suites share one app instance and its data dir; since P19 the
 * page snapshot (filter/sort/open details) persists there. Every spec
 * starts clean: the app's test API wipes the persisted snapshot (all e2e
 * configs run the app with --test-api). Specs that manage the app's
 * lifecycle themselves (the restart spec) import `test` from
 * @playwright/test instead.
 */
export const test = base.extend<{ cleanPageMemo: void }>({
  cleanPageMemo: [
    async ({ page }, use) => {
      const root = await page.request.get('/')
      const html = await root.text()
      const token = html.match(/spk-ocular-api-token" content="([^"]+)"/)?.[1]
      const res = await page.request.post('/api/_test/ui-state/reset', { headers: { Authorization: `Bearer ${token}` } })
      expect(res.ok()).toBeTruthy()
      // These suites exercise already imported connections. Onboarding has a
      // separate fresh-profile browser suite and is never bypassed in production.
      const configHeaders = { Authorization: `Bearer ${token}`, Origin: new URL(root.url()).origin }
      const status = await page.request.post('/api/Configurations', { headers: configHeaders, data: { command: 'status' } })
      expect(status.ok()).toBeTruthy()
      const configState = await status.json()
      if (!configState.initialized) {
        const imported = await page.request.post('/api/Configurations', { headers: configHeaders, data: { command: 'import', paths: configState.candidates.filter((c: {problem?: string}) => !c.problem).map((c: {path: string}) => c.path) } })
        expect(imported.ok()).toBeTruthy()
      }
      // Resource interaction suites start with their synthetic sections open.
      // Default expansion and persistence have separate, fresh-profile coverage.
      for (const key of ['group:Synthetic', 'group:Health', 'favorites']) {
        const saved = await page.request.post('/api/SetNavSection', {
          headers: { Authorization: 'Bearer ' + token, Origin: new URL(root.url()).origin }, data: { key, open: true },
        })
        expect(saved.ok()).toBeTruthy()
      }
      await use()
    },
    { auto: true },
  ],
})

/** Connect explicitly unless this process already has that target connected. */
export async function connectSelected(page: Page) {
  const connect = page.getByRole('button', { name: 'Connect', exact: true })
  const resources = page.getByRole('textbox', { name: 'Filter resources', exact: true })
  await expect(connect.or(resources).first()).toBeVisible()
  if (await connect.isVisible()) await connect.click()
}

/** Minimal kubeconfig; names are quoted (YAML 1.1: bare y/n/on/off are bools). */
export function kubeconfig(current: string, ...names: string[]): string {
  const lines = ['apiVersion: v1', 'kind: Config']
  if (current) lines.push(`current-context: "${current}"`)
  lines.push('clusters:')
  for (const n of names) lines.push(`- name: "${n}-cluster"`, '  cluster:', `    server: "https://${n}.example:6443"`)
  lines.push('users:')
  for (const n of names) lines.push(`- name: "${n}-user"`, '  user:', `    token: "SECRET-${n}"`)
  lines.push('contexts:')
  for (const n of names) {
    lines.push(`- name: "${n}"`, '  context:', `    cluster: "${n}-cluster"`, `    user: "${n}-user"`, `    namespace: "ns-${n}"`)
  }
  return lines.join('\n') + '\n'
}

/** Writes like editors and `kubectl config` do: temp file + rename. */
export function writeAtomic(path: string, body: string) {
  mkdirSync(dirname(path), { recursive: true })
  const tmp = `${path}.tmp-${process.pid}`
  writeFileSync(tmp, body, { mode: 0o600 })
  renameSync(tmp, path)
}

export interface Env {
  home: string
  dataDir: string
  one: string
  two: string
  extra: string
}

export function env(root: string): Env {
  return {
    home: join(root, 'home'),
    dataDir: join(root, 'data'),
    one: join(root, 'kc', 'one.yaml'),
    two: join(root, 'kc', 'two.yaml'),
    extra: join(root, 'home', '.kube', 'extra.yaml'),
  }
}

export const ONE = kubeconfig('prod', 'prod', 'staging')
export const TWO = kubeconfig('dev', 'dev')
export const EXTRA = kubeconfig('', 'lab')

/** The Docker CLI variables cleared for the app under test: it reads only the fixture ~/.docker. */
export const noDockerEnv = { DOCKER_HOST: '', DOCKER_CONTEXT: '', DOCKER_CONFIG: '', DOCKER_TLS_VERIFY: '', DOCKER_TLS: '', DOCKER_CERT_PATH: '' }

/**
 * Docker contexts in <home>/.docker as `docker context create` stores them:
 * "docker-edge" (tcp + TLS, its key holds SECRET), "sock" (a unix socket that
 * does not exist), current "sock". Nothing here is ever connected to.
 */
export function dockerContexts(home: string) {
  const cfg = join(home, '.docker')
  const add = (name: string, host: string, tls?: Record<string, string>) => {
    const dir = createHash('sha256').update(name).digest('hex')
    writeAtomic(join(cfg, 'contexts', 'meta', dir, 'meta.json'), JSON.stringify({ Name: name, Metadata: { Description: `fixture ${name}` }, Endpoints: { docker: { Host: host, SkipTLSVerify: false } } }))
    for (const [f, body] of Object.entries(tls ?? {})) writeAtomic(join(cfg, 'contexts', 'tls', dir, 'docker', f), body)
  }
  add('docker-edge', 'tcp://edge.example:2376', {
    'ca.pem': '-----BEGIN CERTIFICATE-----\nSECRET-CA\n-----END CERTIFICATE-----\n',
    'cert.pem': '-----BEGIN CERTIFICATE-----\nSECRET-CERT\n-----END CERTIFICATE-----\n',
    'key.pem': '-----BEGIN PRIVATE KEY-----\nSECRET-KEY\n-----END PRIVATE KEY-----\n',
  })
  add('sock', `unix://${join(home, 'no-such', 'docker.sock')}`)
  writeAtomic(join(cfg, 'config.json'), JSON.stringify({ auths: { 'registry.example': { auth: 'U0VDUkVULWF1dGg=' } }, currentContext: 'sock' }))
}

/** Chooses a scope in the scope picker (label "Namespace", "Project"…):
 * opens it, searches the name, takes the first found with Enter. */
export async function pickScope(page: Page, label: string, name: string) {
  await page.getByRole('button', { name: label, exact: true }).click()
  const list = page.getByRole('listbox', { name: label })
  await expect(list.getByRole('option', { name, exact: true })).toBeAttached()
  await page.getByRole('combobox', { name: new RegExp(label, 'i') }).fill(name)
  await page.keyboard.press('Enter')
  await expect(page.getByRole('button', { name: label, exact: true })).toHaveText(name)
}

/** The app's select buttons (not native selects) in a part of the page. */
export const selects = (within: Locator | Page) => within.locator('button[aria-haspopup="listbox"]')

/** Chooses an option of an app select: opens it, clicks the option. */
export async function pickOption(button: Locator, name: string | RegExp) {
  await button.click()
  const id = await button.page().locator('[role="listbox"]').last().getAttribute('id')
  await button.page().locator(`[id="${id}"]`).getByRole('option', { name, exact: typeof name === 'string' }).click()
  await expect(button).toHaveText(name)
}
