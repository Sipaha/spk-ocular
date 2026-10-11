import {expect} from '@playwright/test'
import {spawn,type ChildProcess} from 'node:child_process'
import {createServer} from 'node:net'
import {env,noDockerEnv} from './fixtures'
const bin = process.env.E2E_BIN ?? '../../build/bin/spk-ocular'

export const freePort = (): Promise<number> =>
  new Promise((resolve, reject) => {
    const s = createServer()
    s.once('error', reject)
    s.listen(0, '127.0.0.1', () => {
      const p = (s.address() as { port: number }).port
      s.close((err) => err ? reject(err) : resolve(p))
    })
  })

export const stop = async (p: ChildProcess) => {
  if (p.exitCode !== null || p.signalCode !== null || !p.pid) return
  await new Promise<void>((resolve) => {
    const timer = setTimeout(() => p.kill('SIGKILL'), 5000)
    p.once('exit', () => { clearTimeout(timer); resolve() })
    p.kill('SIGTERM')
  })
}

export const start = async (port: number, e: ReturnType<typeof env>): Promise<ChildProcess> => {
  const p = spawn(bin, ['--browser', '--port', String(port), '--test-api', '--test-synthetic'], {
    env: {
      ...process.env,
      SPK_OCULAR_HOME: e.dataDir,
      HOME: e.home,
      ...noDockerEnv,
      SPK_OCULAR_TEST_SYNTH_SECOND: '1',
      KUBECONFIG: e.one,
      LANG: 'en_US.UTF-8', LANGUAGE: '', LC_ALL: '', LC_MESSAGES: '',
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
    // This suite covers workspace persistence; dismiss import explicitly before
    // opening its synthetic workspace. Fresh onboarding is tested separately.
    const base=`http://127.0.0.1:${port}`
    const html=await (await fetch(base)).text()
    const token=html.match(/spk-ocular-api-token" content="([^"]+)"/)?.[1]
    const response=await fetch(base+'/api/Configurations',{method:'POST',headers:{Authorization:`Bearer ${token}`,Origin:base,'Content-Type':'application/json'},body:JSON.stringify({command:'dismiss'})})
    expect(response.ok).toBe(true)
    return p
  } catch (error) {
    await stop(p)
    throw new Error(`app did not start: ${stderr}`, { cause: error })
  }
}
