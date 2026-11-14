import { useEffect, useSyncExternalStore } from 'react'
import { notifyManager, useQueryClient, type InfiniteData, type QueryClient } from '@tanstack/react-query'
import { notifyUnauthorized } from './client'
import { keys } from './keys'
import { deriveStatus } from './live'
import { probeSession, type ProbeResult } from './session'
import type {
  APLive,
  APView,
  ConfigEvent,
  EventPage,
  EventSummary,
  EventView,
  LegacyStreamUpdate,
  PushEvent,
  SiteCounts,
  SiteSummary,
  StreamMessage,
} from './types'

// The site stream (design §4.10, §7.6). One EventSource per site, mounted by
// the site layout route. Typed events patch the query cache in batches; every
// reopen after the first invalidates the site's queries, because the server
// does not replay what was published while the stream was down.

export type StreamStatus = 'idle' | 'connecting' | 'live' | 'reconnecting' | 'offline'

export const BACKOFF_MS = [1_000, 2_000, 5_000, 10_000, 30_000] as const
export const OFFLINE_AFTER_MS = 30_000
export const HIDDEN_CLOSE_MS = 30_000
export const FLUSH_MS = 250
const PROBE_MIN_INTERVAL_MS = 5_000
/** Typed v2 events, plus the pre-v1 `update` a v0.3 controller sends. */
const EVENT_TYPES = ['snapshot', 'ap', 'event', 'config', 'push', 'pending', 'update'] as const

/** What the stream indicator shows (design §2.2): state plus enough to explain it. */
export type StreamHealth = {
  status: StreamStatus
  siteId: string | null
  /** When the current status began (ms since epoch). */
  since: number
  /** Last message of any type (ms), null before the first. */
  lastMessageAt: number | null
  /** Reopen attempts since the stream was last live. */
  attempts: number
  /** When the next scheduled reopen fires (ms), null when none is scheduled. */
  nextRetryAt: number | null
}

// ---- health store (read by the StreamIndicator and poll fallbacks) ----

const idleHealth: StreamHealth = { status: 'idle', siteId: null, since: 0, lastMessageAt: null, attempts: 0, nextRetryAt: null }
let health: StreamHealth = idleHealth
const listeners = new Set<() => void>()

function setHealth(patch: Partial<StreamHealth>, now = Date.now()) {
  const next = { ...health, ...patch }
  if (patch.status && patch.status !== health.status) next.since = now
  if (
    next.status === health.status &&
    next.siteId === health.siteId &&
    next.lastMessageAt === health.lastMessageAt &&
    next.attempts === health.attempts &&
    next.nextRetryAt === health.nextRetryAt
  ) {
    return
  }
  health = next
  listeners.forEach((listener) => listener())
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function getStreamHealth(): StreamHealth {
  return health
}

export function getStreamStatus(): StreamStatus {
  return health.status
}

export function useStreamHealth(): StreamHealth {
  return useSyncExternalStore(subscribe, getStreamHealth, getStreamHealth)
}

export function useStreamStatus(): StreamStatus {
  return useSyncExternalStore(subscribe, getStreamStatus, getStreamStatus)
}

/**
 * refetchInterval for queries the stream normally keeps fresh: a 30 s poll
 * while the stream is offline (down 30 s or more, or no EventSource), else off.
 */
export function useStreamFallbackInterval(): number | false {
  return useStreamStatus() === 'offline' ? 30_000 : false
}

// The stream the indicator controls. A holder object rather than a bare
// `let`, so start() can claim it without aliasing `this` to a variable.
const active: { stream: SiteStream | null } = { stream: null }

/** "Reconnect now" from the stream indicator: skips the remaining backoff. */
export function reconnectStream() {
  active.stream?.reconnectNow()
}

// ---- cache reducer ----

function mergeLive(view: APView, live: APLive): APView {
  return {
    ...view,
    status: live.status,
    connected: live.connected,
    applied_revision: live.applied_revision,
    config_revision: live.config_revision,
    last_error: live.last_error,
    client_count: live.client_count,
    uptime_seconds: live.uptime_seconds,
    lan_ip: live.lan_ip,
    remote_address: live.remote_address,
    behind_nat: Boolean(live.remote_address && live.lan_ip && live.remote_address !== live.lan_ip),
    radios: live.radios ?? view.radios,
    last_seen: live.seen_at && !live.seen_at.startsWith('0001-') ? live.seen_at : view.last_seen,
  }
}

/** Keeps the site's summary counts (switcher, sites list, title) in step with its AP list. */
function syncCounts(qc: QueryClient, siteId: string, list: APView[]) {
  const counts: Partial<SiteCounts> = {
    aps: list.length,
    aps_online: list.filter((ap) => ap.connected).length,
    clients: list.reduce((sum, ap) => sum + (ap.connected ? ap.client_count : 0), 0),
  }
  qc.setQueryData<SiteSummary[]>(keys.sites, (sites) =>
    sites?.map((site) => (site.id === siteId ? { ...site, counts: { ...site.counts, ...counts } } : site)),
  )
  qc.setQueryData<{ counts?: SiteCounts }>(keys.site(siteId), (site) =>
    site?.counts ? { ...site, counts: { ...site.counts, ...counts } } : site,
  )
}

function patchAPs(qc: QueryClient, siteId: string, ids: readonly string[], replace: boolean, merge: (view: APView, id: string) => APView) {
  const listKey = keys.siteAPs(siteId)
  const list = qc.getQueryData<APView[]>(listKey)
  if (list) {
    const wanted = new Set(ids)
    const known = new Set(list.map((ap) => ap.id))
    const next = (replace ? list.filter((ap) => wanted.has(ap.id)) : list).map((ap) => (wanted.has(ap.id) ? merge(ap, ap.id) : ap))
    qc.setQueryData(listKey, next)
    syncCounts(qc, siteId, next)
    // An AP we have no full view for: fetch the list once.
    if (ids.some((id) => !known.has(id))) void qc.invalidateQueries({ queryKey: listKey, exact: true })
  }
  for (const id of ids) {
    const detail = qc.getQueryData<APView>(keys.ap(id))
    if (detail) qc.setQueryData(keys.ap(id), merge(detail, id))
  }
}

function applyLive(qc: QueryClient, siteId: string, lives: APLive[], replace: boolean) {
  const byId = new Map(lives.map((live) => [live.id, live]))
  patchAPs(qc, siteId, [...byId.keys()], replace, (view, id) => mergeLive(view, byId.get(id)!))
}

function applyLegacy(qc: QueryClient, siteId: string, update: LegacyStreamUpdate) {
  const byId = new Map((update.aps ?? []).map((ap) => [ap.id, ap]))
  patchAPs(qc, siteId, [...byId.keys()], true, (view, id) => {
    const live = byId.get(id)!
    const next = { ...view, connected: live.connected, applied_revision: live.applied_revision, client_count: live.client_count }
    if (live.connected && update.observed_at) next.last_seen = update.observed_at
    return { ...next, status: deriveStatus(next) }
  })
}

function summaryToView(siteId: string, summary: EventSummary, existing?: EventView, apName = ''): EventView {
  return {
    site_id: siteId,
    ap_name: existing?.ap_name ?? apName,
    detail: existing?.detail ?? '',
    pace_type: existing?.pace_type ?? null,
    client_mac: existing?.client_mac ?? '',
    wlan_id: existing?.wlan_id ?? '',
    data: existing?.data ?? {},
    ...summary,
  }
}

function applyEvent(qc: QueryClient, siteId: string, summary: EventSummary) {
  const key = keys.siteEvents(siteId, {})
  const cached = qc.getQueryData<InfiniteData<EventPage>>(key)
  // A cached full event is now stale (count, last_seen, resolved_by).
  void qc.invalidateQueries({ queryKey: keys.siteEvent(siteId, summary.id), exact: true })
  if (!cached?.pages?.length) {
    qc.setQueryData<number>(keys.siteNewEvents(siteId), (count = 0) => count + 1)
    return
  }
  const [first, ...rest] = cached.pages
  const existing = first.events.find((event) => event.id === summary.id)
  const apName = qc.getQueryData<APView[]>(keys.siteAPs(siteId))?.find((ap) => ap.id === summary.ap_id)?.name
  const merged = summaryToView(siteId, summary, existing, apName)
  // Newest first: an upserted event moves to the top.
  const events = [merged, ...first.events.filter((event) => event.id !== summary.id)]
  qc.setQueryData<InfiniteData<EventPage>>(key, { ...cached, pages: [{ ...first, events }, ...rest] })
}

function applyConfig(qc: QueryClient, siteId: string, event: ConfigEvent) {
  const kind = event.kind.split('.')[0]
  const invalidate = (queryKey: readonly unknown[], exact = false) => void qc.invalidateQueries({ queryKey, exact })
  switch (kind) {
    case 'wlan':
    case 'network':
      invalidate(keys.siteNetworks(siteId))
      if (event.object_id) invalidate(keys.network(event.object_id))
      break
    case 'site':
      invalidate(keys.site(siteId), true)
      invalidate(keys.sites)
      invalidate(keys.siteRF(siteId))
      invalidate(keys.siteExpert(siteId))
      break
    case 'ap':
      invalidate(keys.siteAPs(siteId))
      if (event.object_id) invalidate(keys.ap(event.object_id))
      break
    case 'rf':
      invalidate(keys.siteRF(siteId))
      break
    default:
      invalidate(keys.site(siteId))
  }
  invalidate(keys.siteChanges(siteId))
  // Recommendations are computed from the config, so any change can add or retire one.
  invalidate(keys.siteRecommendations(siteId))
  for (const apId of event.affected_aps ?? []) invalidate(keys.apConfig(apId))
}

function applyPending(qc: QueryClient, count: number) {
  qc.setQueryData(keys.pendingCount, count)
  qc.setQueryData<SiteSummary[]>(keys.sites, (sites) => sites?.map((site) => ({ ...site, counts: { ...site.counts, pending_aps: count } })))
  void qc.invalidateQueries({ queryKey: keys.pending, predicate: (q) => q.queryKey[1] !== 'count' })
}

export type IncomingMessage = StreamMessage | { type: 'update'; data: LegacyStreamUpdate }

/** Applies one batch of stream messages to the cache in a single render. */
export function applyStreamBatch(qc: QueryClient, siteId: string, batch: readonly IncomingMessage[]) {
  notifyManager.batch(() => {
    for (const message of batch) {
      if (!message.data || typeof message.data !== 'object') continue
      switch (message.type) {
        case 'snapshot':
          if (message.data.site_id && message.data.site_id !== siteId) break
          applyLive(qc, siteId, message.data.aps ?? [], true)
          break
        case 'ap':
          applyLive(qc, siteId, [message.data], false)
          break
        case 'event':
          applyEvent(qc, siteId, message.data)
          break
        case 'config':
          applyConfig(qc, siteId, message.data)
          break
        case 'push':
          qc.setQueryData<Record<string, PushEvent>>(keys.sitePush(siteId), (old = {}) => ({
            ...old,
            [message.data.ap_id]: message.data,
          }))
          break
        case 'pending':
          applyPending(qc, message.data.count)
          break
        case 'update':
          if (message.data.site_id && message.data.site_id !== siteId) break
          applyLegacy(qc, siteId, message.data)
          break
      }
    }
  })
}

/**
 * Invalidates everything a site's stream keeps fresh: the ['site', id]
 * subtree plus cached AP, network and client queries whose base object
 * belongs to the site (§7.6 "Every reopen invalidates").
 */
export function invalidateSite(qc: QueryClient, siteId: string) {
  void qc.invalidateQueries({ queryKey: keys.site(siteId) })
  void qc.invalidateQueries({ queryKey: keys.pending })
  const owned = new Set<string>()
  for (const query of qc.getQueryCache().getAll()) {
    const [kind, id, ...rest] = query.queryKey as unknown[]
    if (rest.length || typeof id !== 'string') continue
    if (kind !== 'ap' && kind !== 'network' && kind !== 'client') continue
    const data = query.state.data as { site_id?: unknown } | undefined
    if (data?.site_id === siteId) owned.add(`${kind}\u0000${id}`)
  }
  if (!owned.size) return
  void qc.invalidateQueries({
    predicate: (query) => {
      const [kind, id] = query.queryKey as unknown[]
      return typeof id === 'string' && owned.has(`${String(kind)}\u0000${id}`)
    },
  })
}

// ---- connection ----

export type StreamEnv = {
  createEventSource: (url: string) => EventSource
  setTimeout: (fn: () => void, ms: number) => unknown
  clearTimeout: (handle: unknown) => void
  now: () => number
  frame: (fn: () => void) => void
  document: Pick<Document, 'visibilityState' | 'addEventListener' | 'removeEventListener'> | null
  /** Receives the browser's `online` event (network back: retry at once). */
  window: Pick<Window, 'addEventListener' | 'removeEventListener'> | null
}

export type SiteStreamOptions = {
  siteId: string
  queryClient: QueryClient
  probeSession?: () => Promise<ProbeResult>
  /** Session expired: save drafts and go to /login?next= (default: the client's 401 handler). */
  onExpired?: () => void
  env?: Partial<StreamEnv>
}

function defaultEnv(): StreamEnv {
  return {
    createEventSource: (url) => new EventSource(url, { withCredentials: true }),
    setTimeout: (fn, ms) => globalThis.setTimeout(fn, ms),
    clearTimeout: (handle) => globalThis.clearTimeout(handle as ReturnType<typeof setTimeout>),
    now: () => Date.now(),
    frame: (fn) => (typeof requestAnimationFrame === 'function' ? requestAnimationFrame(() => fn()) : fn()),
    document: typeof document === 'undefined' ? null : document,
    window: typeof window === 'undefined' ? null : window,
  }
}

export class SiteStream {
  readonly siteId: string
  private readonly qc: QueryClient
  private readonly probe: () => Promise<ProbeResult>
  private readonly onExpired: () => void
  private readonly env: StreamEnv
  private readonly customSource: boolean
  private source: EventSource | null = null
  private opened = false
  private stopped = true
  private hiddenClosed = false
  private attempt = 0
  private downSince: number | null = null
  private lastProbe = Number.NEGATIVE_INFINITY
  private probing = false
  private lastFlush = Number.NEGATIVE_INFINITY
  private queue: IncomingMessage[] = []
  private timers: Record<'reopen' | 'offline' | 'hidden' | 'flush', unknown> = {
    reopen: null,
    offline: null,
    hidden: null,
    flush: null,
  }

  constructor(options: SiteStreamOptions) {
    this.siteId = options.siteId
    this.qc = options.queryClient
    this.probe = options.probeSession ?? probeSession
    this.onExpired = options.onExpired ?? notifyUnauthorized
    this.env = { ...defaultEnv(), ...options.env }
    this.customSource = Boolean(options.env?.createEventSource)
  }

  private health(patch: Partial<StreamHealth>) {
    if (active.stream === this) setHealth(patch, this.env.now())
  }

  start() {
    if (!this.stopped) return
    this.stopped = false
    active.stream = this
    setHealth({ ...idleHealth, siteId: this.siteId }, this.env.now())
    this.env.document?.addEventListener('visibilitychange', this.onVisibility)
    this.env.window?.addEventListener('online', this.onOnline)
    if (typeof EventSource === 'undefined' && !this.customSource) {
      this.health({ status: 'offline' })
      return
    }
    this.connect()
  }

  stop() {
    this.stopped = true
    this.env.document?.removeEventListener('visibilitychange', this.onVisibility)
    this.env.window?.removeEventListener('online', this.onOnline)
    this.close()
    for (const name of Object.keys(this.timers) as (keyof typeof this.timers)[]) this.clearTimer(name)
    this.queue = []
    if (active.stream === this) {
      setHealth(idleHealth, this.env.now())
      active.stream = null
    }
  }

  /** Reconnect at once (the indicator's button, or the network coming back). */
  reconnectNow() {
    if (this.stopped) return
    if (this.source && this.source.readyState === 1 /* OPEN */) return
    this.hiddenClosed = false
    this.attempt = 0
    this.connect()
  }

  private clearTimer(name: keyof typeof this.timers) {
    if (this.timers[name] !== null) {
      this.env.clearTimeout(this.timers[name])
      this.timers[name] = null
    }
  }

  private close() {
    if (!this.source) return
    this.source.removeEventListener('open', this.onOpen)
    this.source.removeEventListener('error', this.onError)
    for (const type of EVENT_TYPES) this.source.removeEventListener(type, this.onMessage)
    this.source.close()
    this.source = null
  }

  private connect() {
    this.close()
    this.clearTimer('reopen')
    this.health({ nextRetryAt: null })
    if (this.downSince === null) this.health({ status: this.opened ? 'reconnecting' : 'connecting' })
    const source = this.env.createEventSource(`/api/sites/${encodeURIComponent(this.siteId)}/stream`)
    source.addEventListener('open', this.onOpen)
    source.addEventListener('error', this.onError)
    for (const type of EVENT_TYPES) source.addEventListener(type, this.onMessage)
    this.source = source
  }

  private readonly onOpen = () => {
    const reopen = this.opened
    this.opened = true
    this.attempt = 0
    this.downSince = null
    this.clearTimer('offline')
    this.health({ status: 'live', attempts: 0, nextRetryAt: null })
    if (reopen) invalidateSite(this.qc, this.siteId)
  }

  private readonly onError = () => {
    if (this.stopped) return
    const closed = !this.source || this.source.readyState === 2 /* CLOSED */
    if (closed) this.close()
    this.markDown()
    void this.checkSession()
  }

  private readonly onOnline = () => {
    if (this.downSince !== null || this.hiddenClosed) this.reconnectNow()
  }

  private markDown() {
    if (this.downSince !== null) return
    this.downSince = this.env.now()
    this.health({ status: 'reconnecting' })
    this.timers.offline = this.env.setTimeout(() => {
      this.timers.offline = null
      if (!this.stopped && this.downSince !== null) this.health({ status: 'offline' })
    }, OFFLINE_AFTER_MS)
  }

  private async checkSession() {
    const now = this.env.now()
    if (!this.probing && now - this.lastProbe >= PROBE_MIN_INTERVAL_MS) {
      this.probing = true
      this.lastProbe = now
      let result: ProbeResult
      try {
        result = await this.probe()
      } catch {
        result = 'unknown'
      }
      this.probing = false
      if (this.stopped) return
      if (result === 'expired') {
        this.stop()
        this.onExpired()
        return
      }
    }
    // The browser retries a dropped stream on its own; a closed one (HTTP
    // error, or our own close) needs our backoff.
    if (!this.source || this.source.readyState === 2) this.scheduleReopen()
  }

  private scheduleReopen() {
    if (this.timers.reopen !== null || this.stopped || this.hiddenClosed) return
    const delay = BACKOFF_MS[Math.min(this.attempt, BACKOFF_MS.length - 1)]
    this.attempt++
    this.health({ attempts: this.attempt, nextRetryAt: this.env.now() + delay })
    this.timers.reopen = this.env.setTimeout(() => {
      this.timers.reopen = null
      if (!this.stopped && !this.hiddenClosed) this.connect()
    }, delay)
  }

  private readonly onVisibility = () => {
    const doc = this.env.document
    if (!doc || this.stopped) return
    if (doc.visibilityState === 'hidden') {
      if (this.timers.hidden !== null) return
      this.timers.hidden = this.env.setTimeout(() => {
        this.timers.hidden = null
        this.hiddenClosed = true
        this.close()
        this.clearTimer('reopen')
        this.clearTimer('offline')
        this.downSince = null
        this.health({ status: 'idle', nextRetryAt: null })
      }, HIDDEN_CLOSE_MS)
    } else {
      this.clearTimer('hidden')
      if (this.hiddenClosed) {
        this.hiddenClosed = false
        this.connect()
      }
    }
  }

  private readonly onMessage = (event: Event) => {
    const message = event as MessageEvent<string>
    const type = message.type as IncomingMessage['type']
    let data: unknown
    try {
      data = JSON.parse(message.data)
    } catch {
      return
    }
    // The indicator shows "last update 3 s ago"; once a second is plenty.
    const now = this.env.now()
    if (now - (health.lastMessageAt ?? Number.NEGATIVE_INFINITY) >= 1_000) this.health({ lastMessageAt: now })
    this.queue.push({ type, data } as IncomingMessage)
    this.scheduleFlush()
  }

  private scheduleFlush() {
    if (this.timers.flush !== null) return
    const wait = Math.max(0, FLUSH_MS - (this.env.now() - this.lastFlush))
    this.timers.flush = this.env.setTimeout(() => {
      this.env.frame(() => {
        this.timers.flush = null
        this.lastFlush = this.env.now()
        const batch = this.queue
        this.queue = []
        if (!this.stopped && batch.length) applyStreamBatch(this.qc, this.siteId, batch)
      })
    }, wait)
  }
}

/** Mounted by the site layout route: one stream for the current site. */
export function useSiteStream(siteId: string | undefined) {
  const queryClient = useQueryClient()
  useEffect(() => {
    if (!siteId) return
    const stream = new SiteStream({ siteId, queryClient })
    stream.start()
    return () => stream.stop()
  }, [siteId, queryClient])
}
