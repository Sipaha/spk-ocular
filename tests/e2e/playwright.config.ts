import { defineConfig } from '@playwright/test'
import { mkdirSync, mkdtempSync } from 'node:fs'
import { join } from 'node:path'
import { EXTRA, ONE, TWO, env, writeAtomic, noDockerEnv, dockerContexts } from './fixtures'

const port = Number(process.env.E2E_PORT ?? 5191)
// The config is evaluated by the runner AND again in every worker: create the
// scratch dir once (runner) and hand it to the workers through the env they
// inherit — a second mkdtemp would point the specs at files the server never
// reads. Scratch state stays inside the repo (gitignored).
let root = process.env.E2E_ROOT
if (!root) {
  mkdirSync(join(import.meta.dirname, '.run'), { recursive: true })
  root = mkdtempSync(join(import.meta.dirname, '.run', 'e2e-'))
  const e = env(root)
  writeAtomic(e.one, ONE)
  writeAtomic(e.two, TWO)
  writeAtomic(e.extra, EXTRA)
  dockerContexts(e.home)
  process.env.E2E_ROOT = root
}
const e = env(root)

// E2E_BIN runs the suite against another browser-mode build.
const bin = process.env.E2E_BIN ?? '../../build/bin/spk-ocular'

export default defineConfig({
  testDir: '.',
  testMatch: 'targets.spec.ts',
  workers: 1, // one app instance, shared state — specs restore what they change
  use: { baseURL: `http://127.0.0.1:${port}`, locale: 'en-US', screenshot: 'only-on-failure' },
  webServer: {
    command: `${bin} --browser --port ${port} --test-api`,
    url: `http://127.0.0.1:${port}/`,
    env: {
      SPK_OCULAR_HOME: e.dataDir,
      HOME: e.home,
      ...noDockerEnv,
      KUBECONFIG: `${e.one}:${e.two}`,
      LANG: 'en_US.UTF-8',
      LANGUAGE: '',
      LC_ALL: '',
      LC_MESSAGES: '',
      // Fixture clusters are unreachable by design; no detour through a proxy.
      HTTPS_PROXY: '',
      HTTP_PROXY: '',
      https_proxy: '',
      http_proxy: '',
    },
    reuseExistingServer: false,
    // SIGTERM, not the default SIGKILL: the app hangs up its terminals (no
    // shells left in the cluster) and frees its tunnels' ports.
    gracefulShutdown: { signal: 'SIGTERM', timeout: 5000 },
  },
})
