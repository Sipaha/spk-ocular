import { ApiError, type Client } from '../api/client'
import type { KindDescriptor, Page, Query, Row, ViewStatus } from '../api/types'

/** How often open views renew their leases (backend closes after 60 s). */
export const TOUCH_INTERVAL_MS = 20_000
const MAX_RETRIES = 5

export interface ViewState {
  /** The backend view id (for metrics); null while (re)opening. */
  viewId: string | null
  kind: KindDescriptor | null
  /** The session reads the view's sources again on request (ViewInfo.resync). */
  resync: boolean
  rows: Row[]
  status: ViewStatus
  /** OpenView failed (unknown kind, target gone, ...). */
  openError: string | null
  /** The view does nothing more by itself: its open failed, its kind is no
   * longer served, or retries and reopens ran out. A new view may do better. */
  halted: boolean
}

type Timers = { setTimeout: typeof setTimeout; clearTimeout: typeof clearTimeout }

/**
 * One live table, client side of the cursor protocol (docs/architecture.md): one pull in flight; the cursor advances only after a page is
 * applied; pull again while the applied version is below the announced one;
 * bounded retries on failure; a "gone" view is reopened; responses of an
 * older open (generation) or after dispose are ignored, and a late OpenView
 * result after dispose is closed. A view whose kind's columns changed
 * ("schema_changed") opens again (bounded); a kind no longer served
 * ("removed") ends the view for good: no rows, no reopening, no retries.
 */
export class ViewSync {
  private viewId: string | null = null
  private generation = 0
  private cursor = 0
  private desired = 0
  private force = false
  private pulling = false
  private retries = 0
  private retryTimer: ReturnType<typeof setTimeout> | null = null
  private disposed = false
  /** The kind is no longer served: the view is over. */
  private ended = false
  /** Reopens for changed columns since the last ready page. */
  private schemaReopens = 0
  /** A reopened view's kind, shown with its first snapshot. */
  private pendingKind: KindDescriptor | null = null
  private rows = new Map<string, Row>()
  private state: ViewState = { viewId: null, kind: null, resync: false, rows: [], status: { state: 'loading' }, openError: null, halted: false }
  private listeners = new Set<() => void>()

  constructor(
    private client: Client,
    readonly provider: string,
    readonly target: string,
    readonly query: Query,
    private timers: Timers = { setTimeout, clearTimeout },
  ) {}

  get id(): string | null {
    return this.viewId
  }

  snapshot = (): ViewState => this.state

  subscribe = (fn: () => void): (() => void) => {
    this.listeners.add(fn)
    return () => this.listeners.delete(fn)
  }

  private emit(patch: Partial<ViewState>) {
    this.state = { ...this.state, ...patch }
    this.listeners.forEach((fn) => fn())
  }

  async open(): Promise<void> {
    // A new generation: in-flight pulls and retries of the previous view are
    // stale and must not block this one (their finally checks generation).
    const gen = ++this.generation
    this.pulling = false
    if (this.retryTimer) this.timers.clearTimeout(this.retryTimer)
    this.retryTimer = null
    this.retries = 0
    this.viewId = null
    this.cursor = 0
    this.desired = 0
    this.force = false
    this.ended = false
    // No id while reopening: metrics must not poll the previous view.
    if (this.state.viewId !== null || this.state.halted) this.emit({ viewId: null, halted: false })
    try {
      const info = await this.client.openView(this.provider, this.target, this.query)
      if (this.disposed || gen !== this.generation) {
        void this.client.closeView(info.viewId).catch(() => {})
        return
      }
      this.viewId = info.viewId
      this.retries = 0
      // Rows shown now are the previous view's: its columns stay with them
      // until the new view's snapshot (one epoch's metadata and rows).
      if (this.rows.size === 0) {
        this.pendingKind = null
        this.emit({ viewId: info.viewId, kind: info.kind, resync: !!info.resync, openError: null })
      } else {
        this.pendingKind = info.kind
        this.emit({ viewId: info.viewId, resync: !!info.resync, openError: null })
      }
      void this.pull()
    } catch (e) {
      if (this.disposed || gen !== this.generation) return
      if (codeOf(e) === 'schema_changed') {
        this.reopenForSchema(e)
        return
      }
      if (codeOf(e) === 'removed') this.end(errText(e))
      else this.emit({ openError: errText(e), status: { state: 'error', class: codeOf(e), message: errText(e) }, halted: true })
    }
  }

  /** The kind's columns changed: open again (its rows stay until then);
   * again and again — an error instead of a loop. */
  private reopenForSchema(e: unknown) {
    const n = ++this.schemaReopens
    if (n > MAX_RETRIES) {
      this.emit({ status: { state: 'error', class: 'schema_changed', message: errText(e) }, halted: true })
      return
    }
    if (n === 1) {
      void this.open()
      return
    }
    const gen = this.generation
    this.retryTimer = this.timers.setTimeout(() => {
      this.retryTimer = null
      if (gen === this.generation && !this.disposed) void this.open()
    }, 250 * 2 ** (n - 2))
  }

  /** The kind is no longer served: final (no rows, nothing more asked). */
  private end(message: string) {
    this.ended = true
    this.generation++ // pulls in flight are over
    this.pulling = false
    if (this.retryTimer) this.timers.clearTimeout(this.retryTimer)
    this.retryTimer = null
    this.rows = new Map()
    this.pendingKind = null
    this.emit({ rows: [], status: { state: 'error', class: 'removed', message }, halted: true })
  }

  /** view_changed for this view. */
  onChanged(p: { version?: number; gone?: boolean }) {
    if (this.disposed || this.ended) return
    if (p.gone) {
      void this.open()
      return
    }
    if (typeof p.version === 'number' && p.version > this.desired) this.desired = p.version
    void this.pull()
  }

  /** Events may have been lost (resync / reconnect): pull regardless. */
  resync() {
    if (this.disposed || this.ended) return
    this.force = true
    void this.pull()
  }

  private async pull(): Promise<void> {
    if (this.pulling || !this.viewId || this.disposed) return
    this.pulling = true
    const gen = this.generation
    const id = this.viewId
    let reopen = false
    let schema: string | null = null
    try {
      do {
        this.force = false
        const before = this.cursor
        const page = await this.client.getRows(id, this.cursor)
        if (this.disposed || gen !== this.generation) return
        if (page.status.state === 'error' && page.status.class === 'removed') {
          this.end(page.status.message ?? '')
          return
        }
        if (page.status.state === 'error' && page.status.class === 'schema_changed') {
          // Not applied: the rows of the old columns stay until the new view.
          schema = page.status.message ?? ''
          break
        }
        // Recovered only when the new view shows its rows (not a loading page).
        if (page.status.state === 'ready') this.schemaReopens = 0
        this.apply(page)
        this.retries = 0
        // A page that did not move the cursor means the announced version is
        // not reachable (never with a sane backend): stop instead of spinning.
        if (page.version <= before) this.desired = this.cursor
      } while (this.cursor < this.desired || this.force)
    } catch (e) {
      if (this.disposed || gen !== this.generation) return
      if (codeOf(e) === 'gone') {
        reopen = true
      } else if (codeOf(e) === 'removed') {
        this.end(errText(e))
      } else if (codeOf(e) === 'schema_changed') {
        schema = errText(e)
      } else if (this.retries < MAX_RETRIES) {
        const delay = 250 * 2 ** this.retries++
        this.retryTimer = this.timers.setTimeout(() => {
          this.retryTimer = null
          if (gen === this.generation) void this.pull()
        }, delay)
      } else {
        this.emit({ status: { state: 'error', class: codeOf(e), message: errText(e) }, halted: true })
      }
    } finally {
      if (gen === this.generation) this.pulling = false
    }
    if (reopen) void this.open()
    else if (schema !== null) this.reopenForSchema(new Error(schema))
  }

  private apply(p: Page) {
    if (p.reset) this.rows = new Map()
    for (const r of p.upserts) this.rows.set(r.id, r)
    for (const id of p.deleted) this.rows.delete(id)
    this.cursor = p.version
    const kind = p.reset && this.pendingKind ? { kind: this.pendingKind } : {}
    if (p.reset) this.pendingKind = null
    this.emit({ rows: Array.from(this.rows.values()), status: p.status, halted: false, ...kind })
  }

  /** Starts (or restarts after dispose — React StrictMode remounts). */
  start() {
    this.disposed = false
    void this.open()
  }

  /** Closes the backend view; late responses are ignored. Restartable. */
  dispose() {
    if (this.disposed) return
    this.disposed = true
    this.generation++ // in-flight opens/pulls become stale
    this.pulling = false
    if (this.retryTimer) this.timers.clearTimeout(this.retryTimer)
    this.retryTimer = null
    if (this.viewId) void this.client.closeView(this.viewId).catch(() => {})
    this.viewId = null
  }
}

/**
 * All open views of the page: routes view_changed events, renews leases
 * and reopens views the backend reports gone.
 */
export class ViewHub {
  private syncs = new Set<ViewSync>()
  private touchTimer: ReturnType<typeof setInterval> | null = null
  private kindsListeners = new Set<(payload?: Record<string, unknown>) => void>()

  constructor(private client: Client) {}

  /** A view object without side effects; attach() starts it. */
  prepare(provider: string, target: string, query: Query): ViewSync {
    return new ViewSync(this.client, provider, target, query)
  }

  attach(s: ViewSync) {
    this.syncs.add(s)
    if (!this.touchTimer) this.touchTimer = setInterval(() => void this.touch(), TOUCH_INTERVAL_MS)
    s.start()
  }

  create(provider: string, target: string, query: Query): ViewSync {
    const s = this.prepare(provider, target, query)
    this.attach(s)
    return s
  }

  release(s: ViewSync) {
    s.dispose()
    this.syncs.delete(s)
    if (this.syncs.size === 0 && this.touchTimer) {
      clearInterval(this.touchTimer)
      this.touchTimer = null
    }
  }

  onViewChanged(payload: Record<string, unknown> | undefined) {
    const id = payload?.viewId
    for (const s of this.syncs) {
      if (s.id && s.id === id) s.onChanged(payload as { version?: number; gone?: boolean })
    }
  }

  resyncAll() {
    for (const s of this.syncs) s.resync()
    // Catalog events may have been lost too: listeners list the kinds again.
    for (const fn of this.kindsListeners) fn(undefined)
  }

  /** kinds_changed ({provider, target, session, rev}): a hint to list the kinds again. */
  onKindsChanged(payload: Record<string, unknown> | undefined) {
    for (const fn of this.kindsListeners) fn(payload ?? {})
  }

  /** fn(payload) on kinds_changed, fn(undefined) on resync. */
  subscribeKinds(fn: (payload?: Record<string, unknown>) => void): () => void {
    this.kindsListeners.add(fn)
    return () => this.kindsListeners.delete(fn)
  }

  async touch() {
    const ids = [...this.syncs].map((s) => s.id).filter((x): x is string => !!x)
    if (!ids.length) return
    try {
      const gone = new Set(await this.client.touchViews(ids))
      for (const s of this.syncs) if (s.id && gone.has(s.id)) s.onChanged({ gone: true })
    } catch {
      // next tick retries; pulls keep leases alive meanwhile
    }
  }
}

function errText(e: unknown) {
  return e instanceof Error ? e.message : String(e)
}

function codeOf(e: unknown): string | undefined {
  return e instanceof ApiError ? e.code : undefined
}
