import type { HelmRequest, HelmResponse } from '../helm/types'
import { Call, Events } from '@wailsio/runtime'
import type { ConnectionStatus, FavoriteKind } from './types'
import type { ActionParams, ActionPlan, AgentAccessStatus, AgentAuditEntry, AgentAuditFilter, AgentGrant, AgentGrantSettings, AgentPending, AgentTarget, ActionResult, ApiEvent, AppInfo, EditDoc, EditPlan, EditPrepareRequest, EditResult, EditRunRequest, EventType, ExecInfo,
  KindsView, LogInfo, Message, LogQuery, LogStreamInfo, MetricsView, Page, Query, RecentObject, Ref, Resource, ScopesView, TargetsView, TerminalInfo, TerminalRequest, ViewInfo, ForwardInfo, StartForwardRequest, Tunnel, Value, ValueEditRequest, ValueList, ValuePlan, ValueResult, ValueRunRequest } from './types'

export class ApiError extends Error {
  constructor(
    public code: string,
    public detail: string,
    /** No coded answer came back (the connection failed, a bare HTTP status): whether the server acted is not known. */
    public transport = false,
    /** The detail as a sentence by key (the UI says it in its language). */
    public why?: Message,
  ) {
    super(detail ? `${code}: ${detail}` : code)
  }
}

export interface Client {
  helm(req: HelmRequest, signal?: AbortSignal): Promise<HelmResponse>
  appInfo(): Promise<AppInfo>
  /** Re-reads local configuration (kubeconfig, ...); no network. */
  listTargets(): Promise<TargetsView>
  /** Remembers the choice across restarts; not_found if the target is gone. */
  selectTarget(provider: string, id: string): Promise<void>
  connectTarget(provider: string, id: string): Promise<ConnectionStatus>
  cancelConnectTarget(provider: string, id: string, attempt: number): Promise<void>
  /** Closes the target's session (its views and streams end); refused on the selected target. */
  closeTarget(provider: string, id: string): Promise<void>
  /** The session's kinds with the catalog's revision (a "kinds_changed" event tells of a new one). */
  listKinds(provider: string, target: string): Promise<KindsView>
  /** Reads the target's kinds again in the background (F5); a change comes as "kinds_changed". */
  refreshKinds(provider: string, target: string): Promise<void>
  /** Permission problems come back in ScopesView.error, not as a rejection. */
  listScopes(provider: string, target: string): Promise<ScopesView>
  openView(provider: string, target: string, query: Query): Promise<ViewInfo>
  /** Changes after cursor `since` (0: full snapshot); rejects with code "gone" for a closed view. */
  getRows(viewId: string, since: number): Promise<Page>
  closeView(viewId: string): Promise<void>
  /** Asks the view's own session to read its sources again (ViewInfo.resync); "gone" for a closed view. */
  resyncView(viewId: string): Promise<void>
  /** Renews leases; returns the ids that are gone. */
  touchViews(viewIds: string[]): Promise<string[]>
  getResource(ref: Ref): Promise<Resource>
  /** Usage for an open view's rows; a missing metrics API is a status, not a rejection. */
  /** Usage of the rows the page shows; signal abandons the request (the server stops waiting too). */
  getMetrics(viewId: string, rowIds: string[], signal?: AbortSignal): Promise<MetricsView>
  getTargetState(provider: string, target: string): Promise<Record<string, string>>
  setTargetState(provider: string, target: string, key: string, value: string): Promise<void>
  getNavSections(): Promise<Record<string, boolean>>
  setNavSection(key: string, open: boolean): Promise<void>
  getFavoriteKinds(): Promise<FavoriteKind[]>
  setKindFavorite(provider: string, kind: string, favorite: boolean): Promise<void>
  moveFavoriteKind(provider: string, kind: string, before: string): Promise<void>
  /** The target's objects whose details were opened, newest first. */
  recentObjects(provider: string, target: string): Promise<RecentObject[]>
  /** Records a successful open of an object's details. */
  touchRecent(ref: Ref, title: string): Promise<void>
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
  /** An object's text for the editor, with a signed base. */
  getEditSource(ref: Ref): Promise<EditDoc>
  /** What an edit would do (nothing changes); a plan that can be written has a token. */
  prepareEdit(req: EditPrepareRequest): Promise<EditPlan>
  /** Writes a reviewed plan once (its token). */
  runEdit(req: EditRunRequest): Promise<EditResult>
  /** An object's keys with sizes (no values), with a signed base. */
  getValues(ref: Ref): Promise<ValueList>
  /** One key's value of that object (ref.uid), read now: keep it only while shown. */
  revealValue(ref: Ref, key: string): Promise<Value>
  /** What a key's change would do (nothing changes, no value in the answer); a plan that can be written has a token. */
  prepareValueEdit(req: ValueEditRequest): Promise<ValuePlan>
  /** Writes a reviewed change once (its token). */
  runValueEdit(req: ValueRunRequest): Promise<ValueResult>
  forwardInfo(ref: Ref): Promise<ForwardInfo>
  /** Listens on loopback and connects once; a failed first connect rejects (conflict: the local port is taken). */
  startForward(req: StartForwardRequest): Promise<Tunnel>
  stopForward(id: string): Promise<void>
  /** Reload on 'forwards_changed'. */
  listForwards(): Promise<Tunnel[]>
  /** Desktop: a loopback URL with a token; browser: a path on this server. */
  streamBase(): Promise<string>
  /** The agent socket's state and the line for agents' instructions. */
  agentAccessStatus(): Promise<AgentAccessStatus>
  /** Every target with grants; reload on 'agent_grants_changed'. */
  listAgentGrants(): Promise<AgentTarget[]>
  /** Sets a target's grants whole (none: revoked); a target granted anew is bound to what it points at now. */
  saveAgentGrants(provider: string, target: string, grants: AgentGrant[], settings?: AgentGrantSettings): Promise<void>
  revokeAllAgentGrants(): Promise<void>
  /** A suspended target's grants hold for what it points at now. */
  /** Grants a suspended target for the identity the user was shown (conflict if it changed again). */
  reconfirmAgentTarget(provider: string, target: string, observed: string): Promise<void>
  /** Agents' destructive plans waiting for the user; reload on 'agent_pending_changed'. */
  listAgentPending(): Promise<AgentPending[]>
  /** gone: decided elsewhere or expired. */
  decideAgentPending(id: string, approve: boolean): Promise<void>
  /** The journal, newest first; reload on 'agent_audit_changed'. */
  listAgentAudit(filter: AgentAuditFilter): Promise<AgentAuditEntry[]>
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

async function post<T>(method: string, body: unknown, signal?: AbortSignal): Promise<T> {
  const headers: Record<string, string> = { 'content-type': 'application/json' }
  const token = tokenMeta()
  if (token) headers.Authorization = `Bearer ${token}`
  let r: Response
  try {
    r = await fetch(`/api/${method}`, { method: 'POST', headers, body: JSON.stringify(body ?? {}), signal })
  } catch (e) {
    if (signal?.aborted) throw e // the caller's own abort, not a transport failure
    throw new ApiError(CodeInternal, e instanceof Error ? e.message : String(e), true)
  }
  const isJSON = r.headers.get('content-type')?.includes('application/json')
  if (!r.ok) {
    const e = isJSON ? ((await r.json().catch(() => null)) as { code?: string; detail?: string; why?: unknown } | null) : null
    if (e?.code) throw new ApiError(e.code, e.detail ?? '', false, asMessage(e.why))
    throw new ApiError(CodeInternal, `HTTP ${r.status}`, true)
  }
  return (isJSON ? await r.json() : undefined) as T
}

/**
 * Orders the page's metrics requests (per view in Go: an older one never
 * starts after a newer one or its own cancel). Starts at the clock so a
 * reloaded page is ahead too.
 */
let metricsSeq = Date.now()
const nextMetricsSeq = () => ++metricsSeq

const done = async (p: Promise<unknown>) => {
  await p
}

export const httpClient: Client = {
  appInfo: () => post('AppInfo', {}),
  helm: (req, signal) => post('Helm', req, signal),
  listTargets: () => post('ListTargets', {}),
  selectTarget: (provider, id) => done(post('SelectTarget', { provider, id })),
  connectTarget: (provider, id) => post('ConnectTarget', { provider, id }),
  cancelConnectTarget: (provider, id, attempt) => done(post('CancelConnectTarget', { provider, id, attempt })),
  closeTarget: (provider, id) => done(post('CloseTarget', { provider, id })),
  listKinds: (provider, target) => post('ListKinds', { provider, target }),
  refreshKinds: (provider, target) => done(post('RefreshKinds', { provider, target })),
  listScopes: (provider, target) => post('ListScopes', { provider, target }),
  openView: (provider, target, query) => post('OpenView', { provider, target, query }),
  getRows: (viewId, since) => post('GetRows', { viewId, since }),
  closeView: (viewId) => done(post('CloseView', { viewId })),
  resyncView: (viewId) => done(post('ResyncView', { viewId })),
  touchViews: (viewIds) => post('TouchViews', { viewIds }),
  getResource: (ref) => post('GetResource', ref),
  getMetrics: (viewId, rowIds, signal) => post('GetMetrics', { viewId, rowIds, seq: nextMetricsSeq() }, signal),
  getTargetState: (provider, target) => post('GetTargetState', { provider, target }),
  setTargetState: (provider, target, key, value) => done(post('SetTargetState', { provider, target, key, value })),
  getNavSections: () => post('GetNavSections', {}),
  setNavSection: (key, open) => done(post('SetNavSection', { key, open })),
  getFavoriteKinds: () => post('GetFavoriteKinds', {}),
  setKindFavorite: (provider, kind, favorite) => done(post('SetKindFavorite', { provider, kind, favorite })),
  moveFavoriteKind: (provider, kind, before) => done(post('MoveFavoriteKind', { provider, kind, before })),
  recentObjects: (provider, target) => post('RecentObjects', { provider, target }),
  touchRecent: (ref, title) => done(post('TouchRecent', { ref, title })),
  logInfo: (ref) => post('LogInfo', ref),
  openLogStream: (ref, query) => post('OpenLogStream', { ref, query }),
  execInfo: (ref) => post('ExecInfo', ref),
  openTerminal: (req) => post('OpenTerminal', req),
  reopenTerminal: (terminalId, cols, rows) => post('ReopenTerminal', { terminalId, cols, rows }),
  forgetTerminal: (terminalId) => done(post('ForgetTerminal', { terminalId })),
  prepareAction: (ref, action, params) => post('PrepareAction', { ref, action, params }),
  runAction: (plan) => post('RunAction', runRequest(plan)),
  getEditSource: (ref) => post('GetEditSource', ref),
  prepareEdit: (req) => post('PrepareEdit', req),
  runEdit: (req) => post('RunEdit', req),
  getValues: (ref) => post('GetValues', ref),
  revealValue: (ref, key) => post('RevealValue', { ref, key }),
  prepareValueEdit: (req) => post('PrepareValueEdit', req),
  runValueEdit: (req) => post('RunValueEdit', req),
  forwardInfo: (ref) => post('ForwardInfo', ref),
  startForward: (req) => post('StartForward', req),
  stopForward: (id) => done(post('StopForward', { id })),
  listForwards: () => post('ListForwards', {}),
  streamBase: () => post('StreamBase', {}),
  agentAccessStatus: () => post('AgentAccessStatus', {}),
  listAgentGrants: () => post('ListAgentGrants', {}),
  saveAgentGrants: (provider, target, grants, settings) => done(post('SaveAgentGrants', { provider, target, grants, ...settings })),
  revokeAllAgentGrants: () => done(post('RevokeAllAgentGrants', {})),
  reconfirmAgentTarget: (provider, target, observed) => done(post('ReconfirmAgentTarget', { provider, target, observed })),
  listAgentPending: () => post('ListAgentPending', {}),
  decideAgentPending: (id, approve) => done(post('DecideAgentPending', { id, approve })),
  listAgentAudit: (filter) => post('ListAgentAudit', filter),
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
  // The runtime puts the error's JSON (CodedError) on cause: its reason.
  const cause = (err as { cause?: unknown })?.cause
  const why = cause && typeof cause === 'object' ? asMessage((cause as { why?: unknown }).why) : undefined
  return new ApiError(code, i < 0 ? '' : msg.slice(i + 2), false, why)
}

/** v as a provider's sentence if it has that shape (text; key and string params optional), else undefined. */
export function asMessage(v: unknown): Message | undefined {
  if (!v || typeof v !== 'object') return undefined
  const m = v as { key?: unknown; params?: unknown; text?: unknown }
  if (typeof m.text !== 'string') return undefined
  if (m.key !== undefined && typeof m.key !== 'string') return undefined
  if (m.params !== undefined && (!m.params || typeof m.params !== 'object' || Object.values(m.params).some((x) => typeof x !== 'string'))) return undefined
  return m as Message
}

const FQN = 'github.com/spk/spk-ocular/internal/api/transport.API.'

async function wcall<T>(method: string, ...args: unknown[]): Promise<T> {
  try {
    return (await Call.ByName(FQN + method, ...args)) as T
  } catch (e) {
    throw parseWailsError(e)
  }
}

/** A call the page may abandon: an abort cancels it in Go (the method's context). */
async function wcallAbortable<T>(signal: AbortSignal | undefined, method: string, ...args: unknown[]): Promise<T> {
  if (signal?.aborted) throw new DOMException('aborted', 'AbortError')
  const p = Call.ByName(FQN + method, ...args)
  // An abort answers at once, whenever the runtime settles the call.
  let onAbort = () => {}
  const aborted = new Promise<never>((_, reject) => {
    onAbort = () => {
      void p.cancel()
      reject(new DOMException('aborted', 'AbortError'))
    }
  })
  signal?.addEventListener('abort', onAbort, { once: true })
  try {
    return (await Promise.race([p, aborted])) as T
  } catch (e) {
    if (signal?.aborted) throw new DOMException('aborted', 'AbortError')
    throw parseWailsError(e)
  } finally {
    signal?.removeEventListener('abort', onAbort)
  }
}

const EVENT_TYPES: EventType[] = ['targets_changed', 'resync', 'view_changed', 'forwards_changed', 'kinds_changed', 'agent_grants_changed', 'agent_pending_changed', 'agent_audit_changed']

export const wailsClient: Client = {
  appInfo: () => wcall('AppInfo'),
  helm: (req, signal) => wcallAbortable(signal, 'Helm', req),
  listTargets: () => wcall('ListTargets'),
  selectTarget: (provider, id) => wcall('SelectTarget', provider, id),
  connectTarget: (provider, id) => wcall('ConnectTarget', provider, id),
  cancelConnectTarget: (provider, id, attempt) => wcall('CancelConnectTarget', provider, id, attempt),
  closeTarget: (provider, id) => wcall('CloseTarget', provider, id),
  listKinds: (provider, target) => wcall('ListKinds', provider, target),
  refreshKinds: (provider, target) => wcall('RefreshKinds', provider, target),
  listScopes: (provider, target) => wcall('ListScopes', provider, target),
  openView: (provider, target, query) => wcall('OpenView', { provider, target, query }),
  getRows: (viewId, since) => wcall('GetRows', viewId, since),
  closeView: (viewId) => wcall('CloseView', viewId),
  resyncView: (viewId) => wcall('ResyncView', viewId),
  touchViews: (viewIds) => wcall('TouchViews', viewIds),
  getResource: (ref) => wcall('GetResource', ref),
  getMetrics: (viewId, rowIds, signal) => {
    const seq = nextMetricsSeq()
    // The runtime's cancel is lost if it overtakes the call: Go is told too.
    const onAbort = () => void wcall('CancelMetrics', viewId, seq).catch(() => {})
    signal?.addEventListener('abort', onAbort, { once: true })
    return wcallAbortable<MetricsView>(signal, 'GetMetrics', { viewId, rowIds, seq }).finally(() => signal?.removeEventListener('abort', onAbort))
  },
  getTargetState: (provider, target) => wcall('GetTargetState', provider, target),
  setTargetState: (provider, target, key, value) => wcall('SetTargetState', provider, target, key, value),
  getNavSections: () => wcall('GetNavSections'),
  setNavSection: (key, open) => wcall('SetNavSection', { key, open }),
  getFavoriteKinds: () => wcall('GetFavoriteKinds'),
  setKindFavorite: (provider, kind, favorite) => wcall('SetKindFavorite', { provider, kind, favorite }),
  moveFavoriteKind: (provider, kind, before) => wcall('MoveFavoriteKind', { provider, kind, before }),
  recentObjects: (provider, target) => wcall('RecentObjects', provider, target),
  touchRecent: (ref, title) => wcall('TouchRecent', { ref, title }),
  logInfo: (ref) => wcall('LogInfo', ref),
  openLogStream: (ref, query) => wcall('OpenLogStream', { ref, query }),
  execInfo: (ref) => wcall('ExecInfo', ref),
  openTerminal: (req) => wcall('OpenTerminal', req),
  reopenTerminal: (terminalId, cols, rows) => wcall('ReopenTerminal', { terminalId, cols, rows }),
  forgetTerminal: (terminalId) => wcall('ForgetTerminal', terminalId),
  prepareAction: (ref, action, params) => wcall('PrepareAction', { ref, action, params }),
  runAction: (plan) => wcall('RunAction', runRequest(plan)),
  getEditSource: (ref) => wcall('GetEditSource', ref),
  prepareEdit: (req) => wcall('PrepareEdit', req),
  runEdit: (req) => wcall('RunEdit', req),
  getValues: (ref) => wcall('GetValues', ref),
  revealValue: (ref, key) => wcall('RevealValue', { ref, key }),
  prepareValueEdit: (req) => wcall('PrepareValueEdit', req),
  runValueEdit: (req) => wcall('RunValueEdit', req),
  forwardInfo: (ref) => wcall('ForwardInfo', ref),
  startForward: (req) => wcall('StartForward', req),
  stopForward: (id) => wcall('StopForward', id),
  listForwards: () => wcall('ListForwards'),
  streamBase: () => wcall('StreamBase'),
  agentAccessStatus: () => wcall('AgentAccessStatus'),
  listAgentGrants: () => wcall('ListAgentGrants'),
  saveAgentGrants: (provider, target, grants, settings) => wcall('SaveAgentGrants', { provider, target, grants, ...settings }),
  revokeAllAgentGrants: () => wcall('RevokeAllAgentGrants'),
  reconfirmAgentTarget: (provider, target, observed) => wcall('ReconfirmAgentTarget', { provider, target, observed }),
  listAgentPending: () => wcall('ListAgentPending'),
  decideAgentPending: (id, approve) => wcall('DecideAgentPending', { id, approve }),
  listAgentAudit: (filter) => wcall('ListAgentAudit', filter),
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
