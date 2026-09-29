import { mkdirSync, renameSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'

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
