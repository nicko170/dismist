import { http, HttpResponse, delay, type HttpHandler } from 'msw'
import * as base from '@/api/mocks/data'
import type { BandProfileView } from '@/api/queries/rf'
import type { APSettingsView } from '@/api/queries/apsettings'
import type { ChangeDetail, RestoreInput, RestorePreview } from '@/api/queries/changes'
import type { WLANInput, WLANView } from '@/api/queries/networks'
import type { Recommendation } from '@/api/queries/recommendations'
import type { PlanProposal, RFProfileInput, RFProfileView } from '@/api/queries/rf'
import type { ExpertPatchView, MigrationReport, SiteConfigView, SitePatch } from '@/api/queries/sites'
import type { DiffEntry, Disruption, ErrorBody, Impact, MutationResponse } from '@/gen/api.gen'
import { checkPatch } from '../kit/expert'
import { changedPaths } from '../kit/patch'
import { provisionalOptions } from '../kit/provisional-options'
import { parseSSHKey, secretByteLength, sshFingerprint } from '../kit/validators'
import { passphraseError, isPersonal } from '@/features/wlans/security'
import * as fixtures from './config-data'
import { toDetail, type MockChange } from './config-data'

// MSW handlers for F's endpoints (design §4.8, §4.9): typed site, WLAN, RF,
// AP override, expert patch, history/restore, recommendations and migration
// mocks with dry runs, If-Match (409 stale), experimental gating and the
// secret_required restore flow. Registered ahead of the phase-1 handlers so
// the typed views win. Mock-only: never in a production build.

type State = {
  sites: Record<string, SiteConfigView>
  rf: Record<string, RFProfileView>
  networks: WLANView[]
  psk: Record<string, string>
  siteExpert: Record<string, ExpertPatchView>
  networkExpert: Record<string, ExpertPatchView>
  apExpert: Record<string, ExpertPatchView>
  ap: Record<string, Partial<APSettingsView>>
  changes: ChangeDetail[]
  recommendations: Record<string, Recommendation[]>
  revision: number
  migration: MigrationReport['state'] | null
}

function initial(): State {
  return {
    sites: { home: fixtures.siteConfig('home'), lab: fixtures.siteConfig('lab') },
    rf: { home: fixtures.homeRF(), lab: fixtures.labRF() },
    networks: fixtures.networks(),
    psk: { ...fixtures.MOCK_PSK },
    siteExpert: {},
    networkExpert: {},
    apExpert: {},
    ap: {},
    changes: fixtures.changes(),
    recommendations: structuredClone(fixtures.recommendations),
    revision: 47,
    migration: null,
  }
}

let state = initial()

/** Vitest and Playwright: start from the fixtures again. */
export function resetConfigMocks() {
  state = initial()
}

const MIGRATION_KEY = 'dismist.mock.migration'

function migrationState(): MigrationReport['state'] {
  if (state.migration) return state.migration
  try {
    const stored = typeof localStorage === 'undefined' ? null : localStorage.getItem(MIGRATION_KEY)
    if (stored === 'legacy' || stored === 'not_run') return stored
  } catch {
    // no storage
  }
  return 'committed'
}

const fail = (status: number, body: Omit<ErrorBody, 'fields'> & { fields?: Record<string, string>; current?: unknown }) =>
  HttpResponse.json({ fields: {}, ...body }, { status })
const notFound = (what: string) => fail(404, { error: `${what} not found.`, code: 'not_found' })
const signedOut = () => fail(401, { error: 'Sign in to continue.', code: 'unauthorized' })

function guard(): Response | null {
  try {
    return localStorage.getItem('dismist.mock.session') === '1' ? null : signedOut()
  } catch {
    return null
  }
}

function typedOnly(): Response | null {
  return migrationState() === 'committed'
    ? null
    : fail(503, { error: 'Typed settings are paused until the schema-2 migration commits.', code: 'migration_pending' })
}

function ifMatch(request: Request, current: { updated_rev?: number }): Response | null {
  const header = request.headers.get('If-Match')
  if (!header) return null
  const rev = Number(header.replace(/"/g, ''))
  return rev === current.updated_rev ? null : fail(409, { error: 'This changed since you opened it.', code: 'stale', current })
}

const isDryRun = (request: Request) => new URL(request.url).searchParams.get('dry_run') === '1'

function siteAPs(siteId: string) {
  return base.aps.filter((ap) => ap.site_id === siteId)
}

const disruptionText: Record<string, string> = {
  none: 'no restart',
  wlan_restart: 'restarts',
  radio_bounce: 'radio restarts',
  all_radios_bounce: 'every radio restarts',
  stranding_risk: 'may strand the AP',
}

function impactFor(siteId: string, disruption: Disruption, diff: DiffEntry[], what: string, apIds?: string[]): Impact {
  if (!diff.length) return { aps: [], disruption: 'none', summary: 'No AP changes' }
  const aps = siteAPs(siteId).filter((ap) => !apIds || apIds.includes(ap.id))
  const clients = aps.reduce((sum, ap) => sum + ap.client_count, 0)
  return {
    aps: aps.map((ap) => ({ id: ap.id, name: ap.name, connected: ap.connected, push: 'reconnect', disruption: [disruption], diff })),
    disruption,
    summary:
      disruption === 'none'
        ? `Applies to ${aps.length} AP${aps.length === 1 ? '' : 's'} · no restart · AP reconnects`
        : `${what} ${disruptionText[disruption]} on ${aps.length} AP${aps.length === 1 ? '' : 's'}; about ${clients} client${clients === 1 ? '' : 's'} reconnect`,
  }
}

function leafDiff(prefix: string, from: unknown, to: unknown, secret = new Set<string>()): DiffEntry[] {
  const read = (source: unknown, path: string) => path.split('.').reduce<unknown>((n, p) => (n as Record<string, unknown> | undefined)?.[p], source)
  return changedPaths(from, to).map((path) =>
    secret.has(path) ? { path: `${prefix}.${path}`, from: '[changed]', to: '[changed]' } : { path: `${prefix}.${path}`, from: read(from, path), to: read(to, path) },
  )
}

function record(siteId: string, summary: string, endpoint: string, objects: MockChange['objects'], impact: Impact) {
  state.revision++
  state.changes.unshift(toDetail({
    revision: state.revision,
    at: new Date().toISOString(),
    actor: { kind: 'session', name: 'admin (browser)' },
    summary,
    kind: 'config',
    affected_aps: impact.aps.map((ap) => ({ id: ap.id, name: ap.name, state: ap.connected ? 'pending' : 'pending' })),
    endpoint,
    objects,
    aps: impact.aps.map((ap) => ({ id: ap.id, name: ap.name, diff: ap.diff ?? [] })),
    restorable: true,
  }))
  void siteId
  return state.revision
}

function respond<T>(request: Request, object: T, impact: Impact, warnings: { code: string; message: string }[] = [], status = 200) {
  const body: MutationResponse<T> = { revision: state.revision, object, impact: isDryRun(request) ? impact : { ...impact, aps: impact.aps.map(({ diff: _diff, ...ap }) => ap) }, warnings }
  if (isDryRun(request)) body.dry_run = true
  return HttpResponse.json(body, { status })
}

function deepMerge<T>(target: T, patch: unknown): T {
  if (patch === null || typeof patch !== 'object' || Array.isArray(patch)) return patch as T
  const out: Record<string, unknown> = { ...(target as Record<string, unknown>) }
  for (const [key, value] of Object.entries(patch as Record<string, unknown>)) {
    if (value === undefined) continue
    out[key] = value !== null && typeof value === 'object' && !Array.isArray(value) && out[key] && typeof out[key] === 'object' ? deepMerge(out[key], value) : value
  }
  return out as T
}

// ---- experimental gating (§4.2 CheckExperimental) ----

const EXPERIMENTAL_VALUES: Record<string, (v: unknown) => boolean> = {
  security: (v) => v === 'owe' || v === 'owe_transition',
  rates: (v) => v === 'high_density' || v === 'legacy_only',
  'roaming.fast': (v) => v === 'okc' || v === '11r',
  'roaming.ap_key_cache': (v) => v === true,
  qos_class: (v) => v !== 'none' && v != null,
  rate_limit: (v) => v != null,
  min_rssi: (v) => v != null,
  'mpsk.enabled': (v) => v === true,
  'schedule.enabled': (v) => v === true,
  'broadcast.mcast_to_ucast': (v) => v === true,
  owe_ssid: (v) => Boolean(v),
}

function experimentalFields(body: Record<string, unknown>, stored: Record<string, unknown>): Record<string, string> {
  const fields: Record<string, string> = {}
  for (const [path, test] of Object.entries(EXPERIMENTAL_VALUES)) {
    const read = (src: unknown) => path.split('.').reduce<unknown>((n, p) => (n as Record<string, unknown> | undefined)?.[p], src)
    const next = read(body)
    if (next === undefined) continue
    if (test(next) && JSON.stringify(next) !== JSON.stringify(read(stored))) fields[path] = 'This value is experimental. Turn on experimental options for the site first.'
  }
  return fields
}

// ---- WLAN validation ----

function validateWlan(siteId: string, input: WLANInput, current: WLANView | null): Record<string, string> {
  const fields: Record<string, string> = {}
  const merged = { ...current, ...input } as WLANView & { psk?: string }
  const bytes = new TextEncoder().encode(merged.ssid ?? '').length
  if (!bytes || bytes > 32) fields.ssid = 'The SSID must be 1 to 32 bytes.'
  const duplicate = state.networks.find((n) => n.site_id === siteId && n.id !== current?.id && n.enabled && merged.enabled && n.ssid === merged.ssid && n.bands.some((b) => merged.bands?.includes(b)))
  if (duplicate) fields.ssid = 'Another enabled WLAN on the same band already uses this SSID.'
  if (isPersonal(merged.security)) {
    if (input.psk) {
      const message = passphraseError(merged.security, input.psk)
      if (message) fields.psk = message
    } else if (!current?.has_psk || !isPersonal(current.security)) fields.psk = 'Enter a passphrase.'
  }
  if (merged.vlan_id !== 1 && !(merged.vlan_id >= 2 && merged.vlan_id <= 4094)) fields.vlan_id = 'Enter a VLAN from 2 to 4094.'
  return fields
}

function wlanView(input: WLANInput, current: WLANView): WLANView {
  const { psk, radius, mpsk, ...rest } = input
  const next = deepMerge(current, rest) as WLANView
  if (psk) next.has_psk = true
  if (radius !== undefined) {
    next.radius = radius
      ? {
          auth: (radius.auth ?? []).map((s, i) => ({ host: s.host, port: s.port, has_secret: Boolean(s.secret) || Boolean(current.radius?.auth[i]?.has_secret) })),
          acct: (radius.acct ?? []).map((s, i) => ({ host: s.host, port: s.port, has_secret: Boolean(s.secret) || Boolean(current.radius?.acct[i]?.has_secret) })),
          mac_auth: radius.mac_auth ?? false,
          acct_interim_sec: radius.acct_interim_sec ?? 0,
          nas_id: radius.nas_id ?? '',
          nas_ip: radius.nas_ip ?? '',
          dynamic_vlan: radius.dynamic_vlan ?? null,
          reauth_sec: radius.reauth_sec ?? 0,
          reauth_on_session_timeout: radius.reauth_on_session_timeout ?? false,
          coa: radius.coa ? { ...radius.coa, servers: radius.coa.servers.map((s) => ({ host: s.host, port: s.port, has_secret: true })) } : null,
        }
      : null
  }
  if (mpsk !== undefined) {
    next.mpsk = mpsk
      ? {
          enabled: Boolean(mpsk.enabled),
          private: Boolean(mpsk.private),
          keys: (mpsk.keys ?? []).map((k, i) => ({ id: k.id || `key-${i + 1}`, name: k.name, has_passphrase: true, vlan_id: k.vlan_id ?? 0, mac: k.mac, expires: k.expires ?? '' })),
        }
      : null
  }
  next.updated_rev = current.updated_rev + 1
  return next
}

function newWlan(siteId: string, input: WLANInput): WLANView {
  const used = new Set(state.networks.filter((n) => n.site_id === siteId).map((n) => n.bssid_index))
  let index = 1
  while (used.has(index)) index++
  const template = fixtures.networks()[0]
  const fresh: WLANView = {
    ...template,
    id: `wlan-${Math.random().toString(16).slice(2, 8)}`,
    site_id: siteId,
    has_psk: false,
    band_steering: false,
    idle_timeout_sec: 1800,
    bssid_index: index,
    live: { clients: 0, aps_broadcasting: 0, aps_total: siteAPs(siteId).length },
    live_vaps: [],
    updated_rev: 0,
  }
  return wlanView(input, fresh)
}

// ---- expert patches ----

function patchLeaves(node: unknown, prefix = ''): string[] {
  if (Array.isArray(node)) return node.flatMap((child) => patchLeaves(child, `${prefix}[]`))
  if (node && typeof node === 'object') {
    return Object.entries(node as Record<string, unknown>).flatMap(([k, v]) => (k === 'Band' || k === 'Name' ? [] : patchLeaves(v, prefix ? `${prefix}.${k}` : k)))
  }
  return [prefix]
}

function shadowing(scope: 'site' | 'ap' | 'wlan', patch: Record<string, unknown>) {
  const leaves = patchLeaves(patch).map((p) => p.toLowerCase())
  const shadowed: string[] = []
  const partly: string[] = []
  for (const meta of Object.values(provisionalOptions)) {
    if (meta.scope !== scope && !meta.scope.startsWith(`${scope}.`)) continue
    const paths = meta.firmwarePaths.map((p) => p.replace(/^WLANs\[\]\./, '').replace(/\[[^\]]*\]/g, '[]').toLowerCase())
    if (!paths.length) continue
    const hit = paths.filter((p) => leaves.some((l) => l === p || l.startsWith(`${p}.`) || l.startsWith(`${p}[]`)))
    if (hit.length === paths.length) shadowed.push(meta.key)
    else if (hit.length) partly.push(meta.key)
  }
  return { shadowed, partly }
}

function putExpert(request: Request, scope: 'site' | 'ap' | 'wlan', objectId: string, store: Record<string, ExpertPatchView>, siteId: string, apIds?: string[]) {
  return async () => {
    const body = (await request.json().catch(() => ({}))) as { patch?: Record<string, unknown> }
    const patch = body.patch ?? {}
    const current = store[objectId] ?? fixtures.emptyExpert(scope, objectId)
    const stale = ifMatch(request, current)
    if (stale) return stale
    const problems = checkPatch(scope, patch).filter((p) => p.kind !== 'warning')
    if (problems.length) {
      return fail(422, {
        error: problems[0].kind === 'duplicate' ? 'Two keys fold to the same firmware key.' : 'The patch writes a path that cannot be patched here.',
        code: problems[0].kind === 'duplicate' ? 'duplicate_key' : 'forbidden_path',
        fields: Object.fromEntries(problems.map((p) => [p.pointer, p.message])),
      })
    }
    const { shadowed, partly } = shadowing(scope, patch)
    const next: ExpertPatchView = { ...current, patch, shadowed, partly_shadowed: partly, ignored_keys: Object.keys(patch).filter((k) => k === 'SiteName'), updated_rev: current.updated_rev + 1 }
    const diff = leafDiff(scope === 'wlan' ? 'WLANs[expert]' : 'Expert', current.patch, patch)
    const impact = impactFor(siteId, scope === 'wlan' ? 'wlan_restart' : 'radio_bounce', diff, scope === 'wlan' ? 'The WLAN' : 'Config', apIds)
    if (!isDryRun(request)) {
      store[objectId] = next
      record(siteId, `Expert patch for ${scope} ${objectId} changed`, `PUT ${new URL(request.url).pathname}`, [], impact)
    }
    return respond(request, next, impact)
  }
}

// ---- handlers ----

export const configHandlers: HttpHandler[] = [
  http.get('/api/system', () => guard() ?? HttpResponse.json({ ...base.system, db: { ...base.system.db, schema: 2 }, migration: { state: migrationState(), reason: migrationState() === 'legacy' ? fixtures.migrationReport('legacy').reason : '', report_available: true } })),
  http.get('/api/system/migration', () => guard() ?? HttpResponse.json(fixtures.migrationReport(migrationState()))),
  http.post('/api/system/migration/retry', ({ request }) => {
    const denied = guard()
    if (denied) return denied
    const result = fixtures.migrationReport('committed')
    if (!isDryRun(request)) state.migration = 'committed'
    return HttpResponse.json(result)
  }),
  http.post('/api/system/migration/finalise', () => fail(409, { error: 'Finalise is available 30 days after the migration committed.', code: 'conflict' })),

  // ---- sites ----
  http.get('/api/sites/:siteId', ({ params }) => {
    const site = state.sites[String(params.siteId)]
    return guard() ?? (site ? HttpResponse.json({ ...site, rf: state.rf[site.id] }) : notFound('Site'))
  }),
  http.patch('/api/sites/:siteId', async ({ request, params }) => {
    const denied = guard()
    if (denied) return denied
    const id = String(params.siteId)
    const site = state.sites[id]
    if (!site) return notFound('Site')
    const stale = ifMatch(request, site)
    if (stale) return stale
    await delay(120)
    const body = (await request.json().catch(() => ({}))) as SitePatch
    const legacyAllowed = Object.keys(body).every((k) => k === 'name' || k === 'ssh')
    if (!legacyAllowed) {
      const paused = typedOnly()
      if (paused) return paused
    }
    if (body.ssh?.keys) {
      const fields: Record<string, string> = {}
      const keys = await Promise.all(
        body.ssh.keys.map(async (line, i) => {
          const parsed = parseSSHKey(line)
          if (!parsed.ok) fields[`ssh.keys[${i}]`] = parsed.error
          return parsed.ok ? { key: parsed.key.key, comment: parsed.key.comment, type: parsed.key.type, fingerprint: await sshFingerprint(parsed.key.blob) } : null
        }),
      )
      if (Object.keys(fields).length) return fail(422, { error: 'Check the SSH keys.', code: 'invalid', fields })
      ;(body.ssh as unknown as { keys: unknown }).keys = keys
    }
    if (body.ssh?.readonly_keys) {
      ;(body.ssh as unknown as { readonly_keys: unknown }).readonly_keys = await Promise.all(
        body.ssh.readonly_keys.map(async (line) => {
          const parsed = parseSSHKey(line)
          return parsed.ok ? { key: parsed.key.key, comment: parsed.key.comment, type: parsed.key.type, fingerprint: await sshFingerprint(parsed.key.blob) } : line
        }),
      )
    }
    if (body.experimental === undefined && !site.experimental && (body.ap_defaults?.config_recovery || body.syslog?.length || body.protection?.penalty_box || (body.management?.vlan_id ?? 1) > 1)) {
      return fail(422, { error: 'This value is experimental. Turn on experimental options for the site first.', code: 'experimental_disabled' })
    }
    const { roaming, ...patch } = body
    const next = deepMerge(site, patch) as SiteConfigView
    next.updated_rev = site.updated_rev + 1
    if (patch.country) next.ccrev = patch.country === 'AU' ? 'AU/890' : null
    const dismistOnly = Object.keys(body).every((k) => ['name', 'timezone', 'experimental'].includes(k))
    const diff = dismistOnly && !roaming ? [] : leafDiff('Site', site, next).filter((d) => !d.path.endsWith('updated_rev'))
    const disruption: Disruption = patch.country ? 'all_radios_bounce' : roaming ? 'wlan_restart' : 'none'
    const impact = impactFor(id, disruption, roaming ? [{ path: 'WLANs[*].KeyCache.KeyEncryptKey', from: '[changed]', to: '[changed]' }] : diff, 'Config')
    if (!isDryRun(request)) {
      state.sites[id] = next
      const s = base.sites.find((x) => x.id === id)
      if (s && patch.name) s.name = patch.name
      record(id, `Site ${next.name}: ${Object.keys(body).join(', ')}`, `PATCH /api/sites/${id}`, [{ kind: 'site', id, name: next.name, diff: leafDiff('site', site, next).filter((d) => !d.path.endsWith('updated_rev')) }], impact)
    }
    return respond(request, { ...next, rf: state.rf[id] }, impact)
  }),
  http.delete('/api/sites/:siteId', ({ params }) => {
    const denied = guard()
    if (denied) return denied
    const id = String(params.siteId)
    const n = siteAPs(id).length
    if (n) return fail(409, { error: `Move or delete its ${n} AP${n === 1 ? '' : 's'} first.`, code: 'conflict' })
    delete state.sites[id]
    const index = base.sites.findIndex((s) => s.id === id)
    if (index >= 0) base.sites.splice(index, 1)
    return HttpResponse.json({ revision: ++state.revision, object: null, impact: { aps: [], disruption: 'none', summary: 'No AP changes' }, warnings: [] })
  }),
  http.get('/api/sites/:siteId/expert', ({ params }) => guard() ?? HttpResponse.json(state.siteExpert[String(params.siteId)] ?? fixtures.emptyExpert('site', String(params.siteId)))),
  http.put('/api/sites/:siteId/expert', ({ request, params }) => guard() ?? putExpert(request, 'site', String(params.siteId), state.siteExpert, String(params.siteId))()),

  // ---- RF ----
  http.get('/api/sites/:siteId/rf', ({ params }) => {
    const rf = state.rf[String(params.siteId)]
    return guard() ?? (rf ? HttpResponse.json({ ...rf, recommendations: state.recommendations[String(params.siteId)]?.filter((r) => r.area === 'rf') ?? [] }) : notFound('Site'))
  }),
  http.put('/api/sites/:siteId/rf', async ({ request, params }) => {
    const denied = guard() ?? typedOnly()
    if (denied) return denied
    const id = String(params.siteId)
    const rf = state.rf[id]
    if (!rf) return notFound('Site')
    const stale = ifMatch(request, rf)
    if (stale) return stale
    await delay(120)
    const body = (await request.json().catch(() => ({}))) as RFProfileInput
    const site = state.sites[id]
    const experimental = body.band_5?.advanced?.mu_mimo || body.band_5?.advanced?.txbf || body.band_24?.advanced?.mu_mimo || body.band_24?.advanced?.txbf || body.scanning_radio === false
    if (experimental && !site.experimental) return fail(422, { error: 'This value is experimental. Turn on experimental options for the site first.', code: 'experimental_disabled' })
    for (const key of ['band_24', 'band_5'] as const) {
      const p = body[key]?.power
      if (p?.mode === 'fixed' && ((p.dbm ?? 0) < 1 || (p.dbm ?? 0) > 25)) return fail(422, { error: 'Power must be 1 to 25 dBm.', code: 'firmware_rule', fields: { [`${key}.power.dbm`]: 'Enter 1 to 25 dBm.' } })
    }
    const next: RFProfileView = {
      ...rf,
      managed: body.managed ?? rf.managed,
      band_24: { ...rf.band_24, ...body.band_24, advanced: { ...rf.band_24.advanced, ...body.band_24?.advanced }, preamble: (body.band_24?.preamble ?? rf.band_24.preamble) as BandProfileView['preamble'], valid_channels: rf.band_24.valid_channels },
      band_5: { ...rf.band_5, ...body.band_5, advanced: { ...rf.band_5.advanced, ...body.band_5?.advanced }, preamble: (body.band_5?.preamble ?? rf.band_5.preamble) as BandProfileView['preamble'], valid_channels: rf.band_5.valid_channels },
      scanning_radio: body.scanning_radio === undefined ? rf.scanning_radio : body.scanning_radio,
      updated_rev: rf.updated_rev + 1,
    }
    const { plan: _p, recommendations: _r, updated_rev: _u, ...comparable } = rf
    const { plan: _p2, recommendations: _r2, updated_rev: _u2, ...nextComparable } = next
    // The Auto channel list is planner input only: never sent to the AP.
    const diff = leafDiff('Radios', comparable, nextComparable).filter((d) => !/channels/.test(d.path))
    const warnings = next.band_5.power.mode === 'fixed' && (next.band_5.power.dbm ?? 0) > 20 ? [{ code: 'power_high', message: '5 GHz power above 20 dBm' }] : []
    const impact = impactFor(id, body.managed !== rf.managed ? 'all_radios_bounce' : 'radio_bounce', diff, 'The radio')
    if (!isDryRun(request)) {
      state.rf[id] = next
      record(id, 'Radio settings changed', `PUT /api/sites/${id}/rf`, [{ kind: 'rf', id, name: state.sites[id]?.name ?? id, diff }], impact)
    }
    return respond(request, next, impact, warnings)
  }),
  http.post('/api/sites/:siteId/rf/plan', async ({ request, params }) => {
    const denied = guard() ?? typedOnly()
    if (denied) return denied
    const id = String(params.siteId)
    const rf = state.rf[id]
    if (!rf) return notFound('Site')
    await delay(200)
    const changes: PlanProposal['changes'] = rf.plan
      .filter((row) => row.mode === 'auto')
      .map((row) => {
        const target = row.band === '2.4' ? (row.ap_name === 'Study' ? 11 : 6) : row.ap_name === 'Study' ? 36 : 149
        const keep = target === row.channel
        return {
          ap_id: row.ap_id,
          ap_name: row.ap_name,
          band: row.band,
          from: { channel: row.channel, width: row.width, power_dbm: row.power_dbm, dfs_fallback: null },
          to: { channel: target, width: row.width, power_dbm: row.power_dbm, dfs_fallback: null },
          cost_before: keep ? 3.2 : 9.4,
          cost_after: 3.2,
          keep,
          reason: keep ? 'Already the lowest-cost channel' : 'Co-channel with 1 neighbour at −71 dBm',
          disruption: keep ? 'none' : 'radio_bounce',
        }
      })
    const proposal: PlanProposal = { inputs: { aps: new Set(rf.plan.map((r) => r.ap_id)).size, scans_age_s: { '02-aa-00-00-00-01': 240, '02-aa-00-00-00-03': 900, '02-aa-00-00-00-02': null } }, changes }
    if (isDryRun(request)) return respond(request, proposal, impactFor(id, 'radio_bounce', changes.filter((c) => !c.keep).map((c) => ({ path: `Radios[${c.band}].RRM.Channel`, from: c.from.channel, to: c.to.channel })), 'The radio', changes.filter((c) => !c.keep).map((c) => c.ap_id)), [])
    for (const change of changes) {
      const row = rf.plan.find((r) => r.ap_id === change.ap_id && r.band === change.band)
      if (row) Object.assign(row, { channel: change.to.channel, source: 'planner, today' })
    }
    const impact = impactFor(id, 'radio_bounce', changes.filter((c) => !c.keep).map((c) => ({ path: `Radios[${c.band}].RRM.Channel`, from: c.from.channel, to: c.to.channel })), 'The radio', changes.filter((c) => !c.keep).map((c) => c.ap_id))
    record(id, 'Applied the channel plan', `POST /api/sites/${id}/rf/plan`, [], impact)
    return respond(request, rf, impact)
  }),
  http.get('/api/sites/:siteId/rf/neighbours', ({ request }) => {
    const denied = guard()
    if (denied) return denied
    const bandFilter = new URL(request.url).searchParams.get('band')
    const view = fixtures.neighbours()
    return HttpResponse.json({ ...view, neighbours: view.neighbours.filter((n) => !bandFilter || n.band === bandFilter) })
  }),

  // ---- recommendations ----
  http.get('/api/sites/:siteId/recommendations', ({ params }) => guard() ?? HttpResponse.json(state.recommendations[String(params.siteId)] ?? [])),
  http.post('/api/sites/:siteId/recommendations/:rid/apply', async ({ request, params }) => {
    const denied = guard() ?? typedOnly()
    if (denied) return denied
    const id = String(params.siteId)
    const rec = state.recommendations[id]?.find((r) => r.id === params.rid)
    if (!rec) return notFound('Recommendation')
    const site = state.sites[id]
    const next = structuredClone(site)
    if (rec.id === 'rec-poe') next.ap_defaults.poe_constrained = 'keep_radios'
    if (rec.id === 'rec-dhcp-hostname') next.ap_defaults.hostname_from_name = true
    const diff =
      rec.id === 'rec-poe'
        ? [{ path: 'PoePwrConstrained.EnableRadios', from: false, to: true }]
        : [{ path: 'SVIs[1].DHCP.Hostname', from: undefined, to: 'hallway' }, { path: 'Hostname', from: 'Hallway', to: 'hallway' }]
    const impact = impactFor(id, 'none', diff, 'Config')
    if (!isDryRun(request)) {
      next.updated_rev++
      state.sites[id] = next
      state.recommendations[id] = state.recommendations[id].filter((r) => r.id !== rec.id)
      record(id, `Applied recommendation: ${rec.title}`, `POST /api/sites/${id}/recommendations/${rec.id}/apply`, [], impact)
    }
    return respond(request, next, impact)
  }),

  // ---- networks ----
  http.get('/api/sites/:siteId/networks', ({ params }) => guard() ?? HttpResponse.json(state.networks.filter((n) => n.site_id === params.siteId))),
  http.post('/api/sites/:siteId/networks', async ({ request, params }) => {
    const denied = guard() ?? typedOnly()
    if (denied) return denied
    const id = String(params.siteId)
    await delay(120)
    const input = (await request.json().catch(() => ({}))) as WLANInput
    const fields = validateWlan(id, input, null)
    const experimental = state.sites[id]?.experimental ? {} : experimentalFields(input as Record<string, unknown>, {})
    if (Object.keys(experimental).length) return fail(422, { error: 'Experimental options are off for this site.', code: 'experimental_disabled', fields: experimental })
    if (Object.keys(fields).length) return fail(422, { error: 'Check the highlighted fields.', code: 'invalid', fields })
    const created = newWlan(id, input)
    const impact = impactFor(id, 'wlan_restart', [{ path: `WLANs[${created.ssid}]`, from: null, to: '(added)' }], `WLAN ${created.ssid}`)
    impact.summary = `WLAN ${created.ssid} starts on ${impact.aps.length} AP${impact.aps.length === 1 ? '' : 's'}; other WLANs are not restarted`
    if (!isDryRun(request)) {
      state.networks.push(created)
      if (input.psk) state.psk[created.id] = input.psk
      record(id, `Created WLAN ${created.ssid}`, `POST /api/sites/${id}/networks`, [{ kind: 'network', id: created.id, name: created.ssid, diff: [{ path: '(created)', from: null, to: created.ssid }] }], impact)
    }
    return respond(request, created, impact, [], isDryRun(request) ? 200 : 201)
  }),
  http.get('/api/sites/:siteId/networks/:nid', ({ params }) => {
    const network = state.networks.find((n) => n.id === params.nid && n.site_id === params.siteId)
    return guard() ?? (network ? HttpResponse.json(network) : notFound('WLAN'))
  }),
  http.patch('/api/sites/:siteId/networks/:nid', async ({ request, params }) => {
    const denied = guard()
    if (denied) return denied
    const id = String(params.siteId)
    const current = state.networks.find((n) => n.id === params.nid && n.site_id === id)
    if (!current) return notFound('WLAN')
    const stale = ifMatch(request, current)
    if (stale) return stale
    await delay(120)
    const input = (await request.json().catch(() => ({}))) as WLANInput
    const legacyAllowed = Object.keys(input).every((k) => ['ssid', 'psk', 'enabled'].includes(k))
    if (!legacyAllowed) {
      const paused = typedOnly()
      if (paused) return paused
    }
    const experimental = state.sites[id]?.experimental ? {} : experimentalFields(input as Record<string, unknown>, current as unknown as Record<string, unknown>)
    if (Object.keys(experimental).length) return fail(422, { error: 'Experimental options are off for this site.', code: 'experimental_disabled', fields: experimental })
    const fields = validateWlan(id, input, current)
    if (Object.keys(fields).length) return fail(422, { error: 'Check the highlighted fields.', code: 'invalid', fields })
    const next = wlanView(input, current)
    const { live: _l, live_vaps: _v, updated_rev: _u, ...from } = current
    const { live: _l2, live_vaps: _v2, updated_rev: _u2, ...to } = next
    const diff = leafDiff(`WLANs[${current.ssid}]`, from, to)
    if (input.psk) diff.push({ path: `WLANs[${current.ssid}].Auth.PSK`, from: '[changed]', to: '[changed]' })
    const impact = impactFor(id, 'wlan_restart', diff, `WLAN ${current.ssid}`)
    const warnings = next.enabled && next.bands.length === 1 && next.bands[0] === '2.4' && next.band_steering ? [{ code: 'steer_one_band', message: 'Band steering has no effect on a single band' }] : []
    if (!isDryRun(request)) {
      state.networks = state.networks.map((n) => (n.id === current.id ? next : n))
      if (input.psk) state.psk[current.id] = input.psk
      record(id, `WLAN ${next.ssid}: ${Object.keys(input).filter((k) => k !== 'psk').join(', ') || 'passphrase changed'}`, `PATCH /api/sites/${id}/networks/${current.id}`, [{ kind: 'network', id: current.id, name: next.ssid, diff: leafDiff('', from, to) }], impact)
    }
    return respond(request, next, impact, warnings)
  }),
  http.delete('/api/sites/:siteId/networks/:nid', ({ request, params }) => {
    const denied = guard() ?? typedOnly()
    if (denied) return denied
    const id = String(params.siteId)
    const current = state.networks.find((n) => n.id === params.nid && n.site_id === id)
    if (!current) return notFound('WLAN')
    const stale = ifMatch(request, current)
    if (stale) return stale
    const impact = impactFor(id, 'wlan_restart', [{ path: `WLANs[${current.ssid}]`, from: '(present)', to: null }], `WLAN ${current.ssid}`)
    if (!isDryRun(request)) {
      state.networks = state.networks.filter((n) => n.id !== current.id)
      record(id, `Deleted WLAN ${current.ssid}`, `DELETE /api/sites/${id}/networks/${current.id}`, [], impact)
    }
    return respond(request, null, impact)
  }),
  http.get('/api/sites/:siteId/networks/:nid/expert', ({ params }) => guard() ?? HttpResponse.json(state.networkExpert[String(params.nid)] ?? fixtures.emptyExpert('wlan', String(params.nid)))),
  http.put('/api/sites/:siteId/networks/:nid/expert', ({ request, params }) => guard() ?? putExpert(request, 'wlan', String(params.nid), state.networkExpert, String(params.siteId))()),
  http.post('/api/sites/:siteId/networks/:nid/reveal', ({ params }) => {
    const denied = guard()
    if (denied) return denied
    const network = state.networks.find((n) => n.id === params.nid)
    if (!network) return notFound('WLAN')
    const psk = state.psk[network.id] ?? ''
    const esc = (v: string) => v.replace(/([\\;,:"])/g, '\\$1')
    return HttpResponse.json({ psk, wifi_qr: `WIFI:T:WPA;S:${esc(network.ssid)};P:${esc(psk)};${network.hidden ? 'H:true;' : ''};` })
  }),

  // ---- changes ----
  http.get('/api/sites/:siteId/changes', ({ request, params }) => {
    const denied = guard()
    if (denied) return denied
    const ap = new URL(request.url).searchParams.get('ap')
    const list = state.changes.filter((c) => !ap || c.affected_aps.some((a) => a.id === ap)).map(({ objects: _o, diff: _d, snapshot: _s, effective: _e, results: _r, restorable: _x, ...summary }) => summary)
    void params
    return HttpResponse.json({ changes: list, next_before: null })
  }),
  http.get('/api/sites/:siteId/changes/:rev', ({ params }) => {
    const change = state.changes.find((c) => c.revision === Number(params.rev))
    return guard() ?? (change ? HttpResponse.json(change) : notFound('Revision'))
  }),
  http.post('/api/sites/:siteId/changes/:rev/restore', async ({ request, params }) => {
    const denied = guard() ?? typedOnly()
    if (denied) return denied
    const id = String(params.siteId)
    const rev = Number(params.rev)
    const change = state.changes.find((c) => c.revision === rev)
    if (!change) return notFound('Revision')
    const body = (await request.json().catch(() => ({}))) as RestoreInput
    await delay(150)
    // Revisions before the IoT passphrase change need that passphrase (§4.11 "Secrets on restore").
    const needsSecret = rev < 46 && rev >= 45
    const secretPath = 'networks.wlan-iot.psk'
    if (needsSecret) {
      const kept = body.keep_current?.includes(secretPath) ?? false
      const value = body.secrets?.[secretPath]
      if (!kept && !value) {
        return fail(422, { error: 'Some secrets differ from today’s.', code: 'secret_required', fields: { [secretPath]: 'Passphrase for Example-IoT at rev 45 differs from today’s' } })
      }
      if (value && value !== 'old-iot-passphrase') {
        return fail(422, { error: 'That passphrase does not match the one stored at that revision.', code: 'secret_required', fields: { [secretPath]: 'Passphrase for Example-IoT at rev 45 differs from today’s (the value entered does not match)' } })
      }
    }
    const iot = state.networks.find((n) => n.id === 'wlan-iot')
    const removed = rev < 45 && iot && !body.keep_newer_wlans ? [{ kind: 'network' as const, id: iot.id, name: iot.ssid }] : []
    const preview: RestorePreview = {
      revision: rev,
      changed: rev >= 45 ? [{ kind: 'network', id: 'wlan-iot', name: 'Example-IoT' }] : [{ kind: 'site', id, name: state.sites[id]?.name ?? id }],
      recreated: [],
      removed,
      not_restored: [],
      secret_fields: body.secrets ? Object.keys(body.secrets) : [],
    }
    const diff = rev >= 47 ? [] : rev >= 45 ? [{ path: 'WLANs[Example-IoT].Vlans', from: ['30'], to: ['1'] }] : [{ path: 'WLANs[Example-IoT]', from: '(present)', to: null }]
    const impact = impactFor(id, 'wlan_restart', removed.length || rev < 47 ? diff : [], 'WLAN Example-IoT')
    if (!isDryRun(request)) record(id, `Restored revision ${rev}`, `POST /api/sites/${id}/changes/${rev}/restore`, [], impact)
    return respond(request, preview, impact)
  }),

  // ---- APs ----
  http.get('/api/aps/:apId', ({ params }) => {
    const ap = base.aps.find((a) => a.id === params.apId)
    if (!ap) return guard() ?? notFound('AP')
    return guard() ?? HttpResponse.json({ ...base.apDetail(ap), ...fixtures.apExtras(ap.id), ...state.ap[ap.id], ...pickAP(ap) })
  }),
  http.patch('/api/aps/:apId', async ({ request, params }) => {
    const denied = guard()
    if (denied) return denied
    const ap = base.aps.find((a) => a.id === params.apId)
    if (!ap) return notFound('AP')
    const stale = ifMatch(request, ap)
    if (stale) return stale
    await delay(100)
    const body = (await request.json().catch(() => ({}))) as Record<string, unknown>
    if (body.overrides) {
      const paused = typedOnly()
      if (paused) return paused
    }
    const fields: Record<string, string> = {}
    if (typeof body.secret === 'string' && secretByteLength(body.secret) !== 32) fields.secret = 'The secret must be 32 bytes, as hex or base64.'
    if (typeof body.name === 'string' && !body.name.trim()) fields.name = 'Enter a name.'
    if (Object.keys(fields).length) return fail(422, { error: 'Check the highlighted fields.', code: 'invalid', fields })
    const extras = { ...fixtures.apExtras(ap.id), ...state.ap[ap.id] }
    const overrides = body.overrides ? deepMerge(extras.overrides, body.overrides) : extras.overrides
    const features = body.protocol_features ? { ...extras.protocol_features, ...(body.protocol_features as Record<string, boolean>) } : extras.protocol_features
    const featuresAt = body.protocol_features ? { ...extras.protocol_features_at, ...Object.fromEntries(Object.keys(body.protocol_features as object).map((k) => [k, new Date().toISOString()])) } : extras.protocol_features_at
    const diff: DiffEntry[] = []
    if (typeof body.name === 'string' && body.name !== ap.name) diff.push({ path: 'Name', from: ap.name, to: body.name })
    if (body.overrides) diff.push(...leafDiff('AP', extras.overrides, overrides))
    const disruption: Disruption = body.overrides && 'rf' in (body.overrides as object) ? 'radio_bounce' : 'none'
    const impact = impactFor(ap.site_id, disruption, diff, 'The radio', [ap.id])
    const next = { ...base.apDetail(ap), ...extras, overrides, protocol_features: features, protocol_features_at: featuresAt, ...pickAP(ap) } as APSettingsView
    if (typeof body.name === 'string') next.name = body.name
    if (typeof body.management_ip === 'string') next.pinned_ip = body.management_ip
    if (typeof body.secret === 'string') next.has_secret = true
    if (!isDryRun(request)) {
      state.ap[ap.id] = { ...state.ap[ap.id], overrides, protocol_features: features, protocol_features_at: featuresAt }
      if (typeof body.name === 'string') ap.name = body.name
      if (typeof body.management_ip === 'string') ap.pinned_ip = body.management_ip
      if (typeof body.site_id === 'string') ap.site_id = body.site_id
      ap.updated_rev++
      next.updated_rev = ap.updated_rev
      if (diff.length) record(ap.site_id, `AP ${ap.name}: ${Object.keys(body).join(', ')}`, `PATCH /api/aps/${ap.id}`, [{ kind: 'ap', id: ap.id, name: ap.name, diff }], impact)
    }
    return respond(request, next, impact)
  }),
  http.get('/api/aps/:apId/expert', ({ params }) => guard() ?? HttpResponse.json(state.apExpert[String(params.apId)] ?? fixtures.emptyExpert('ap', String(params.apId)))),
  http.put('/api/aps/:apId/expert', ({ request, params }) => {
    const ap = base.aps.find((a) => a.id === params.apId)
    if (!ap) return notFound('AP')
    return guard() ?? putExpert(request, 'ap', ap.id, state.apExpert, ap.site_id, [ap.id])()
  }),
  http.get('/api/aps/:apId/config', ({ request, params }) => {
    const denied = guard()
    if (denied) return denied
    const ap = base.aps.find((a) => a.id === params.apId)
    if (!ap) return notFound('AP')
    const view = new URL(request.url).searchParams.get('view') ?? 'effective'
    const config = fixtures.effectiveConfig(ap.id, ap.name)
    if (view === 'applied') (config.LED as Record<string, number>).Brightness = 254
    return HttpResponse.json({
      ap_id: ap.id,
      view,
      revision: view === 'applied' ? ap.applied_revision : ap.config_revision,
      effective_hash: 'e1:9c1f0d8a2b7e4c11a0f5e6d7c8b9a0f1e2d3c4b5a69788',
      normaliser_version: 1,
      config,
      report: { version: 1, ignored: ['SiteName'], type_errors: [], local_files: 'none', zero_rule: true, notes: [] },
      site_patch: {},
      ap_patch: state.apExpert[ap.id]?.patch ?? {},
      h0: { recorded_at: new Date(Date.now() - 5 * 86400_000).toISOString(), config_revision: 41, normaliser_version: 1, effective_hash: 'e1:9c1f0d8a2b7e4c11a0f5e6d7c8b9a0f1e2d3c4b5a69788', matches: true },
      trace: [
        { key: 'site.ap_defaults.led_brightness', layer: 'site', shadowed: false },
        { key: 'site.rf.band_5.power', layer: 'site', shadowed: false },
        { key: 'ap.rf.channel', layer: 'ap', shadowed: false },
        { key: 'wlan.security', layer: 'wlan', shadowed: false },
      ],
    })
  }),
  http.post('/api/aps/:apId/actions/:action', ({ params, request }) => {
    const denied = guard()
    if (denied) return denied
    const ap = base.aps.find((a) => a.id === params.apId)
    if (!ap) return notFound('AP')
    if (!state.sites[ap.site_id]?.experimental && params.action === 'locate') {
      return fail(422, { error: 'Locate is experimental. Turn on experimental options for the site first.', code: 'experimental_disabled' })
    }
    if (params.action === 'locate') {
      void request
      const until = new Date(Date.now() + 5 * 60_000).toISOString()
      state.ap[ap.id] = { ...state.ap[ap.id], locate_until: until }
      return HttpResponse.json({ action: 'locate', state: 'accepted', until })
    }
    if (params.action === 'unlocate') {
      state.ap[ap.id] = { ...state.ap[ap.id], locate_until: null }
      return HttpResponse.json({ action: 'unlocate', state: 'done' })
    }
    return fail(501, { error: 'This AP command is not verified for this firmware.', code: 'unverified_command' })
  }),
]

/** The phase-1 fields the mutable AP list owns. */
function pickAP(ap: (typeof base.aps)[number]) {
  return { name: ap.name, site_id: ap.site_id, pinned_ip: ap.pinned_ip, updated_rev: ap.updated_rev }
}

declare global {
  interface Window {
    dismistConfigMock?: { reset(): void; setMigration(state: MigrationReport['state'] | null): void }
  }
}

if (typeof window !== 'undefined') {
  window.dismistConfigMock = {
    reset: resetConfigMocks,
    setMigration: (value) => {
      state.migration = value
    },
  }
}
