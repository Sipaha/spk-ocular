// Mirrors internal/api (Go) DTOs.

export interface Detail {
  key: string
  value: string
}

export interface Target {
  provider: string
  id: string
  title: string
  subtitle?: string
  current?: boolean
  details?: Detail[]
  /** opaque revision of the configuration: equal = the same */
  configRev?: string
}

export interface Problem {
  source: string
  message: string
}

export interface TargetGroup {
  provider: string
  title: string
  targets: Target[]
  problems: Problem[]
  error?: string
  /** Names of the provider's scopes and targets in palette commands (":ns demo", ":ctx prod"). */
  aliases?: { scope?: string[]; target?: string[] }
}

/** An object whose details were opened (its UID as of then). */
export interface RecentObject {
  ref: Ref
  title: string
  /** Unix milliseconds. */
  openedAt: number
}

export interface TargetRef {
  provider: string
  id: string
}

export interface TargetsView {
  groups: TargetGroup[]
  selected: TargetRef | null
}

export interface AppInfo {
  name: string
  version: string
  mode: 'desktop' | 'browser'
  language: 'ru' | 'en'
}

/**
 * resync: the UI fell behind the backend's events; reload all state.
 * view_changed: {viewId, version} — pull; {viewId, gone: true} — reopen.
 * forwards_changed: the tunnels changed (counters at most once a second); call listForwards.
 */
export type EventType = 'targets_changed' | 'resync' | 'view_changed' | 'forwards_changed'

export interface ApiEvent {
  type: EventType
  payload?: Record<string, unknown>
}

// ---- resources and views (internal/core, internal/provider, internal/views)

export interface Ref {
  provider: string
  target: string
  scope?: string
  kind: string
  name: string
  uid?: string
}

export type HealthState = 'ok' | 'progressing' | 'warning' | 'error' | 'terminating' | 'unknown'

export interface Issue {
  state: HealthState
  reason: string
  message?: string
  /** When the issue began (unix ms), if the provider knows it. */
  since?: number
}

export interface Health {
  state: HealthState
  reason?: string
  message?: string
  issues?: Issue[]
}

/** A typed value; `{}` is "no value". */
export interface Cell {
  text?: string
  num?: number
  /** unix ms (age columns) */
  time?: number
  /** Shown quieter: evidence (a recent event), not a current state. */
  muted?: boolean
}

export interface Row {
  id: string
  /** Provider revision: changes with any change of the object. */
  rev?: string
  ref: Ref
  cells: Cell[]
  health: Health
}

export type ColumnType = 'text' | 'number' | 'age' | 'status' | 'ratio' | 'cpu' | 'bytes'

export interface Column {
  id: string
  title: string
  type: ColumnType
  width?: number
  scopeColumn?: boolean
  metric?: boolean
}

export interface KindDescriptor {
  id: string
  title: string
  group: string
  columns: Column[]
  scoped: boolean
  /** Not in the navigation (reached through relations). */
  hidden?: boolean
  /** One object of the kind ("Deployment"; title is the navigation's plural). */
  singular?: string
  /** Short names in palette commands (":po"), besides the id and title. */
  aliases?: string[]
  /** Objects of this kind have logs. */
  logs?: boolean
  /** A command (terminal) can run in objects of this kind. */
  exec?: boolean
  /** Ports of objects of this kind can be forwarded. */
  forward?: boolean
  /** Actions objects of this kind offer (restart, scale, delete, …). */
  actions?: ActionDescriptor[]
  /** How a table of this kind is first sorted (else by the first column). */
  sort?: SortSpec
}

/** Sort by column (descending if desc), ties by then (ascending). */
export interface SortSpec {
  column: string
  desc?: boolean
  then?: string
}

// ---- actions (internal/core/action.go)

export interface ActionParam {
  /** 'count': an integer in min..max */
  kind: string
  min: number
  max: number
}

export interface ActionDescriptor {
  id: string
  title: string
  destructive?: boolean
  param?: ActionParam
}

export interface ActionParams {
  count?: number
}

export type RightsState = 'allowed' | 'denied' | 'unknown'

/** A provider's sentence: said by key in the UI's language (i18n messageText), else its English text. */
export interface Message {
  key?: string
  params?: Record<string, string>
  text: string
}

/** What an action would do, read without changing anything. */
export interface ActionPlan {
  /** the target (title, endpoint, configRev) and the object with its UID as read now */
  where: LiveTarget
  action: ActionDescriptor
  params: ActionParams
  current?: number
  /** destructive for these params (delete; scale down deleting claims; scale to 0) */
  destructive?: boolean
  effects?: Message[]
  warnings?: Message[]
  rights: { state: RightsState; reason?: string }
  /** why it cannot run in the object's state */
  unavailable?: Message
  /** opaque: sent back with the run */
  expect: string
}

export interface ActionResult {
  message: string
}

export type ScopeMode = 'all' | 'one' | 'none'

export interface ScopeSel {
  mode: ScopeMode
  name?: string
}

export interface Query {
  kind: string
  scope: ScopeSel
  subject?: Ref
  /** One object by name (details follow their object). */
  name?: string
}

export interface CodedErrorDTO {
  code: string
  detail: string
}

export interface ScopesView {
  scopes: { name: string }[]
  /** A kind whose live rows are the scopes (k8s: namespaces). */
  kind?: string
  error?: CodedErrorDTO
}

export interface ViewInfo {
  viewId: string
  kind: KindDescriptor
}

export type StatusState = 'loading' | 'ready' | 'stale' | 'error'

export interface ViewStatus {
  state: StatusState
  class?: string
  message?: string
  /** A view of several sources: how each is observed (provider order). */
  coverage?: SourceCoverage[]
}

export type CoverageState = 'loading' | 'ready' | 'stale' | 'denied' | 'error'

export interface SourceCoverage {
  source: string
  state: CoverageState
  class?: string
  message?: string
}

export interface Page {
  viewId: string
  version: number
  reset: boolean
  upserts: Row[]
  deleted: string[]
  status: ViewStatus
}

export interface Relation {
  /** owner | owns | selects | routes-to | runs-on | about */
  type: string
  ref: Ref
  /** Named, not openable (a kind not shown, or an object that cannot be pinned down). */
  inert?: boolean
}

export interface Resource {
  ref: Ref
  health: Health
  facts: Detail[]
  yaml: string
  relations: Relation[] | null
  relationsError?: string
  /** A relation list hit its cap; more exist. */
  relationsTruncated?: boolean
}

export interface Usage {
  cpu: number
  memory: number
  /** When the sample was taken. */
  at?: string
}

export interface MetricsView {
  /** "ok" or an error class: unsupported (no metrics API), forbidden, ... */
  status: string
  message?: string
  timestamp?: string
  window?: string
  values: Record<string, Usage>
}

export interface LogChannel {
  id: string
  title: string
  /** "init", "sidecar", "ephemeral" */
  note?: string
}

export interface LogInfo {
  channels: LogChannel[]
  defaultChannel: string
  /** The object is a group of sources (a workload's pods). */
  aggregate: boolean
  /** Previous-instance logs make sense (one pod). */
  previous: boolean
}

export interface LogQuery {
  /** "" = default, "*" = all */
  channel?: string
  previous?: boolean
  follow?: boolean
  /** N > 0 or -1 = all (within the backend's budget) */
  tailLines: number
  /** Absolute cutoff (RFC 3339), fixed when the stream is opened. */
  sinceTime?: string
}

export interface LogStreamInfo {
  /** GET <streamBase>/logs/<streamId> once, within 30 s. */
  streamId: string
}

export interface ExecChannel {
  id: string
  title: string
  note?: string
  running: boolean
  state?: string
}

export interface ExecInstance {
  id: string
  title: string
  ready: boolean
  channels: ExecChannel[]
  defaultChannel: string
}

export interface ExecInfo {
  instances: ExecInstance[]
  defaultInstance: string
  /** The provider's names of the levels (Pod, Container); absent: generic words. */
  instanceLabel?: Message
  channelLabel?: Message
}

/** What a live resource (terminal, tunnel) is connected to, as captured when opened. */
export interface LiveTarget {
  provider: string
  target: string
  targetTitle: string
  endpoint?: string
  /** the target's configRev when opened: another now = reconfigured since */
  configRev?: string
  ref: Ref
  instance?: string
  channel?: string
  /** the remote port of a tunnel */
  port?: number
  command?: string[]
}

export interface TerminalRequest {
  ref: Ref
  instance?: string
  channel?: string
  /** argv; empty = the interactive shell */
  command?: string[]
  cols: number
  rows: number
}

export interface TerminalInfo {
  /** Names the terminal across reconnects; forget it when the tab closes. */
  terminalId: string
  /** WebSocket to <streamBase as ws:>/term/<streamId> once, within 30 s. */
  streamId: string
  target: LiveTarget
}

// ---- tunnels (internal/core ForwardInfo, internal/forwards Info)

export interface ForwardPort {
  port: number
  name?: string
  protocol: string
  /** where it leads, human-readable */
  note?: string
  /** 'http' | 'https' when the port is known to speak it: offer "Open" */
  scheme?: string
  supported: boolean
  reason?: string
}

export interface ForwardInfo {
  ports: ForwardPort[]
  /** a port that is not listed can be forwarded too (a Pod) */
  anyPort?: boolean
  /** nothing can be forwarded, and why */
  unsupported?: string
}

export interface StartForwardRequest {
  ref: Ref
  port: number
  /** 0/absent: the remote port when ≥ 1024 and free, otherwise any */
  localPort?: number
  scheme?: string
}

export type ForwardState = 'connecting' | 'ready' | 'idle' | 'error'

export interface Tunnel {
  id: string
  target: LiveTarget
  localPort: number
  /** actual addresses: 127.0.0.1:<port> and [::1]:<port> when ours */
  addresses: string[]
  ipv6: 'ok' | 'busy' | 'unavailable'
  ipv6Detail?: string
  scheme?: string
  state: ForwardState
  upstream?: string
  conns: number
  served: number
  rejected: number
  failed: number
  bytesIn: number
  bytesOut: number
  lastError?: { class: string; message: string; at: string }
  started: string
}
