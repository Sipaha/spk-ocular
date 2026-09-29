import { Call, Events } from '@wailsio/runtime'
import type { ActionParams, ActionPlan, ActionResult, ApiEvent, AppInfo, EventType, ExecInfo, KindDescriptor, LogInfo, LogQuery, LogStreamInfo, MetricsView, Page, Query, Ref, Resource, ScopesView, TargetsView, TerminalInfo, TerminalRequest, ViewInfo, ForwardInfo, StartForwardRequest, Tunnel } from './types'

export class ApiError extends Error {
  constructor(
    public code: string,
    public detail: string,
    /** No coded answer came back (the connection failed, a bare HTTP status): whether the server acted is not known. */
    public transport = false,
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
  /** What an action would do (nothing changes); the confirmation shows it. */
  prepareAction(ref: Ref, action: string, params: ActionParams): Promise<ActionPlan>
  /** Runs a confirmed plan: its object, params, expect and target revision. */
  runAction(plan: ActionPlan): Promise<ActionResult>
  forwardInfo(ref: Ref): Promise<ForwardInfo>
  /** Listens on loopback and connects once; a failed first connect rejects (conflict: the local port is taken). */
  startForward(req: StartForwardRequest): Promise<Tunnel>
  stopForward(id: string): Promise<void>
  /** Reload on 'forwards_changed'. */
  listForwards(): Promise<Tunnel[]>
  /** Desktop: a loopback URL with a token; browser: a path on this server. */
  streamBase(): Promise<string>
  subscribeEvents(onEvent: (e: ApiEvent) => void): () => void
}

/** The run of a confirmed plan: exactly what was reviewed goes back. */
export const runRequest = (plan: ActionPlan) => ({
  ref: plan.where.ref,
  action: plan.action.id,
  params: plan.params,
  expect: plan.expect,
  configRev: plan.where.configRev ?? '',
})

const CodeInternal = 'internal'

const tokenMeta = () => document.querySelector('meta[name="spk-ocular-api-token"]')?.getAttribute('content') ?? ''

async function post<T>(method: string, body: unknown): Promise<T> {
  const headers: Record<string, string> = { 'content-type': 'application/json' }
  const token = tokenMeta()
  if (token) headers.Authorization = `Bearer ${token}`
  let r: Response
  try {
    r = await fetch(`/api/${method}`, { method: 'POST', headers, body: JSON.stringify(body ?? {}) })
  } catch (e) {
    throw new ApiError(CodeInternal, e instanceof Error ? e.message : String(e), true)
  }
  const isJSON = r.headers.get('content-type')?.includes('application/json')
  if (!r.ok) {
    const e = isJSON ? ((await r.json().catch(() => null)) as { code?: string; detail?: string } | null) : null
    if (e?.code) throw new ApiError(e.code, e.detail ?? '')
    throw new ApiError(CodeInternal, `HTTP ${r.status}`, true)
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
  prepareAction: (ref, action, params) => post('PrepareAction', { ref, action, params }),
  runAction: (plan) => post('RunAction', runRequest(plan)),
  forwardInfo: (ref) => post('ForwardInfo', ref),
  startForward: (req) => post('StartForward', req),
  stopForward: (id) => done(post('StopForward', { id })),
  listForwards: () => post('ListForwards', {}),
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

/**
 * Wails rejects with an Error whose message is CodedError's "code: detail";
 * a message that does not start with a code came from the runtime, not from
 * an API method (a transport failure).
 */
export function parseWailsError(err: unknown): ApiError {
  const msg = err instanceof Error ? err.message : String((err as { message?: string })?.message ?? err)
  const i = msg.indexOf(': ')
  const code = i < 0 ? msg : msg.slice(0, i)
  if (!/^[a-z][a-z_]*$/.test(code)) return new ApiError(CodeInternal, msg, true)
  return new ApiError(code, i < 0 ? '' : msg.slice(i + 2))
}

const FQN = 'github.com/spk/spk-ocular/internal/api/transport.API.'

async function wcall<T>(method: string, ...args: unknown[]): Promise<T> {
  try {
    return (await Call.ByName(FQN + method, ...args)) as T
  } catch (e) {
    throw parseWailsError(e)
  }
}

const EVENT_TYPES: EventType[] = ['targets_changed', 'resync', 'view_changed', 'forwards_changed']

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
  prepareAction: (ref, action, params) => wcall('PrepareAction', { ref, action, params }),
  runAction: (plan) => wcall('RunAction', runRequest(plan)),
  forwardInfo: (ref) => wcall('ForwardInfo', ref),
  startForward: (req) => wcall('StartForward', req),
  stopForward: (id) => wcall('StopForward', id),
  listForwards: () => wcall('ListForwards'),
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
