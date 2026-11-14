import { http, HttpResponse, delay, type HttpHandler } from 'msw'
import { sse } from 'msw/sse'
import type {
  APDetail,
  APLive,
  APView,
  ClientView,
  ConfigEvent,
  ErrorBody,
  ErrorCode,
  EventPage,
  EventSummary,
  EventView,
  MutationResponse,
  PendingEvent,
  PushEvent,
  SiteSummary,
  SiteView,
  StreamSnapshot,
  TokenCreated,
  TokenList,
} from '../types'
import * as config from './handlers-config'
import * as data from './data'
import { mockState } from './state'

// MSW handlers for the phase-1 API (design §4.9, §4.10), typed with the
// generated contract. Every non-2xx body uses {error, code, fields}. The
// browser build exposes controls on window.__dismistMock (see browser.ts) so
// Playwright can drop the stream, keep it failing, or push typed events.

export const fail = (status: number, error: string, code: ErrorCode, fields: Record<string, string> = {}, init?: ResponseInit) =>
  HttpResponse.json<ErrorBody>({ error, code, fields }, { status, ...init })
export const unauthorized = () => fail(401, 'Sign in to continue.', 'unauthorized')
export const notFound = (what: string) => fail(404, `${what} not found.`, 'not_found')

export function guard(): Response | null {
  return mockState.signedIn() ? null : unauthorized()
}

export function toLive(ap: APView): APLive {
  return {
    id: ap.id,
    status: ap.status,
    connected: ap.connected,
    applied_revision: ap.applied_revision,
    config_revision: ap.config_revision,
    last_error: ap.last_error,
    client_count: ap.client_count,
    uptime_seconds: ap.uptime_seconds,
    lan_ip: ap.lan_ip,
    remote_address: ap.remote_address,
    radios: ap.radios,
    seen_at: new Date().toISOString(),
  }
}

export function toSummary(event: EventView): EventSummary {
  return {
    id: event.id,
    ap_id: event.ap_id,
    kind: event.kind,
    severity: event.severity,
    title: event.title,
    first_seen: event.first_seen,
    last_seen: event.last_seen,
    count: event.count,
    resolved_by: event.resolved_by,
    has_client: Boolean(event.client_mac),
  }
}

/** Secret validation mirrors the server: 32 bytes as hex or base64. */
function secretBytes(value: string): number | null {
  const compact = value.replace(/[\s:]/g, '')
  if (/^[0-9a-fA-F]{64}$/.test(compact)) return 32
  try {
    const normalised = compact.replace(/-/g, '+').replace(/_/g, '/')
    return atob(normalised + '='.repeat((4 - (normalised.length % 4)) % 4)).length
  } catch {
    return null
  }
}

function siteCounts(site: SiteSummary): SiteSummary {
  const siteAPs = data.aps.filter((ap) => ap.site_id === site.id)
  return {
    ...site,
    counts: {
      ...site.counts,
      aps: siteAPs.length,
      aps_online: siteAPs.filter((ap) => ap.connected).length,
      clients: siteAPs.reduce((sum, ap) => sum + ap.client_count, 0),
      pending_aps: data.pending.length,
    },
  }
}

export function ifMatchFails(request: Request, rev: number): boolean {
  const header = request.headers.get('If-Match')
  return header !== null && header.replace(/"/g, '') !== String(rev)
}

function eventPage(request: Request, rows: EventView[]): EventPage {
  const params = new URL(request.url).searchParams
  const before = params.get('before')
  const limit = Math.min(500, Number(params.get('limit') ?? 50) || 50)
  const filtered = rows
    .filter((e) => !params.get('ap') || e.ap_id === params.get('ap'))
    .filter((e) => !params.get('severity') || e.severity === params.get('severity'))
    .filter((e) => !params.get('kind') || e.kind === params.get('kind'))
    .filter((e) => !params.get('client') || e.client_mac === params.get('client'))
    .filter((e) => !before || e.last_seen < before)
    .toSorted((a, b) => (a.last_seen < b.last_seen ? 1 : -1))
  const events = filtered.slice(0, limit)
  return { events, next_before: filtered.length > limit ? (events.at(-1)?.last_seen ?? null) : null }
}

let nextRevision = 44

/** The site's current config revision, and the next one for a committed mutation. */
export const revisions = {
  current: () => nextRevision - 1,
  next: () => nextRevision++,
}

export const restHandlers: HttpHandler[] = [
  http.get('/auth/session', () =>
    mockState.signedIn()
      ? HttpResponse.json({ authenticated: true, expires_at: new Date(Date.now() + 12 * 3600_000).toISOString(), via: 'session' })
      : HttpResponse.json({ authenticated: false, expires_at: null }, { status: 401 }),
  ),
  http.post('/auth/login', async ({ request }) => {
    await delay(150)
    if (mockState.failedLogins >= 5) {
      return fail(429, 'Too many attempts.', 'rate_limited', {}, { headers: { 'Retry-After': '30' } })
    }
    const body = (await request.json().catch(() => ({}))) as { token?: string }
    if (body.token === data.MOCK_ADMIN_TOKEN || body.token === data.MOCK_API_TOKEN) {
      mockState.failedLogins = 0
      mockState.setSignedIn(true)
      return HttpResponse.json({ authenticated: true })
    }
    mockState.failedLogins++
    return fail(401, 'Invalid token.', 'unauthorized', { token: 'That token is not valid.' })
  }),
  http.post('/auth/logout', () => {
    mockState.setSignedIn(false)
    return HttpResponse.json({ authenticated: false })
  }),

  // ---- sites ----
  http.get('/api/sites', () => guard() ?? HttpResponse.json(data.sites.map(siteCounts))),
  http.post('/api/sites', async ({ request }) => {
    const denied = guard()
    if (denied) return denied
    const body = (await request.json().catch(() => ({}))) as Partial<SiteView>
    const name = (body.name ?? '').trim()
    if (!name) return fail(422, 'Check the highlighted fields.', 'invalid', { name: 'Enter a site name.' })
    const id = name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') || `site-${data.sites.length + 1}`
    if (data.sites.some((s) => s.id === id)) return fail(409, 'A site with this name exists.', 'conflict', { name: 'Already used.' })
    const site: SiteSummary = {
      id,
      name,
      country: body.country ?? 'AU',
      timezone: body.timezone ?? 'Australia/Sydney',
      counts: { aps: 0, aps_online: 0, clients: 0, wlans: 0, pending_aps: 0 },
    }
    data.sites.push(site)
    return HttpResponse.json(data.siteView(site), { status: 201 })
  }),
  http.get('/api/sites/:siteId', ({ params }) => {
    const site = data.sites.find((s) => s.id === params.siteId)
    return guard() ?? (site ? HttpResponse.json(config.siteSettingsView(siteCounts(site))) : notFound('Site'))
  }),
  http.delete('/api/sites/:siteId', ({ params }) => {
    const denied = guard()
    if (denied) return denied
    const index = data.sites.findIndex((s) => s.id === params.siteId)
    if (index < 0) return notFound('Site')
    if (data.aps.some((ap) => ap.site_id === params.siteId)) {
      return fail(409, 'Move or remove this site’s APs first.', 'conflict')
    }
    data.sites.splice(index, 1)
    return new HttpResponse(null, { status: 204 })
  }),

  // ---- APs (order matters: /pending before /:apId) ----
  http.get('/api/aps', ({ request }) => {
    const site = new URL(request.url).searchParams.get('site')
    return guard() ?? HttpResponse.json(data.aps.filter((ap) => !site || ap.site_id === site))
  }),
  http.get('/api/aps/pending', () => guard() ?? HttpResponse.json(data.pending)),
  http.delete('/api/aps/pending/:apId', ({ params }) => {
    const denied = guard()
    if (denied) return denied
    const index = data.pending.findIndex((p) => p.id === params.apId)
    if (index < 0) return notFound('Pending AP')
    data.pending.splice(index, 1)
    return new HttpResponse(null, { status: 204 })
  }),
  http.get('/api/aps/:apId', ({ params }) => {
    const ap = data.aps.find((a) => a.id === params.apId)
    return guard() ?? (ap ? HttpResponse.json<APDetail>({ ...data.apDetail(ap), ...config.apExtras(ap.id) }) : notFound('AP'))
  }),
  http.get('/api/aps/:apId/clients', ({ params }) => guard() ?? HttpResponse.json(data.clients.filter((c) => c.ap_id === params.apId))),
  http.patch('/api/aps/:apId', async ({ params, request }) => {
    const denied = guard()
    if (denied) return denied
    const ap = data.aps.find((a) => a.id === params.apId)
    if (!ap) return notFound('AP')
    if (ifMatchFails(request, ap.updated_rev)) {
      return HttpResponse.json({ error: 'This AP changed since you opened it.', code: 'stale', fields: {}, object: ap }, { status: 409 })
    }
    const body = (await request.json().catch(() => ({}))) as Partial<APView> & {
      secret?: string
      management_ip?: string
      protocol_features?: Record<string, boolean>
      overrides?: Record<string, unknown>
    }
    if (body.name !== undefined && !body.name.trim()) return fail(422, 'Check the highlighted fields.', 'invalid', { name: 'Enter a name.' })
    if (body.secret !== undefined && secretBytes(body.secret) !== 32) {
      return fail(422, 'Check the highlighted fields.', 'invalid', { secret: 'The secret must be 32 bytes, as hex or base64.' })
    }
    if (body.site_id !== undefined && !data.sites.some((s) => s.id === body.site_id)) {
      return fail(422, 'Check the highlighted fields.', 'invalid', { site_id: 'Choose an existing site.' })
    }
    return config.patchAP(request, ap, body)
  }),
  http.delete('/api/aps/:apId', ({ params }) => {
    const denied = guard()
    if (denied) return denied
    const index = data.aps.findIndex((a) => a.id === params.apId)
    if (index < 0) return notFound('AP')
    data.aps.splice(index, 1)
    return new HttpResponse(null, { status: 204 })
  }),
  http.post('/api/aps', async ({ request }) => {
    const denied = guard()
    if (denied) return denied
    const body = (await request.json().catch(() => ({}))) as Record<string, string>
    const fields: Record<string, string> = {}
    const id = (body.id ?? '').trim().toLowerCase().replace(/[:.]/g, '-')
    if (!/^([0-9a-f]{2}-){5}[0-9a-f]{2}$/.test(id)) fields.id = 'Use the AP’s Ethernet MAC address.'
    if (secretBytes(body.secret ?? '') !== 32) fields.secret = 'The secret must be 32 bytes, as hex or base64.'
    if (body.management_ip && !/^\d{1,3}(\.\d{1,3}){3}$/.test(body.management_ip)) fields.management_ip = 'Enter an IPv4 address, or leave it blank.'
    if (Object.keys(fields).length) return fail(400, 'Check the highlighted fields.', 'invalid', fields)
    if (data.aps.some((a) => a.id === id)) return fail(409, 'An AP with this ID is already registered.', 'conflict', { id: 'Already registered.' })
    const created: APView = {
      ...data.aps[0],
      id,
      name: body.name ?? '',
      site_id: body.site_id ?? '',
      status: 'never_connected',
      connected: false,
      connected_at: null,
      client_count: 0,
      radios: [],
      last_seen: null,
      updated_rev: 1,
    }
    data.aps.push(created)
    const response: MutationResponse<APView> = {
      revision: nextRevision++,
      object: created,
      impact: { aps: [], disruption: 'none', summary: 'No AP changes' },
      warnings: [],
    }
    return HttpResponse.json(response, { status: 201 })
  }),

  // ---- events, clients ----
  http.get('/api/sites/:siteId/events', ({ params, request }) =>
    guard() ?? HttpResponse.json(eventPage(request, data.events.filter((e) => e.site_id === params.siteId))),
  ),
  http.get('/api/sites/:siteId/events/:eventId', ({ params }) => {
    const event = data.events.find((e) => e.site_id === params.siteId && e.id === params.eventId)
    return guard() ?? (event ? HttpResponse.json(event) : notFound('Event'))
  }),
  http.get('/api/sites/:siteId/clients', ({ params, request }) => {
    const search = new URL(request.url).searchParams
    const siteAPs = new Set(data.aps.filter((a) => a.site_id === params.siteId).map((a) => a.id))
    const rows: ClientView[] = data.clients
      .filter((c) => siteAPs.has(c.ap_id))
      .filter((c) => !search.get('ap') || c.ap_id === search.get('ap'))
      .filter((c) => !search.get('wlan') || c.wlan_id === search.get('wlan'))
    return guard() ?? HttpResponse.json(rows)
  }),
  http.get('/api/clients/:mac', ({ params }) => {
    const detail = data.clientDetail(String(params.mac))
    return guard() ?? (detail ? HttpResponse.json(detail) : notFound('Client'))
  }),

  // ---- system ----
  http.get('/api/system', () => guard() ?? HttpResponse.json(data.system)),
  http.get('/api/system/tokens', () => guard() ?? HttpResponse.json<TokenList>({ tokens: data.tokens })),
  http.post('/api/system/tokens', async ({ request }) => {
    const denied = guard()
    if (denied) return denied
    const body = (await request.json().catch(() => ({}))) as { name?: string; scope?: 'read' | 'admin' }
    if (!body.name?.trim()) return fail(422, 'Check the highlighted fields.', 'invalid', { name: 'Name the token.' })
    const created: TokenCreated = {
      id: `tok${data.tokens.length + 1}`,
      name: body.name.trim(),
      scope: body.scope ?? 'read',
      created: new Date().toISOString(),
      last_used: null,
      token: 'dsm_mock_fedcba9876543210',
    }
    data.tokens.push({ id: created.id, name: created.name, scope: created.scope, created: created.created, last_used: null })
    return HttpResponse.json(created, { status: 201 })
  }),
  http.delete('/api/system/tokens/:id', ({ params }) => {
    const denied = guard()
    if (denied) return denied
    const index = data.tokens.findIndex((t) => t.id === params.id)
    if (index < 0) return notFound('Token')
    data.tokens.splice(index, 1)
    return new HttpResponse(null, { status: 204 })
  }),
  http.get('/api/system/sessions', () => guard() ?? HttpResponse.json({ sessions: data.authSessions })),
  http.delete('/api/system/sessions/:id', ({ params }) => {
    const denied = guard()
    if (denied) return denied
    const index = data.authSessions.findIndex((s) => s.id === params.id)
    if (index < 0) return notFound('Session')
    data.authSessions.splice(index, 1)
    return new HttpResponse(null, { status: 204 })
  }),
  http.get('/api/onboarding', () => guard() ?? HttpResponse.json(data.onboarding)),
]

// ---- the site stream ----

type StreamClient = { send(message: { event: string; data: unknown }): void; error(): void; close(): void }
const streamClients = new Map<StreamClient, string>()
let failing = false

/** Drops every open stream (Playwright: stream loss and reconnect). */
export function dropStreams() {
  for (const client of streamClients.keys()) client.error()
  streamClients.clear()
}

/** While on, every stream request fails at once (the server is down). */
export function failStreams(on: boolean) {
  failing = on
  if (on) dropStreams()
}

type Emittable =
  | { type: 'snapshot'; data: StreamSnapshot }
  | { type: 'ap'; data: APLive }
  | { type: 'event'; data: EventSummary }
  | { type: 'config'; data: ConfigEvent }
  | { type: 'push'; data: PushEvent }
  | { type: 'pending'; data: PendingEvent }

/** Sends one typed event to every open stream of a site. */
export function emit(siteId: string, message: Emittable) {
  for (const [client, site] of streamClients) {
    if (site === siteId) client.send({ event: message.type, data: message.data })
  }
}

/** The site stream: a snapshot on connect, then an `ap` update every 5 s. */
export function streamHandlers(): HttpHandler[] {
  return [
    sse<{ snapshot: StreamSnapshot; ap: APLive; event: EventSummary; config: ConfigEvent; push: PushEvent; pending: PendingEvent }>(
      '/api/sites/:siteId/stream',
      ({ client, params }) => {
        if (!mockState.signedIn() || failing) {
          client.error()
          return
        }
        const siteId = String(params.siteId)
        const siteAPs = () => data.aps.filter((ap) => ap.site_id === siteId)
        client.send({ event: 'snapshot', data: { site_id: siteId, revision: nextRevision, aps: siteAPs().map(toLive) } })
        streamClients.set(client as StreamClient, siteId)
        let tick = 0
        const timer = setInterval(() => {
          if (!streamClients.has(client as StreamClient)) return clearInterval(timer)
          const live = siteAPs().filter((ap) => ap.connected)
          if (!live.length) return
          const target = live[tick++ % live.length]
          target.client_count = Math.max(0, target.client_count + (tick % 2 ? 1 : -1))
          target.last_seen = new Date().toISOString()
          client.send({ event: 'ap', data: toLive(target) })
        }, 5000)
      },
    ),
  ]
}
