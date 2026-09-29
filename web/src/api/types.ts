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
 */
export type EventType = 'targets_changed' | 'resync' | 'view_changed'

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
  /** owner | owns | selects | routes-to | runs-on */
  type: string
  ref: Ref
}

export interface Resource {
  ref: Ref
  health: Health
  facts: Detail[]
  yaml: string
  relations: Relation[] | null
  relationsError?: string
}

export interface Usage {
  cpu: number
  memory: number
}

export interface MetricsView {
  /** "ok" or an error class: unsupported (no metrics API), forbidden, ... */
  status: string
  message?: string
  timestamp?: string
  window?: string
  values: Record<string, Usage>
}
