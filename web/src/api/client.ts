import { Call, Events } from '@wailsio/runtime'
import type { ApiEvent, AppInfo, EventType, TargetsView } from './types'

export class ApiError extends Error {
  constructor(
    public code: string,
    public detail: string,
  ) {
    super(detail ? `${code}: ${detail}` : code)
  }
}

export interface Client {
  appInfo(): Promise<AppInfo>
  /** Re-reads local configuration (kubeconfig, ...); no network. */
  listTargets(): Promise<TargetsView>
  /** Remembers the choice across restarts; not_found if the target is gone. */
  selectTarget(provider: string, id: string): Promise<void>
  subscribeEvents(onEvent: (e: ApiEvent) => void): () => void
}

const tokenMeta = () => document.querySelector('meta[name="spk-ocular-api-token"]')?.getAttribute('content') ?? ''

async function post<T>(method: string, body: unknown): Promise<T> {
  const headers: Record<string, string> = { 'content-type': 'application/json' }
  const token = tokenMeta()
  if (token) headers.Authorization = `Bearer ${token}`
  const r = await fetch(`/api/${method}`, { method: 'POST', headers, body: JSON.stringify(body ?? {}) })
  const isJSON = r.headers.get('content-type')?.includes('application/json')
  if (!r.ok) {
    if (isJSON) {
      const e = (await r.json()) as { code?: string; detail?: string }
      throw new ApiError(e.code ?? 'internal', e.detail ?? '')
    }
    throw new ApiError('internal', `HTTP ${r.status}`)
  }
  return (isJSON ? await r.json() : undefined) as T
}

const done = async (p: Promise<unknown>) => {
  await p
}

export const httpClient: Client = {
  appInfo: () => post('AppInfo', {}),
  listTargets: () => post('ListTargets', {}),
  selectTarget: (provider, id) => done(post('SelectTarget', { provider, id })),
  subscribeEvents(onEvent) {
    const es = new EventSource(`/api/events?token=${encodeURIComponent(tokenMeta())}`)
    es.onmessage = (m) => onEvent(JSON.parse(m.data) as ApiEvent)
    // EventSource reconnects by itself; after a gap the view may be stale,
    // so every (re)open is reported as a change.
    es.onopen = () => onEvent({ type: 'targets_changed' })
    return () => es.close()
  },
}

/** Wails rejects with an Error whose message is CodedError's "code: detail". */
export function parseWailsError(err: unknown): ApiError {
  const msg = err instanceof Error ? err.message : String((err as { message?: string })?.message ?? err)
  const i = msg.indexOf(': ')
  return i < 0 ? new ApiError(msg, '') : new ApiError(msg.slice(0, i), msg.slice(i + 2))
}

const FQN = 'github.com/spk/spk-ocular/internal/api/transport.API.'

async function wcall<T>(method: string, ...args: unknown[]): Promise<T> {
  try {
    return (await Call.ByName(FQN + method, ...args)) as T
  } catch (e) {
    throw parseWailsError(e)
  }
}

const EVENT_TYPES: EventType[] = ['targets_changed']

export const wailsClient: Client = {
  appInfo: () => wcall('AppInfo'),
  listTargets: () => wcall('ListTargets'),
  selectTarget: (provider, id) => wcall('SelectTarget', provider, id),
  subscribeEvents(onEvent) {
    const offs = EVENT_TYPES.map((type) =>
      Events.On(type, (ev: { data: unknown }) => {
        onEvent({ type, payload: (ev.data ?? undefined) as Record<string, unknown> | undefined })
      }),
    )
    return () => offs.forEach((off) => off())
  },
}

/** Desktop pages load from wails:// (Linux/macOS) or http(s)://wails.localhost (Windows). */
export function isDesktop(loc: Pick<Location, 'protocol' | 'hostname'> = window.location): boolean {
  return loc.protocol === 'wails:' || loc.hostname === 'wails.localhost'
}

export const client: Client = isDesktop() ? wailsClient : httpClient
