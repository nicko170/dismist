import { http, HttpResponse, delay, type HttpHandler } from 'msw'
import { sse } from 'msw/sse'
import type { APLive, APView, ErrorBody, EventPage, MutationResponse, StreamSnapshot } from '../types'
import * as data from './data'
import { mockState } from './state'

// MSW handlers for the phase-0/1 API (design §4.9, §4.10). Every non-2xx
// body uses the {error, code, fields} contract.

const fail = (status: number, body: ErrorBody, init?: ResponseInit) => HttpResponse.json(body, { status, ...init })
const unauthorized = () => fail(401, { error: 'Sign in to continue.', code: 'unauthorized' })
const notFound = (what: string) => fail(404, { error: `${what} not found.`, code: 'not_found' })

function guard(): Response | null {
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

export const restHandlers: HttpHandler[] = [
  http.get('/auth/session', () =>
    mockState.signedIn()
      ? HttpResponse.json({ authenticated: true, expires_at: new Date(Date.now() + 12 * 3600_000).toISOString() })
      : HttpResponse.json({ authenticated: false }, { status: 401 }),
  ),
  http.post('/auth/login', async ({ request }) => {
    await delay(150)
    if (mockState.failedLogins >= 5) {
      return fail(429, { error: 'Too many attempts.', code: 'rate_limited' }, { headers: { 'Retry-After': '30' } })
    }
    const body = (await request.json().catch(() => ({}))) as { token?: string }
    if (body.token === data.MOCK_ADMIN_TOKEN || body.token === data.MOCK_API_TOKEN) {
      mockState.failedLogins = 0
      mockState.setSignedIn(true)
      return HttpResponse.json({ authenticated: true })
    }
    mockState.failedLogins++
    return fail(401, { error: 'Invalid token.', code: 'unauthorized', fields: { token: 'That token is not valid.' } })
  }),
  http.post('/auth/logout', () => {
    mockState.setSignedIn(false)
    return HttpResponse.json({ authenticated: false })
  }),

  http.get('/api/sites', () => guard() ?? HttpResponse.json(data.sites)),
  http.get('/api/sites/:siteId', ({ params }) => {
    const site = data.sites.find((s) => s.id === params.siteId)
    return guard() ?? (site ? HttpResponse.json(data.siteView(site)) : notFound('Site'))
  }),
  http.get('/api/aps', ({ request }) => {
    const site = new URL(request.url).searchParams.get('site')
    return guard() ?? HttpResponse.json(data.aps.filter((ap) => !site || ap.site_id === site))
  }),
  http.get('/api/aps/pending', () => guard() ?? HttpResponse.json(data.pending)),
  http.get('/api/aps/:apId', ({ params }) => {
    const ap = data.aps.find((a) => a.id === params.apId)
    return guard() ?? (ap ? HttpResponse.json(data.apDetail(ap)) : notFound('AP'))
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
    if (Object.keys(fields).length) return fail(400, { error: 'Check the highlighted fields.', code: 'invalid', fields })
    if (data.aps.some((a) => a.id === id)) return fail(409, { error: 'An AP with this ID is already registered.', code: 'conflict', fields: { id: 'Already registered.' } })
    const created: APView = { ...data.aps[0], id, name: body.name ?? '', site_id: body.site_id ?? '', status: 'never_connected', connected: false, client_count: 0, radios: [], last_seen: null }
    data.aps.push(created)
    const response: MutationResponse<APView> = { revision: 44, object: created, impact: { aps: [], disruption: 'none', summary: 'No AP changes' }, warnings: [] }
    return HttpResponse.json(response, { status: 201 })
  }),
  http.get('/api/sites/:siteId/events', ({ params }) => {
    const page: EventPage = { events: data.events.filter((e) => e.site_id === params.siteId), next_before: null }
    return guard() ?? HttpResponse.json(page)
  }),
  http.get('/api/sites/:siteId/clients', ({ params }) => {
    const siteAPs = new Set(data.aps.filter((a) => a.site_id === params.siteId).map((a) => a.id))
    return guard() ?? HttpResponse.json(data.clients.filter((c) => siteAPs.has(c.ap_id)))
  }),
  http.get('/api/system', () => guard() ?? HttpResponse.json(data.system)),
  http.get('/api/system/tokens', () => guard() ?? HttpResponse.json(data.tokens)),
  http.get('/api/onboarding', () => guard() ?? HttpResponse.json(data.onboarding)),
]

type StreamClient = { error(): void; close(): void }
const streamClients = new Set<StreamClient>()

/** Drops every open stream (Playwright: stream loss and reconnect). */
export function dropStreams() {
  for (const client of streamClients) client.error()
  streamClients.clear()
}

/** The site stream: a snapshot on connect, then an `ap` update every 5 s. */
export function streamHandlers(): HttpHandler[] {
  return [
    sse<{ snapshot: StreamSnapshot; ap: APLive; message: unknown }>('/api/sites/:siteId/stream', ({ client, params }) => {
      if (!mockState.signedIn()) {
        client.error()
        return
      }
      const siteId = String(params.siteId)
      const siteAPs = () => data.aps.filter((ap) => ap.site_id === siteId)
      client.send({ event: 'snapshot', data: { site_id: siteId, revision: 44, aps: siteAPs().map(toLive) } })
      streamClients.add(client)
      let tick = 0
      const timer = setInterval(() => {
        if (!streamClients.has(client)) return clearInterval(timer)
        const live = siteAPs().filter((ap) => ap.connected)
        if (!live.length) return
        const target = live[tick++ % live.length]
        target.client_count = Math.max(0, target.client_count + (tick % 2 ? 1 : -1))
        client.send({ event: 'ap', data: toLive(target) })
      }, 5000)
    }),
  ]
}
