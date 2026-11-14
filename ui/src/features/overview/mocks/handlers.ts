import { http, HttpResponse, type HttpHandler } from 'msw'
import type { APView, ErrorBody, ErrorCode, EventPage, MutationResponse, SiteView } from '@/gen/api.gen'
import { normaliseMac } from '@/lib/mac'
import * as fx from './data'

// MSW handlers for the endpoints workstream E's screens read beyond WS-D's
// phase-0 mocks (design §4.9): AP detail, actions, history samples,
// neighbours, clients, events with filters and paging, sessions, tokens,
// migration and backups. Register them after WS-D's handlers
// (setupWorker(...restHandlers, ...screenHandlers)); the Playwright specs
// for E's screens also serve them in Node through e2e/screens/fixture.ts.

const fail = (status: number, error: string, code: ErrorCode, fields: Record<string, string> = {}) =>
  HttpResponse.json<ErrorBody>({ error, code, fields }, { status })

type State = ReturnType<typeof createState>

function createState() {
  const aps = fx.baseAPs()
  return {
    aps,
    details: new Map(aps.map((ap) => [ap.id, fx.detailFor(ap)])),
    pending: fx.basePending(),
    dismissed: [] as ReturnType<typeof fx.basePending>,
    clients: fx.baseClients(aps),
    events: fx.baseEvents(aps),
    sessions: fx.authSessions(),
    tokens: fx.tokens(),
    blocked: new Set<string>(),
    meta: new Map<string, { alias: string; notes: string }>(),
    migration: { state: 'committed' as const, schema: 2, compat: 1, finalised: false },
    revision: 44,
  }
}

let state: State = createState()

/** Fresh fixtures (each Playwright test starts from the same data). */
export function resetScreenMocks() {
  state = createState()
}

const RANGE_MS: Record<string, [number, number]> = { '1h': [3600_000, 60_000], '24h': [86400_000, 600_000], '7d': [7 * 86400_000, 3600_000] }

const mutation = <T,>(object: T, summary = 'No AP changes', aps: MutationResponse<T>['impact']['aps'] = [], dryRun = false): MutationResponse<T> => ({
  revision: dryRun ? state.revision : ++state.revision,
  object,
  impact: { aps, disruption: aps.length ? 'wlan_restart' : 'none', summary },
  warnings: [],
  ...(dryRun ? { dry_run: true } : {}),
})

export const screenHandlers: HttpHandler[] = [
  // ---- sites ----
  http.get('/api/sites/:siteId/recommendations', () => HttpResponse.json([])),
  http.get('/api/sites/:siteId/history/samples', ({ request }) => {
    const url = new URL(request.url)
    const [range, step] = RANGE_MS[url.searchParams.get('range') ?? '24h'] ?? RANGE_MS['24h']
    const ap = url.searchParams.get('ap')
    const aps = state.aps.filter((a) => !ap || a.id === ap)
    return HttpResponse.json({ samples: fx.samplesFor(aps, range, step).map((s) => ({ ...s, at: new Date(s.at).toISOString() })) })
  }),
  http.get('/api/sites/:siteId/rf/neighbours', ({ request }) => {
    const ap = new URL(request.url).searchParams.get('ap')
    return HttpResponse.json(fx.neighbours().filter((n) => !ap || n.seen_by === ap))
  }),
  http.post('/api/sites', async ({ request }) => {
    const body = (await request.json().catch(() => ({}))) as { name?: string; country?: string; timezone?: string }
    if (!body.name?.trim()) return fail(422, 'Check the highlighted fields.', 'invalid', { name: 'Enter a name.' })
    if (body.name.trim().toLowerCase() === 'home') return fail(409, 'A site with this name exists.', 'conflict', { name: 'Another site already has this name.' })
    const site = {
      id: body.name.trim().toLowerCase().replace(/[^a-z0-9]+/g, '-'),
      name: body.name.trim(),
      country: body.country ?? 'AU',
      timezone: body.timezone ?? 'UTC',
      counts: { aps: 0, aps_online: 0, clients: 0, wlans: 0, pending_aps: 0 },
      ccrev: 'AU/890',
      experimental: false,
      updated_rev: 1,
    } as SiteView // the phase-1 part; the typed settings come from F's config mocks
    return HttpResponse.json(site, { status: 201 })
  }),

  // ---- APs (pending before :apId) ----
  http.get('/api/aps/pending', ({ request }) => {
    const dismissed = new URL(request.url).searchParams.get('dismissed')
    return HttpResponse.json(dismissed ? state.dismissed : state.pending)
  }),
  http.delete('/api/aps/pending/:apId', ({ params }) => {
    const index = state.pending.findIndex((p) => p.id === params.apId)
    if (index < 0) return fail(404, 'Pending AP not found.', 'not_found')
    state.dismissed.push(...state.pending.splice(index, 1))
    return new HttpResponse(null, { status: 204 })
  }),
  http.get('/api/aps/:apId', ({ params }) => {
    const detail = state.details.get(String(params.apId))
    return detail ? HttpResponse.json(detail) : fail(404, 'AP not found.', 'not_found')
  }),
  http.get('/api/aps/:apId/telemetry', ({ params }) => {
    const detail = state.details.get(String(params.apId))
    if (!detail) return fail(404, 'AP not found.', 'not_found')
    const now = Date.now()
    return HttpResponse.json({
      kernel_log: detail.connected
        ? [
            { at: new Date(now - 3600_000).toISOString(), line: 'wl0: link up (wl0.1)' },
            { at: new Date(now - 1800_000).toISOString(), line: 'br0: port 3(wl1.1) entered forwarding state' },
          ]
        : [],
    })
  }),
  http.post('/api/aps/:apId/actions/:action', ({ params }) => {
    const detail = state.details.get(String(params.apId))
    if (!detail) return fail(404, 'AP not found.', 'not_found')
    const action = String(params.action)
    if (action === 'locate') {
      detail.locate_until = new Date(Date.now() + 5 * 60_000).toISOString()
      return HttpResponse.json({ action, state: 'accepted', until: detail.locate_until })
    }
    if (action === 'unlocate') {
      detail.locate_until = null
      return HttpResponse.json({ action, state: 'done' })
    }
    if (action === 'push-config') return HttpResponse.json({ action, state: 'accepted' })
    if (!detail.capabilities.ap_cmd) return fail(501, 'This AP’s firmware is not verified for remote commands.', 'unverified_command')
    return HttpResponse.json({ action, state: 'accepted' })
  }),
  http.patch('/api/aps/:apId', async ({ params, request }) => {
    const detail = state.details.get(String(params.apId))
    if (!detail) return fail(404, 'AP not found.', 'not_found')
    const header = request.headers.get('If-Match')
    if (header && header.replace(/"/g, '') !== String(detail.updated_rev)) {
      return HttpResponse.json({ error: 'This AP changed since you opened it.', code: 'stale', fields: {}, object: detail }, { status: 409 })
    }
    const dryRun = new URL(request.url).searchParams.get('dry_run') === '1'
    const body = (await request.json().catch(() => ({}))) as Partial<APView>
    if (body.site_id && !dryRun) {
      detail.site_id = body.site_id
      detail.updated_rev++
    }
    const impact = body.site_id ? [{ id: detail.id, name: detail.name, connected: detail.connected, push: detail.connection.push_mode, disruption: ['all_radios_bounce' as const] }] : []
    return HttpResponse.json(mutation(detail as APView, body.site_id ? `${detail.name} takes the new site’s WLANs; about ${detail.client_count} clients reconnect` : 'No AP changes', impact, dryRun))
  }),
  http.delete('/api/aps/:apId', ({ params }) => {
    const id = String(params.apId)
    if (!state.details.delete(id)) return fail(404, 'AP not found.', 'not_found')
    state.aps = state.aps.filter((a) => a.id !== id)
    return new HttpResponse(null, { status: 204 })
  }),

  // ---- clients ----
  http.get('/api/sites/:siteId/clients', ({ request }) => {
    const view = new URL(request.url).searchParams.get('view')
    if (view === 'recent') return HttpResponse.json(fx.recentSessions())
    return HttpResponse.json(state.clients.map((c) => ({ ...c, alias: state.meta.get(c.mac)?.alias || c.alias })))
  }),
  http.get('/api/clients/:mac', ({ params }) => {
    const mac = normaliseMac(String(params.mac)) ?? ''
    const detail = fx.clientDetailFor(mac, state.clients, state.events)
    if (!detail) return fail(404, 'Client not found.', 'not_found')
    const meta = state.meta.get(mac)
    return HttpResponse.json({ ...detail, meta: meta ?? { alias: '', notes: '' } })
  }),
  http.post('/api/sites/:siteId/clients/:mac/:op', ({ params, request }) => {
    const mac = String(params.mac)
    const op = String(params.op)
    const dryRun = new URL(request.url).searchParams.get('dry_run') === '1'
    if (op === 'disconnect') {
      const client = state.clients.find((c) => c.mac === mac)
      if (!client || !state.details.get(client.ap_id)?.capabilities.ap_cmd) return fail(501, 'Not verified for this firmware.', 'unverified_command')
      return HttpResponse.json({ action: 'disconnect', state: 'accepted' })
    }
    if (op !== 'block' && op !== 'unblock') return fail(404, 'Not found.', 'not_found')
    if (!dryRun) {
      if (op === 'block') state.blocked.add(mac)
      else state.blocked.delete(mac)
    }
    const aps = state.aps.filter((a) => a.connected).map((a) => ({ id: a.id, name: a.name, connected: true, push: 'reconnect' as const, disruption: ['none' as const] }))
    return HttpResponse.json(mutation({ mac, blocked: op === 'block' }, `Blocked clients change on ${aps.length} APs; no WLAN restarts`, aps, dryRun))
  }),
  http.put('/api/clients/:mac/meta', async ({ params, request }) => {
    const body = (await request.json().catch(() => ({}))) as { alias?: string; notes?: string }
    state.meta.set(String(params.mac), { alias: body.alias ?? '', notes: body.notes ?? '' })
    return HttpResponse.json({ alias: body.alias ?? '', notes: body.notes ?? '' })
  }),

  // ---- events ----
  http.get('/api/sites/:siteId/events/:eventId', ({ params }) => {
    const event = state.events.find((e) => e.id === params.eventId)
    return event ? HttpResponse.json(event) : fail(404, 'Event not found.', 'not_found')
  }),
  http.get('/api/sites/:siteId/events', ({ request }) => {
    const q = new URL(request.url).searchParams
    const limit = Math.min(500, Number(q.get('limit') ?? 50) || 50)
    const before = q.get('before')
    const since = q.get('since')
    const rows = state.events
      .filter((e) => !q.get('ap') || e.ap_id === q.get('ap'))
      .filter((e) => !q.get('client') || e.client_mac === q.get('client'))
      .filter((e) => !q.get('kind') || e.kind === q.get('kind'))
      .filter((e) => !q.get('severity') || e.severity === q.get('severity'))
      .filter((e) => !since || e.last_seen >= since)
      .filter((e) => !before || e.last_seen < before)
      .toSorted((a, b) => b.last_seen.localeCompare(a.last_seen))
    const page = rows.slice(0, limit)
    const body: EventPage = { events: page, next_before: rows.length > limit ? page[page.length - 1].last_seen : null }
    return HttpResponse.json(body)
  }),

  // ---- system ----
  http.get('/api/system/sessions', () => HttpResponse.json({ sessions: state.sessions })),
  http.delete('/api/system/sessions/:id', ({ params }) => {
    state.sessions = state.sessions.filter((s) => s.id !== params.id)
    return new HttpResponse(null, { status: 204 })
  }),
  http.post('/api/system/sessions/revoke-all', () => {
    state.sessions = []
    return new HttpResponse(null, { status: 204 })
  }),
  http.get('/api/system/tokens', () => HttpResponse.json({ tokens: state.tokens })),
  http.post('/api/system/tokens', async ({ request }) => {
    const body = (await request.json().catch(() => ({}))) as { name?: string; scope?: 'read' | 'admin' }
    if (!body.name?.trim()) return fail(422, 'Check the highlighted fields.', 'invalid', { name: 'Name the token.' })
    const id = `dsm_${String(state.tokens.length + 1).padStart(16, '0')}`
    const created = { id, name: body.name.trim(), scope: body.scope ?? 'read', created: new Date().toISOString(), last_used: null }
    state.tokens.push(created)
    return HttpResponse.json({ ...created, token: `${id}_synthetic-secret-for-tests` }, { status: 201 })
  }),
  http.delete('/api/system/tokens/:id', ({ params }) => {
    state.tokens = state.tokens.filter((t) => t.id !== params.id)
    return new HttpResponse(null, { status: 204 })
  }),
  http.get('/api/system/migration', () =>
    HttpResponse.json({
      state: state.migration.state,
      reason: '',
      at: new Date(Date.now() - 3 * 86400_000).toISOString(),
      paths: [],
      schema: state.migration.schema,
      compat: state.migration.compat,
      finalised: state.migration.finalised,
      finalise_available: false,
      finalise_after: new Date(Date.now() + 27 * 86400_000).toISOString(),
      report: {
        normaliser_version: 1,
        objects: [
          { scope: 'wlan', id: 'wlan-example', name: 'Example', lifted: { security: 'wpa2_wpa3_personal', band_steering: true, idle_timeout_sec: 3600, bssid_index: 1 }, residual: {} },
          { scope: 'site', id: 'home', name: 'Home', lifted: { 'rf.managed': true, 'rf.band_5.width': 80, 'rf.band_5.power': '17 dBm', 'rf.band_24.width': 20, 'rf.band_24.power': '12 dBm' }, residual: { Monitor: { CCRev: '' } } },
          { scope: 'ap', id: '02-aa-00-00-00-01', name: 'Hallway', lifted: { 'rf_override.5.channel': 149, 'rf_override.2.4.channel': 1 }, residual: {} },
        ],
        aps: state.aps.map((a) => ({ id: a.id, name: a.name, verified: true, local_files: 'none', bssid_index: 'measured', mismatches: [] })),
      },
    }),
  ),
  http.post('/api/system/migration/retry', () => fail(409, 'The migration has already committed.', 'conflict')),
  http.post('/api/system/migration/finalise', () => fail(409, 'Finalise is available 30 days after the migration.', 'conflict')),
  http.get(
    '/api/system/backup',
    () =>
      new HttpResponse('synthetic backup', {
        headers: { 'Content-Type': 'application/octet-stream', 'Content-Disposition': 'attachment; filename="controller-20261114.db"' },
      }),
  ),
]
