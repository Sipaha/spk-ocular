import { defineConfig } from '@playwright/test'
import { noDockerEnv } from './fixtures'
import { existsSync, mkdirSync, mkdtempSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

// Real-cluster e2e (make e2e-kind). Fails — never skips — without the
// cluster: the kubeconfigs come from make kind-up / scripts/kind-rbac.sh.
const kc = process.env.OCULAR_KIND_KUBECONFIG
const rbac = process.env.OCULAR_KIND_RBAC_DIR
if (!kc || !existsSync(kc) || !rbac || !existsSync(join(rbac, 'viewer.kubeconfig'))) {
  throw new Error('kind e2e needs OCULAR_KIND_KUBECONFIG and OCULAR_KIND_RBAC_DIR (run: make e2e-kind)')
}
const port = Number(process.env.E2E_PORT ?? 5241)
let root = process.env.E2E_KIND_ROOT
if (!root) {
  mkdirSync(join(import.meta.dirname, '.run'), { recursive: true })
  root = mkdtempSync(join(import.meta.dirname, '.run', 'kind-'))
  mkdirSync(join(root, 'home'), { recursive: true })
  writeFileSync(join(root, 'README'), 'scratch dir of the kind e2e run\n')
  process.env.E2E_KIND_ROOT = root
}
const bin = process.env.E2E_BIN ?? '../../build/bin/spk-ocular'

export default defineConfig({
  testDir: '.',
  testMatch: 'kind.spec.ts',
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
      KUBECONFIG: `${kc}:${join(rbac, 'viewer.kubeconfig')}`,
      LANG: 'en_US.UTF-8', LANGUAGE: '', LC_ALL: '', LC_MESSAGES: '',
    },
    reuseExistingServer: false,
    // SIGTERM, not the default SIGKILL: the app hangs up its terminals (no
    // shells left in the cluster) and frees its tunnels' ports.
    gracefulShutdown: { signal: 'SIGTERM', timeout: 5000 },
  },
})
