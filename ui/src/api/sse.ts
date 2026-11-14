import { useEffect, useSyncExternalStore } from 'react'
import { notifyManager, useQueryClient, type InfiniteData, type QueryClient } from '@tanstack/react-query'
import { notifyUnauthorized } from './client'
import { keys } from './keys'
import { probeSession, type ProbeResult } from './session'
import type { APLive, APView, ConfigEvent, EventPage, EventSummary, EventView, PushEvent, StreamMessage } from './types'

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
const EVENT_TYPES = ['snapshot', 'ap', 'event', 'config', 'push', 'pending'] as const

// ---- status store (read by the StreamIndicator and poll fallbacks) ----

let currentStatus: StreamStatus = 'idle'
const statusListeners = new Set<() => void>()

function setStatus(next: StreamStatus) {
  if (next === currentStatus) return
  currentStatus = next
  statusListeners.forEach((listener) => listener())
}

export function getStreamStatus(): StreamStatus {
  return currentStatus
}

export function useStreamStatus(): StreamStatus {
  return useSyncExternalStore(
    (listener) => {
      statusListeners.add(listener)
      return () => statusListeners.delete(listener)
    },
    getStreamStatus,
    getStreamStatus,
  )
}

/**
 * refetchInterval for queries the stream normally keeps fresh: a 30 s poll
 * while the stream is offline (down 30 s or more, or no EventSource), else off.
 */
export function useStreamFallbackInterval(): number | false {
  return useStreamStatus() === 'offline' ? 30_000 : false
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
    radios: live.radios,
    last_seen: live.seen_at || view.last_seen,
  }
}

function applyLive(qc: QueryClient, siteId: string, lives: APLive[], replace: boolean) {
  const listKey = keys.siteAPs(siteId)
  const list = qc.getQueryData<APView[]>(listKey)
  if (list) {
    const byId = new Map(lives.map((live) => [live.id, live]))
    const known = new Set(list.map((ap) => ap.id))
    const next = (replace ? list.filter((ap) => byId.has(ap.id)) : list).map((ap) => {
      const live = byId.get(ap.id)
      return live ? mergeLive(ap, live) : ap
    })
    qc.setQueryData(listKey, next)
    // An AP we have no full view for: fetch the list once.
    if (lives.some((live) => !known.has(live.id))) void qc.invalidateQueries({ queryKey: listKey, exact: true })
  }
  for (const live of lives) {
    const detail = qc.getQueryData<APView>(keys.ap(live.id))
    if (detail) qc.setQueryData(keys.ap(live.id), mergeLive(detail, live))
  }
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
  for (const apId of event.affected_aps ?? []) invalidate(keys.apConfig(apId))
}

/** Applies one batch of stream messages to the cache in a single render. */
export function applyStreamBatch(qc: QueryClient, siteId: string, batch: readonly StreamMessage[]) {
  notifyManager.batch(() => {
    for (const message of batch) {
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
          qc.setQueryData(keys.pendingCount, message.data.count)
          void qc.invalidateQueries({ queryKey: keys.pending, predicate: (q) => q.queryKey[1] !== 'count' })
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
  const owned = new Set<string>()
  for (const query of qc.getQueryCache().getAll()) {
    const [kind, id, ...rest] = query.queryKey as unknown[]
    if (rest.length || typeof id !== 'string') continue
    if (kind !== 'ap' && kind !== 'network' && kind !== 'client') continue
    const data = query.state.data as { site_id?: unknown; live?: { site_id?: unknown } } | undefined
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
  private queue: StreamMessage[] = []
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

  start() {
    if (!this.stopped) return
    this.stopped = false
    this.env.document?.addEventListener('visibilitychange', this.onVisibility)
    if (typeof EventSource === 'undefined' && !this.customSource) {
      setStatus('offline')
      return
    }
    this.connect()
  }

  stop() {
    this.stopped = true
    this.env.document?.removeEventListener('visibilitychange', this.onVisibility)
    this.close()
    for (const name of Object.keys(this.timers) as (keyof typeof this.timers)[]) this.clearTimer(name)
    this.queue = []
    setStatus('idle')
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
    if (this.downSince === null) setStatus(this.opened ? 'reconnecting' : 'connecting')
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
    setStatus('live')
    if (reopen) invalidateSite(this.qc, this.siteId)
  }

  private readonly onError = () => {
    if (this.stopped) return
    const closed = !this.source || this.source.readyState === 2 /* CLOSED */
    if (closed) this.close()
    this.markDown()
    void this.checkSession()
  }

  private markDown() {
    if (this.downSince !== null) return
    this.downSince = this.env.now()
    setStatus('reconnecting')
    this.timers.offline = this.env.setTimeout(() => {
      this.timers.offline = null
      if (!this.stopped && this.downSince !== null) setStatus('offline')
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
        setStatus('idle')
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
    const type = message.type as StreamMessage['type']
    let data: unknown
    try {
      data = JSON.parse(message.data)
    } catch {
      return
    }
    this.queue.push({ type, data } as StreamMessage)
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
