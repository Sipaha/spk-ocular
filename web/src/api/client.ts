import { Call, Events } from '@wailsio/runtime'
import type { ApiEvent, AppInfo, EventType, ExecInfo, KindDescriptor, LogInfo, LogQuery, LogStreamInfo, MetricsView, Page, Query, Ref, Resource, ScopesView, TargetsView, TerminalInfo, TerminalRequest, ViewInfo } from './types'

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
  listKinds(provider: string, target: string): Promise<KindDescriptor[]>
  /** Permission problems come back in ScopesView.error, not as a rejection. */
  listScopes(provider: string, target: string): Promise<ScopesView>
  openView(provider: string, target: string, query: Query): Promise<ViewInfo>
  /** Changes after cursor `since` (0: full snapshot); rejects with code "gone" for a closed view. */
  getRows(viewId: string, since: number): Promise<Page>
  closeView(viewId: string): Promise<void>
  /** Renews leases; returns the ids that are gone. */
  touchViews(viewIds: string[]): Promise<string[]>
  getResource(ref: Ref): Promise<Resource>
  /** Usage for an open view's rows; a missing metrics API is a status, not a rejection. */
  getMetrics(viewId: string): Promise<MetricsView>
  getTargetState(provider: string, target: string): Promise<Record<string, string>>
  setTargetState(provider: string, target: string, key: string, value: string): Promise<void>
  logInfo(ref: Ref): Promise<LogInfo>
  /** Registers a log stream; read it from streamBase() + '/logs/' + streamId. */
  openLogStream(ref: Ref, query: LogQuery): Promise<LogStreamInfo>
  execInfo(ref: Ref): Promise<ExecInfo>
  /** Registers a terminal; open a WebSocket to wsBase(streamBase()) + '/term/' + streamId. */
  openTerminal(req: TerminalRequest): Promise<TerminalInfo>
  /** Runs the terminal's command again with the connection and pod it was opened with. */
  reopenTerminal(terminalId: string, cols: number, rows: number): Promise<TerminalInfo>
  /** The terminal's tab closed. */
  forgetTerminal(terminalId: string): Promise<void>
  /** Desktop: a loopback URL with a token; browser: a path on this server. */
  streamBase(): Promise<string>
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
  listKinds: (provider, target) => post('ListKinds', { provider, target }),
  listScopes: (provider, target) => post('ListScopes', { provider, target }),
  openView: (provider, target, query) => post('OpenView', { provider, target, query }),
  getRows: (viewId, since) => post('GetRows', { viewId, since }),
  closeView: (viewId) => done(post('CloseView', { viewId })),
  touchViews: (viewIds) => post('TouchViews', { viewIds }),
  getResource: (ref) => post('GetResource', ref),
  getMetrics: (viewId) => post('GetMetrics', { viewId }),
  getTargetState: (provider, target) => post('GetTargetState', { provider, target }),
  setTargetState: (provider, target, key, value) => done(post('SetTargetState', { provider, target, key, value })),
  logInfo: (ref) => post('LogInfo', ref),
  openLogStream: (ref, query) => post('OpenLogStream', { ref, query }),
  execInfo: (ref) => post('ExecInfo', ref),
  openTerminal: (req) => post('OpenTerminal', req),
  reopenTerminal: (terminalId, cols, rows) => post('ReopenTerminal', { terminalId, cols, rows }),
  forgetTerminal: (terminalId) => done(post('ForgetTerminal', { terminalId })),
  streamBase: () => post('StreamBase', {}),
  subscribeEvents(onEvent) {
    const es = new EventSource(`/api/events?token=${encodeURIComponent(tokenMeta())}`)
    es.onmessage = (m) => onEvent(JSON.parse(m.data) as ApiEvent)
    // EventSource reconnects by itself; events emitted during the gap are
    // gone, so every (re)open is a resync.
    es.onopen = () => onEvent({ type: 'resync' })
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

const EVENT_TYPES: EventType[] = ['targets_changed', 'resync', 'view_changed']

export const wailsClient: Client = {
  appInfo: () => wcall('AppInfo'),
  listTargets: () => wcall('ListTargets'),
  selectTarget: (provider, id) => wcall('SelectTarget', provider, id),
  listKinds: (provider, target) => wcall('ListKinds', provider, target),
  listScopes: (provider, target) => wcall('ListScopes', provider, target),
  openView: (provider, target, query) => wcall('OpenView', { provider, target, query }),
  getRows: (viewId, since) => wcall('GetRows', viewId, since),
  closeView: (viewId) => wcall('CloseView', viewId),
  touchViews: (viewIds) => wcall('TouchViews', viewIds),
  getResource: (ref) => wcall('GetResource', ref),
  getMetrics: (viewId) => wcall('GetMetrics', viewId),
  getTargetState: (provider, target) => wcall('GetTargetState', provider, target),
  setTargetState: (provider, target, key, value) => wcall('SetTargetState', provider, target, key, value),
  logInfo: (ref) => wcall('LogInfo', ref),
  openLogStream: (ref, query) => wcall('OpenLogStream', { ref, query }),
  execInfo: (ref) => wcall('ExecInfo', ref),
  openTerminal: (req) => wcall('OpenTerminal', req),
  reopenTerminal: (terminalId, cols, rows) => wcall('ReopenTerminal', { terminalId, cols, rows }),
  forgetTerminal: (terminalId) => wcall('ForgetTerminal', terminalId),
  streamBase: () => wcall('StreamBase'),
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
