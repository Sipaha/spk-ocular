import { defineConfig } from '@playwright/test'
import { createHash } from 'node:crypto'
import { existsSync, mkdirSync, mkdtempSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { noDockerEnv, writeAtomic } from './fixtures'

// Real-daemon e2e (make e2e-dind) on the isolated test daemon. Fails —
// never skips — without it: the endpoint comes from scripts/dind-verify.sh.
const host = process.env.OCULAR_DIND_HOST
if (!host || !process.env.OCULAR_DIND_VERIFY || !existsSync(process.env.OCULAR_DIND_VERIFY)) {
  throw new Error('dind e2e needs OCULAR_DIND_HOST and OCULAR_DIND_VERIFY (run: make e2e-dind)')
}
const port = Number(process.env.E2E_PORT ?? 5251)
let root = process.env.E2E_DIND_ROOT
if (!root) {
  mkdirSync(join(import.meta.dirname, '.run'), { recursive: true })
  root = mkdtempSync(join(import.meta.dirname, '.run', 'dind-'))
  const home = join(root, 'home')
  mkdirSync(home, { recursive: true })
  writeFileSync(join(root, 'README'), 'scratch dir of the dind e2e run\n')
  // One Docker context, current: the test daemon (as `docker context create` stores it).
  const dir = createHash('sha256').update('ocular-dind').digest('hex')
  writeAtomic(join(home, '.docker', 'contexts', 'meta', dir, 'meta.json'), JSON.stringify({ Name: 'ocular-dind', Metadata: { Description: 'the isolated test daemon' }, Endpoints: { docker: { Host: host, SkipTLSVerify: false } } }))
  writeAtomic(join(home, '.docker', 'config.json'), JSON.stringify({ currentContext: 'ocular-dind' }))
  process.env.E2E_DIND_ROOT = root
}
const bin = process.env.E2E_BIN ?? '../../build/bin/spk-ocular'

export default defineConfig({
  testDir: '.',
  testMatch: 'dind.spec.ts',
  workers: 1,
  timeout: 60_000,
  expect: { timeout: 20_000 },
  use: { baseURL: `http://127.0.0.1:${port}`, locale: 'en-US', screenshot: 'only-on-failure', viewport: { width: 1500, height: 850 } },
  webServer: {
    command: `${bin} --browser --port ${port}`,
    url: `http://127.0.0.1:${port}/`,
    env: {
      SPK_OCULAR_HOME: join(root, 'data'),
      HOME: join(root, 'home'),
      ...noDockerEnv,
      KUBECONFIG: join(root, 'no-kubeconfig'),
      HTTPS_PROXY: '', HTTP_PROXY: '', https_proxy: '', http_proxy: '',
      LANG: 'en_US.UTF-8', LANGUAGE: '', LC_ALL: '', LC_MESSAGES: '',
    },
    reuseExistingServer: false,
    gracefulShutdown: { signal: 'SIGTERM', timeout: 5000 },
  },
})
