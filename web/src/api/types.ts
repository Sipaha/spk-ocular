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
  /** The scope a first visit shows (k8s: the context's namespace); absent: all. */
  defaultScope?: string
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
  /** What the provider's scopes are called; absent: the UI's generic words. */
  scopeNames?: ScopeNames
}

export interface ScopeNames {
  singular: Message
  plural: Message
  all: Message
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
export type EventType = 'targets_changed' | 'resync' | 'view_changed' | 'forwards_changed' | 'kinds_changed'

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
  /** The object's key: never changes for the object (Docker: a container's full id). */
  name: string
  uid?: string
  /** How the object is shown when it differs from name; display only, never identity. */
  title?: string
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
  /** A collapsible level inside the group (its label), if any. */
  subgroup?: string
  columns: Column[]
  scoped: boolean
  /** Not in the navigation (reached through relations). */
  hidden?: boolean
  /** The kind a first visit of the target opens (none marked: the first in the navigation). */
  default?: boolean
  /** The kind whose view with query.subject = an object of this kind lists events about it; absent: none. */
  eventsKind?: string
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
  /** Objects of this kind can be edited as text (GetEditSource). */
  editable?: boolean
  /** Objects of this kind keep protected values by key (GetValues). */
  values?: boolean
  /** Actions objects of this kind offer (restart, scale, delete, …). */
  actions?: ActionDescriptor[]
  /** How a table of this kind is first sorted (else by the first column). */
  sort?: SortSpec
  /** A view of several sources: what it does not look at by design. */
  notCovered?: string[]
}

/** A session's kinds now (ListKinds). Rev grows within a session; a new session (reconfigured target) starts anew. */
export interface KindsView {
  kinds: KindDescriptor[]
  rev: number
  /** ready | discovering (more kinds may come) | partial (unconfirmed groups keep their last known kinds) | failed */
  state: 'ready' | 'discovering' | 'partial' | 'failed'
  /** Groups whose kinds could not be confirmed ("*": every named group). */
  unconfirmed?: string[]
  session: number
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

/** An object's text for the editor (GetEditSource). */
export interface EditDoc {
  /** the object, with the UID read */
  ref: Ref
  text: string
  /** the object's revision as read (Row.rev's form) */
  version?: string
  /** signed by the backend: sent back with the original text */
  base: string
}

/** What an edit would do, read without changing anything. */
export interface EditPlan {
  where: LiveTarget
  /** the object now and the expected result, as the editor shows objects */
  before: string
  after: string
  /** after is the server's dry run (a prediction); else computed locally */
  checked: boolean
  changed: boolean
  /** the object changed since the text was read: the edit lies over it */
  rebased?: boolean
  /** fields the edit overwrites that changed since the text was read */
  collisions?: string[]
  destructive?: boolean
  warnings?: Message[]
  rights: { state: RightsState; reason?: string }
  /** the server refused the edit */
  unavailable?: Message
  /** present only when the plan can be written */
  token?: string
}

export interface EditPrepareRequest {
  ref: Ref
  base: string
  original: string
  edited: string
}

export interface EditRunRequest extends EditPrepareRequest {
  token: string
}

/** An edit was written; actual is the object as written (may differ from the plan's after). */
export interface EditResult {
  message: string
  version?: string
  actual?: string
}

// ---- protected values (internal/core/values.go)

/** One key of an object's protected values, never its value. */
export interface ValueKey {
  key: string
  /** bytes (decoded) */
  size: number
  /** UTF-8 without control characters but \n and \t: shown and edited as text; else as base64 */
  text?: boolean
}

/** An object's keys, as read now (GetValues). */
export interface ValueList {
  /** the object, with the UID read */
  ref: Ref
  version?: string
  keys: ValueKey[]
  /** signed by the backend: sent back with a key's change */
  base: string
}

/** One key's value, read on request (RevealValue): never kept beyond the view that asked. */
export interface Value {
  key: string
  size: number
  text?: boolean
  /** the text itself when text, else base64 of the bytes */
  value: string
  /** the object and revision it was read from */
  uid: string
  version?: string
}

export type ValueOp = 'set' | 'delete'
export type ValueEncoding = 'text' | 'base64'

/** What reads the object's values, as far as could be seen; unknown is never "nothing". */
export interface ValueConsumers {
  known: boolean
  why?: string
  items?: string[]
}

/** What a key's change would do, read without changing anything and without any value. */
export interface ValuePlan {
  where: LiveTarget
  key: string
  op: ValueOp
  /** sizes in bytes; -1 is absent */
  before: number
  after: number
  /** after is the server's dry run; else computed locally */
  checked: boolean
  changed: boolean
  /** the object changed since its keys were listed */
  rebased?: boolean
  /** this key changed since its keys were listed */
  collision?: boolean
  destructive?: boolean
  /** keys the server's review leaves otherwise than the change asks */
  serverChanges?: string[]
  consumers?: ValueConsumers
  warnings?: Message[]
  rights: { state: RightsState; reason?: string }
  /** why it cannot be written */
  unavailable?: Message
  /** present only when the plan can be written */
  token?: string
}

/** A key's change: set (value in encoding; empty is a value) or delete (no value). */
export interface ValueEditRequest {
  ref: Ref
  base: string
  key: string
  op: ValueOp
  value?: string
  encoding?: ValueEncoding
}

export interface ValueRunRequest extends ValueEditRequest {
  token: string
}

/** A key's change was written. */
export interface ValueResult {
  message: string
  version?: string
  /** keys written otherwise than the review expected */
  differs?: string[]
  /** as reviewed: the server changes what was typed */
  serverChanges?: string[]
}

export type ActionOutcome = 'done' | 'refused' | 'unknown' | 'skipped'

/** One write of a run (a service's container): its object and outcome. */
export interface ActionPart {
  id: string
  title: string
  outcome: ActionOutcome
  message?: string
}

/**
 * A run's answer. Several writes report each in parts; outcome is done only
 * when every part is (absent: done — older answers).
 */
export interface ActionResult {
  message: string
  outcome?: Exclude<ActionOutcome, 'skipped'>
  parts?: ActionPart[]
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
  /** The session reads the view's sources again on request (resyncView, F5). */
  resync?: boolean
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

/** CPU in cores, memory in bytes; absent — that metric is unknown. */
export interface Usage {
  cpu?: number
  memory?: number
  /** A sum of which some parts did not answer: at least this much. */
  cpuPartial?: boolean
  memoryPartial?: boolean
  /** When the sample was taken. */
  at?: string
}

export interface MetricsView {
  /** "ok" or an error class: unsupported (no metrics API), forbidden, ... */
  status: string
  /** The error's detail (not "ok"). */
  message?: string
  /** With "ok": only this many of the asked rows were (the first ones). */
  limit?: number
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
  /** The provider's names of a channel (Container) and of all of them; absent: generic words. */
  channelLabel?: Message
  allChannelsLabel?: Message
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
  /** Nowhere to run now ("No running pods"); absent: generic words. */
  noInstances?: Message
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
