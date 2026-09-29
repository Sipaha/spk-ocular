import { ApiError, type Client } from '../api/client'
import type { KindDescriptor, Page, Query, Row, ViewStatus } from '../api/types'

/** How often open views renew their leases (backend closes after 60 s). */
export const TOUCH_INTERVAL_MS = 20_000
const MAX_RETRIES = 5

export interface ViewState {
  kind: KindDescriptor | null
  rows: Row[]
  status: ViewStatus
  /** OpenView failed (unknown kind, target gone, ...). */
  openError: string | null
}

type Timers = { setTimeout: typeof setTimeout; clearTimeout: typeof clearTimeout }

/**
 * One live table, client side of the cursor protocol (docs/specs "Живые
 * таблицы"): one pull in flight; the cursor advances only after a page is
 * applied; pull again while the applied version is below the announced one;
 * bounded retries on failure; a "gone" view is reopened; responses of an
 * older open (generation) or after dispose are ignored, and a late OpenView
 * result after dispose is closed.
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
  private rows = new Map<string, Row>()
  private state: ViewState = { kind: null, rows: [], status: { state: 'loading' }, openError: null }
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
    const gen = ++this.generation
    this.viewId = null
    this.cursor = 0
    this.desired = 0
    try {
      const info = await this.client.openView(this.provider, this.target, this.query)
      if (this.disposed || gen !== this.generation) {
        void this.client.closeView(info.viewId).catch(() => {})
        return
      }
      this.viewId = info.viewId
      this.retries = 0
      this.emit({ kind: info.kind, openError: null })
      void this.pull()
    } catch (e) {
      if (this.disposed || gen !== this.generation) return
      this.emit({ openError: errText(e), status: { state: 'error', class: codeOf(e), message: errText(e) } })
    }
  }

  /** view_changed for this view. */
  onChanged(p: { version?: number; gone?: boolean }) {
    if (this.disposed) return
    if (p.gone) {
      void this.open()
      return
    }
    if (typeof p.version === 'number' && p.version > this.desired) this.desired = p.version
    void this.pull()
  }

  /** Events may have been lost (resync / reconnect): pull regardless. */
  resync() {
    if (this.disposed) return
    this.force = true
    void this.pull()
  }

  private async pull(): Promise<void> {
    if (this.pulling || !this.viewId || this.disposed) return
    this.pulling = true
    const gen = this.generation
    const id = this.viewId
    let reopen = false
    try {
      do {
        this.force = false
        const before = this.cursor
        const page = await this.client.getRows(id, this.cursor)
        if (this.disposed || gen !== this.generation) return
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
      } else if (this.retries < MAX_RETRIES) {
        const delay = 250 * 2 ** this.retries++
        this.retryTimer = this.timers.setTimeout(() => {
          this.retryTimer = null
          void this.pull()
        }, delay)
      } else {
        this.emit({ status: { state: 'error', class: codeOf(e), message: errText(e) } })
      }
    } finally {
      if (gen === this.generation) this.pulling = false
    }
    if (reopen) void this.open()
  }

  private apply(p: Page) {
    if (p.reset) this.rows = new Map()
    for (const r of p.upserts) this.rows.set(r.id, r)
    for (const id of p.deleted) this.rows.delete(id)
    this.cursor = p.version
    this.emit({ rows: Array.from(this.rows.values()), status: p.status })
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
