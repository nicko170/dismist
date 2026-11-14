import { http, HttpResponse, delay, type HttpHandler } from 'msw'
import type {
  APOverrides,
  APView,
  ActionResult,
  ChangePage,
  ChangeRecord,
  DiffEntry,
  Disruption,
  ExpertPatchView,
  Impact,
  ImpactAP,
  MutationResponse,
  PlanProposal,
  RFProfileInput,
  RFProfileView,
  RestoreInput,
  RevealView,
  SamplesView,
  SiteSettingsInput,
  SiteSettingsView,
  SiteSummary,
  WLANInput,
  WLANView,
  Warning,
} from '../types'
import * as cfg from './config'
import * as data from './data'
import { emit, fail, guard, ifMatchFails, notFound, revisions, toLive } from './handlers'

// MSW handlers for the phase 2 to 4 endpoints (design §4.9): typed WLAN,
// site, RF and AP settings with ?dry_run=1 impact and redacted per-AP
// diffs, If-Match, expert patches, changes and restore (with the
// secret_required case), recommendations, the RF plan and neighbours,
// actions (unverified commands answer 501), reveal, client meta, history
// samples and the migration report. Committed mutations emit `config`,
// `ap` and `push` events so PushProgress and the lists move like the real
// thing.

const rank: Record<Disruption, number> = { none: 0, wlan_restart: 1, radio_bounce: 2, all_radios_bounce: 3, stranding_risk: 4 }
const worst = (list: Disruption[]) => list.reduce<Disruption>((w, d) => (rank[d] > rank[w] ? d : w), 'none')

type Change = { field: string; path: string; from: unknown; to: unknown; disruption: Disruption | null; secret?: boolean }

const isDry = (request: Request) => new URL(request.url).searchParams.get('dry_run') === '1'
const invalid = (fields: Record<string, string>, message = 'Check the highlighted fields.') => fail(422, message, 'invalid', fields)

function equal(a: unknown, b: unknown) {
  return JSON.stringify(a) === JSON.stringify(b)
}

/** Field-level changes between two flat-ish objects, one level of nesting. */
function diffObjects(before: Record<string, unknown>, after: Record<string, unknown>, describe: (field: string) => { path: string; disruption: Disruption | null; secret?: boolean }): Change[] {
  const out: Change[] = []
  for (const key of Object.keys(after)) {
    const a = before[key]
    const b = after[key]
    if (equal(a, b)) continue
    if (a && b && typeof a === 'object' && typeof b === 'object' && !Array.isArray(a) && !Array.isArray(b)) {
      for (const sub of Object.keys(b as object)) {
        const x = (a as Record<string, unknown>)[sub]
        const y = (b as Record<string, unknown>)[sub]
        if (!equal(x, y)) out.push({ field: `${key}.${sub}`, from: x, to: y, ...describe(`${key}.${sub}`) })
      }
      continue
    }
    out.push({ field: key, from: a, to: b, ...describe(key) })
  }
  return out
}

function impactFor(siteId: string, changes: Change[], dry: boolean, label: string): Impact {
  const compiled = changes.filter((c) => c.disruption !== null)
  if (!compiled.length) return { aps: [], disruption: 'none', summary: 'No AP changes' }
  const disruption = worst(compiled.map((c) => c.disruption!))
  const siteAPs = data.aps.filter((ap) => ap.site_id === siteId)
  const diff: DiffEntry[] = compiled.map((c) => ({ path: c.path, from: c.secret ? '[changed]' : c.from, to: c.secret ? '[changed]' : c.to }))
  const apsOut: ImpactAP[] = siteAPs.map((ap) => ({
    id: ap.id,
    name: ap.name,
    connected: ap.connected,
    push: 'reconnect',
    disruption: [disruption],
    ...(dry ? { diff } : {}),
  }))
  const clients = siteAPs.reduce((sum, ap) => sum + (ap.connected ? ap.client_count : 0), 0)
  const what =
    disruption === 'wlan_restart' ? `${label} restarts on ${siteAPs.length} AP${siteAPs.length === 1 ? '' : 's'}` : disruption === 'none' ? `${label} updates on ${siteAPs.length} AP${siteAPs.length === 1 ? '' : 's'}` : `Radios restart on ${siteAPs.length} AP${siteAPs.length === 1 ? '' : 's'}`
  return { aps: apsOut, disruption, summary: disruption === 'none' ? what : `${what}; about ${clients} clients reconnect` }
}

/** After a commit: bump the APs, send `config`, `ap`, and the push lifecycle. */
function commit(siteId: string, impact: Impact, kind: string, objectId: string, summary: string, diff: ChangeRecord['diff'] = []): number {
  const revision = revisions.next()
  const affected = impact.aps.map((ap) => ap.id)
  cfg.changes.unshift({
    revision,
    at: new Date().toISOString(),
    actor: { kind: 'session', name: 'admin', id: 'a1b2c3d4', remote: '198.51.100.20' },
    kind: 'config',
    summary,
    endpoint: kind,
    affected_aps: impact.aps.map((ap) => ({ id: ap.id, name: ap.name, state: ap.connected ? 'pending' : 'offline', error: '' })),
    objects: [],
    diff: [...diff, ...impact.aps.flatMap((ap) => ap.diff ?? [])],
    results: Object.fromEntries(impact.aps.map((ap) => [ap.id, { state: 'pending', at: null, error: '' }])),
    snapshot: null,
    effective: {},
    restorable: true,
  })
  emit(siteId, { type: 'config', data: { revision, kind, object_id: objectId, affected_aps: affected, actor: { kind: 'session', name: 'admin' } } })
  for (const id of affected) {
    const ap = data.aps.find((candidate) => candidate.id === id)
    if (!ap) continue
    ap.config_revision = revision
    if (!ap.connected) continue
    ap.status = 'applying'
    emit(siteId, { type: 'ap', data: toLive(ap) })
    const steps = [
      { state: 'notified' as const, after: 300 },
      { state: 'fetched' as const, after: 900 },
      { state: 'applied' as const, after: 1800 },
    ]
    for (const step of steps) {
      setTimeout(() => {
        emit(siteId, { type: 'push', data: { ap_id: id, revision, state: step.state, error: '' } })
        if (step.state === 'applied') {
          ap.applied_revision = revision
          ap.status = 'in_sync'
          const record = cfg.changes.find((c) => c.revision === revision)
          if (record) record.results[id] = { state: 'applied', at: new Date().toISOString(), error: '' }
          emit(siteId, { type: 'ap', data: toLive(ap) })
        }
      }, step.after)
    }
  }
  return revision
}

function respond<T>(request: Request, siteId: string, object: T, impact: Impact, warnings: Warning[], commitFn: () => { revision: number; object: T }) {
  if (isDry(request)) {
    const body: MutationResponse<T> = { revision: revisions.current(), object, impact, warnings, dry_run: true }
    return HttpResponse.json(body)
  }
  const result = commitFn()
  const body: MutationResponse<T> = { revision: result.revision, object: result.object, impact: { ...impact, aps: impact.aps.map(({ diff: _diff, ...ap }) => ap) }, warnings }
  void siteId
  return HttpResponse.json(body)
}

// ---- views ----

export function siteSettingsView(summary: SiteSummary): SiteSettingsView {
  const settings = cfg.settingsFor(summary.id)
  return {
    ...summary,
    ccrev: summary.country === 'AU' ? '890' : null,
    ...settings,
    rf: cfg.rfFor(summary.id),
    expert: {
      has_patch: Object.keys(cfg.expertFor('site', summary.id).patch).length > 0,
      field: 'expert',
      shadowed: cfg.expertFor('site', summary.id).shadowed,
      partly_shadowed: cfg.expertFor('site', summary.id).partly_shadowed,
      overridden_on: {},
    },
    counts: { ...summary.counts, wlans: cfg.networks.filter((n) => n.site_id === summary.id).length },
  }
}

const extras = new Map<string, { protocol_features: Record<string, boolean>; overrides: APOverrides; locate_until: string | null }>()

export function apExtras(id: string) {
  if (!extras.has(id)) {
    extras.set(id, {
      protocol_features: { subscribe_reply: false, ip_register_record: true, file_discard: false, site_rest_404: false, pong2: false, config_change_notify: false, ap_cmd: false },
      overrides: { led_brightness: null, ssh_enabled: null, rf: { '2.4': null, '5': null }, management: null, eth1: null },
      locate_until: null,
    })
  }
  return extras.get(id)!
}

export async function patchAP(request: Request, ap: APView, body: Partial<APView> & { secret?: string; management_ip?: string; protocol_features?: Record<string, boolean>; overrides?: Record<string, unknown> }) {
  const extra = apExtras(ap.id)
  const changes: Change[] = []
  if (body.overrides) {
    for (const [key, value] of Object.entries(body.overrides)) {
      const current = (extra.overrides as unknown as Record<string, unknown>)[key]
      if (equal(current, value)) continue
      const disruption: Disruption = key === 'rf' ? 'radio_bounce' : key === 'management' ? 'stranding_risk' : 'none'
      changes.push({ field: `overrides.${key}`, path: key === 'led_brightness' ? 'LED.Brightness' : key === 'ssh_enabled' ? 'SSH.Disabled' : key === 'rf' ? 'Radios' : key, from: current, to: value, disruption })
    }
  }
  const moved = body.site_id !== undefined && body.site_id !== ap.site_id
  if (moved) changes.push({ field: 'site_id', path: 'WLANs', from: ap.site_id, to: body.site_id, disruption: 'all_radios_bounce' })
  const impactAll = impactFor(ap.site_id, changes, isDry(request), 'Config')
  const impact = { ...impactAll, aps: impactAll.aps.filter((row) => row.id === ap.id) }
  const preview = { ...ap, ...(body.name !== undefined ? { name: body.name.trim() } : {}) }
  return respond(request, ap.site_id, preview, impact, [], () => {
    if (body.name !== undefined) ap.name = body.name.trim()
    if (body.site_id !== undefined) ap.site_id = body.site_id
    if (body.management_ip !== undefined) ap.pinned_ip = body.management_ip
    if (body.secret !== undefined) ap.has_secret = true
    if (body.protocol_features) Object.assign(extra.protocol_features, body.protocol_features)
    if (body.overrides) Object.assign(extra.overrides, body.overrides)
    ap.updated_rev++
    const revision = impact.aps.length ? commit(ap.site_id, impact, 'ap.updated', ap.id, `AP ${ap.name}: settings`) : revisions.current()
    return { revision, object: ap }
  })
}

// ---- WLAN rules (a client-side copy of a few server rules, for realistic errors) ----

const wlanPath: Record<string, string> = {
  ssid: 'SSID',
  enabled: 'Enabled',
  security: 'Auth.AKM',
  psk: 'Auth.PSK',
  bands: 'Bands',
  vlan_id: 'Vlans',
  hidden: 'Hidden',
  isolation: 'Isolation',
  band_steering: 'BandConfigs[2.4].Steer',
  dtim: 'DTIM.Period',
  idle_timeout_sec: 'WCInactivity.TimeoutSec',
  max_clients: 'MaxStaAllowed',
  pmf: 'Dot11w',
  'roaming.fast': 'RoamMode',
  'roaming.neighbor_report': 'WNM.NeighborReport',
  'roaming.bss_transition': 'WNM.BSSTransitionManagement.Passive',
  'roaming.bss_load': 'WNM.BSSLoad',
  group_rekey_sec: 'Auth.GroupRekeyIntervalSec',
}

function experimentalWLANFields(input: WLANInput): string[] {
  const out: string[] = []
  if (input.security === 'owe' || input.security === 'owe_transition') out.push('security')
  if (input.roaming?.fast && input.roaming.fast !== 'none') out.push('roaming.fast')
  if (input.rates === 'high_density' || input.rates === 'legacy_only') out.push('rates')
  if (input.min_rssi && Object.keys(input.min_rssi).length) out.push('min_rssi')
  if (input.rate_limit) out.push('rate_limit')
  if (input.qos_class) out.push('qos_class')
  if (input.broadcast?.mcast_to_ucast) out.push('broadcast.mcast_to_ucast')
  if (input.mpsk?.enabled) out.push('mpsk.enabled')
  if (input.schedule?.enabled) out.push('schedule')
  return out
}

function validateWLAN(siteId: string, input: WLANInput, existing: WLANView | null): Record<string, string> | Response {
  const fields: Record<string, string> = {}
  const merged = { ...existing, ...input } as WLANView & { psk?: string }
  if (input.ssid !== undefined) {
    const bytes = new TextEncoder().encode(input.ssid).length
    if (bytes < 1 || bytes > 32) fields.ssid = 'Network names are 1 to 32 bytes.'
    const clash = cfg.networks.find((n) => n.site_id === siteId && n.id !== existing?.id && n.enabled && n.ssid === input.ssid && n.bands.some((b) => (merged.bands ?? []).includes(b)))
    if (clash) fields.ssid = `${clash.ssid} is already broadcast on the same band.`
  }
  const personal = ['wpa2_personal', 'wpa2_wpa3_personal', 'wpa3_personal'].includes(merged.security)
  if (personal && !existing?.has_psk && !input.psk) fields.psk = 'Enter a passphrase.'
  if (input.psk) {
    const sae = merged.security !== 'wpa2_personal'
    if (/^[0-9a-fA-F]{64}$/.test(input.psk) && sae) fields.psk = 'WPA3 needs a passphrase of 8 to 63 characters; 64 hex digits work only for WPA2.'
    else if (!/^[0-9a-fA-F]{64}$/.test(input.psk) && (input.psk.length < 8 || input.psk.length > 63)) fields.psk = 'Use 8 to 63 characters.'
  }
  if (input.bands && !input.bands.length) fields.bands = 'Choose at least one band.'
  if (input.vlan_id !== undefined && (input.vlan_id < 1 || input.vlan_id > 4094)) fields.vlan_id = 'VLAN IDs run from 2 to 4094 (1 is untagged).'
  if (input.band_steering && (merged.bands ?? []).length < 2) fields.band_steering = 'Band steering needs both bands.'
  const site = cfg.settingsFor(siteId)
  const experimental = experimentalWLANFields(input)
  if (experimental.length && !site.experimental) {
    return fail(
      422,
      'These settings are experimental. Turn on experimental settings for this site first (Settings › Advanced).',
      'experimental_disabled',
      Object.fromEntries(experimental.map((f) => [f, 'Experimental: not yet verified on AP33 0.15.'])),
    )
  }
  return fields
}

function applyWLAN(target: WLANView, input: WLANInput) {
  const { psk, radius, mpsk, ...rest } = input
  void radius
  void mpsk
  Object.assign(target, rest)
  if (rest.roaming) target.roaming = { ...target.roaming, ...rest.roaming }
  if (rest.broadcast) target.broadcast = { ...target.broadcast, ...rest.broadcast }
  if (psk) {
    cfg.psks.set(target.id, psk)
    target.has_psk = true
  }
  if (target.security === 'open' || target.security === 'owe') target.has_psk = false
  target.experimental_in_use = experimentalWLANFields(target as WLANInput)
}

function wlanChanges(before: WLANView, input: WLANInput): Change[] {
  const after = structuredClone(before)
  applyWLAN(after, input)
  const changes = diffObjects(before as unknown as Record<string, unknown>, after as unknown as Record<string, unknown>, (field) => ({
    path: `WLANs[${before.ssid}].${wlanPath[field] ?? field}`,
    disruption: before.enabled || after.enabled ? 'wlan_restart' : null,
  })).filter((c) => !['experimental_in_use', 'has_psk', 'updated_rev'].includes(c.field))
  if (input.psk) changes.push({ field: 'psk', path: `WLANs[${before.ssid}].Auth.PSK`, from: '[changed]', to: '[changed]', disruption: before.enabled ? 'wlan_restart' : null, secret: true })
  return changes
}

// ---- RF ----

function rfChanges(before: RFProfileView, input: RFProfileInput): Change[] {
  const out: Change[] = []
  for (const band of ['band_24', 'band_5'] as const) {
    const patch = input[band]
    if (!patch) continue
    const label = band === 'band_5' ? '5' : '2.4'
    for (const [key, value] of Object.entries(patch)) {
      const from = (before[band] as unknown as Record<string, unknown>)[key]
      if (equal(from, value)) continue
      out.push({ field: `rf.${band}.${key}`, path: `Radios[${label}].${key === 'width' ? 'Width' : key === 'power' ? 'Power' : key === 'channels' ? 'RRM.Channel' : key === 'enabled' ? 'Disabled' : key}`, from, to: value, disruption: 'radio_bounce' })
    }
  }
  if (input.managed !== undefined && input.managed !== before.managed) out.push({ field: 'rf.managed', path: 'Radios', from: before.managed, to: input.managed, disruption: null })
  if (input.scanning_radio !== undefined && input.scanning_radio !== before.scanning_radio) out.push({ field: 'rf.scanning_radio', path: 'Monitor.NbrScan.Enabled', from: before.scanning_radio, to: input.scanning_radio, disruption: 'radio_bounce' })
  return out
}

function applyRF(target: RFProfileView, input: RFProfileInput) {
  if (input.managed !== undefined) target.managed = input.managed
  if (input.scanning_radio !== undefined) target.scanning_radio = input.scanning_radio
  for (const band of ['band_24', 'band_5'] as const) if (input[band]) Object.assign(target[band], input[band])
  target.updated_rev += 1
}

// ---- site ----

function siteChanges(siteId: string, input: SiteSettingsInput): Change[] {
  const summary = data.sites.find((s) => s.id === siteId)!
  const before = siteSettingsView(summary)
  const out: Change[] = []
  const describe = (field: string): { path: string; disruption: Disruption | null } => {
    if (field === 'name' || field === 'timezone' || field === 'experimental') return { path: field, disruption: null }
    if (field === 'country') return { path: 'Radios[].CCRev', disruption: 'all_radios_bounce' }
    if (field.startsWith('management')) return { path: field.replace('management.', '').toUpperCase(), disruption: 'stranding_risk' }
    if (field === 'ap_defaults.led_brightness') return { path: 'LED.Brightness', disruption: 'none' }
    if (field.startsWith('ssh')) return { path: 'SSH.AuthorizedKeys', disruption: 'none' }
    if (field.startsWith('protection')) return { path: 'BlackList', disruption: 'none' }
    return { path: field, disruption: 'none' }
  }
  const { rf, ssh, ...rest } = input
  out.push(...diffObjects(before as unknown as Record<string, unknown>, rest as Record<string, unknown>, describe))
  if (ssh) {
    const keysBefore = before.ssh.keys.map((k) => k.key)
    if (ssh.keys && !equal(keysBefore, ssh.keys)) out.push({ field: 'ssh.keys', path: 'SSH.AuthorizedKeys', from: `${keysBefore.length} keys`, to: `${ssh.keys.length} keys`, disruption: 'none' })
    if (ssh.enabled !== undefined && ssh.enabled !== before.ssh.enabled) out.push({ field: 'ssh.enabled', path: 'SSH.Disabled', from: !before.ssh.enabled, to: !ssh.enabled, disruption: 'none' })
  }
  if (rf) out.push(...rfChanges(before.rf, rf))
  return out
}

function applySite(siteId: string, input: SiteSettingsInput) {
  const summary = data.sites.find((s) => s.id === siteId)!
  const settings = cfg.settingsFor(siteId)
  const { rf, ssh, name, country, timezone, ...rest } = input
  if (name !== undefined) summary.name = name
  if (country !== undefined) summary.country = country
  if (timezone !== undefined) summary.timezone = timezone
  for (const [key, value] of Object.entries(rest)) {
    const current = (settings as Record<string, unknown>)[key]
    ;(settings as Record<string, unknown>)[key] = current && typeof current === 'object' && !Array.isArray(current) && value && typeof value === 'object' ? { ...current, ...value } : value
  }
  if (ssh) {
    if (ssh.enabled !== undefined) settings.ssh.enabled = ssh.enabled
    if (ssh.keys) settings.ssh.keys = ssh.keys.map((line) => ({ key: line, comment: line.split(/\s+/).slice(2).join(' '), type: line.split(/\s+/)[0], fingerprint: 'SHA256:(mock)' }))
  }
  if (rf) applyRF(cfg.rfFor(siteId), rf)
  settings.updated_rev++
}

// ---- handlers ----

const siteOf = (id: unknown) => data.sites.find((s) => s.id === id)

function expertHandlers(scope: ExpertPatchView['scope'], base: string, find: (params: Record<string, unknown>) => { id: string; siteId: string } | null): HttpHandler[] {
  const getResolver = ({ params }: { params: Record<string, unknown> }) => {
    const denied = guard()
    if (denied) return denied
    const target = find(params)
    return target ? HttpResponse.json(cfg.expertFor(scope, target.id)) : notFound('Object')
  }
  const putResolver = async ({ params, request }: { params: Record<string, unknown>; request: Request }) => {
    const denied = guard()
    if (denied) return denied
    const target = find(params)
    if (!target) return notFound('Object')
    const current = cfg.expertFor(scope, target.id)
    if (ifMatchFails(request, current.updated_rev)) return HttpResponse.json({ error: 'This patch changed since you opened it.', code: 'stale', fields: {}, object: current }, { status: 409 })
    const body = (await request.json().catch(() => null)) as { patch?: unknown } | null
    const patch = body && typeof body === 'object' && 'patch' in body ? body.patch : body
    if (!patch || typeof patch !== 'object' || Array.isArray(patch)) return invalid({ patch: 'The patch must be a JSON object.' })
    const forbidden = current.forbidden ?? []
    const hits = Object.keys(patch).filter((key) => forbidden.some((f) => f.toLowerCase() === key.toLowerCase()))
    if (hits.length) return fail(422, `${hits.join(', ')} cannot be set in an expert patch.`, 'forbidden_path', Object.fromEntries(hits.map((h) => [h, 'dismist manages this field.'])))
    const changes = diffObjects(current.patch, patch as Record<string, unknown>, (field) => ({ path: field, disruption: 'radio_bounce' }))
    const impact = impactFor(target.siteId, changes, isDry(request), 'Config')
    const next: ExpertPatchView = { ...current, patch: patch as Record<string, unknown>, updated_rev: current.updated_rev + 1 }
    const warnings: Warning[] = /radios/i.test(JSON.stringify(Object.keys(patch))) ? [{ code: 'shadows_typed', message: 'This patch overrides the typed radio settings (channel, width, power) on this scope.' }] : []
    return respond(request, target.siteId, next, impact, warnings, () => {
      cfg.expertPatches.set(`${scope}:${target.id}`, next)
      const revision = impact.aps.length ? commit(target.siteId, impact, `${scope}.updated`, target.id, `Expert patch (${scope})`) : revisions.current()
      return { revision, object: cfg.expertFor(scope, target.id) }
    })
  }
  const alias = base.replace(/\/expert$/, '/advanced')
  return [http.get(base, getResolver), http.put(base, putResolver), http.get(alias, getResolver), http.put(alias, putResolver)]
}

export const configHandlers: HttpHandler[] = [
  // ---- site settings ----
  http.patch('/api/sites/:siteId', async ({ params, request }) => {
    const denied = guard()
    if (denied) return denied
    const summary = siteOf(params.siteId)
    if (!summary) return notFound('Site')
    const settings = cfg.settingsFor(summary.id)
    if (ifMatchFails(request, settings.updated_rev)) return HttpResponse.json({ error: 'This site changed since you opened it.', code: 'stale', fields: {}, object: siteSettingsView(summary) }, { status: 409 })
    const input = (await request.json().catch(() => ({}))) as SiteSettingsInput
    const fields: Record<string, string> = {}
    if (input.name !== undefined && !input.name.trim()) fields.name = 'Enter a site name.'
    if (input.country !== undefined && input.country !== 'AU') fields.country = 'Only AU (AU/890) is verified on AP33 so far.'
    if (input.management?.vlan_id !== undefined && input.management.vlan_id !== 1 && !settings.experimental) {
      return fail(422, 'A tagged management VLAN is experimental. Turn on experimental settings first.', 'experimental_disabled', { 'management.vlan_id': 'Experimental.' })
    }
    if (Object.keys(fields).length) return invalid(fields)
    const changes = siteChanges(summary.id, input)
    const impact = impactFor(summary.id, changes, isDry(request), 'Config')
    const preview = siteSettingsView({ ...summary, ...(input.name ? { name: input.name } : {}) })
    return respond(request, summary.id, preview, impact, [], () => {
      applySite(summary.id, input)
      const revision = commit(summary.id, impact.aps.length ? impact : { aps: [], disruption: 'none', summary: '' }, 'site.updated', summary.id, 'Site settings', changes.map((c) => ({ object: `Site ${summary.name}`, path: c.field, from: c.from, to: c.to })))
      return { revision, object: siteSettingsView(summary) }
    })
  }),
  ...expertHandlers('site', '/api/sites/:siteId/expert', (params) => (siteOf(params.siteId) ? { id: String(params.siteId), siteId: String(params.siteId) } : null)),

  // ---- RF ----
  http.get('/api/sites/:siteId/rf', ({ params }) => guard() ?? (siteOf(params.siteId) ? HttpResponse.json(cfg.rfFor(String(params.siteId))) : notFound('Site'))),
  http.put('/api/sites/:siteId/rf', async ({ params, request }) => {
    const denied = guard()
    if (denied) return denied
    if (!siteOf(params.siteId)) return notFound('Site')
    const siteId = String(params.siteId)
    const current = cfg.rfFor(siteId)
    if (ifMatchFails(request, current.updated_rev)) return HttpResponse.json({ error: 'The RF profile changed since you opened it.', code: 'stale', fields: {}, object: current }, { status: 409 })
    const input = (await request.json().catch(() => ({}))) as RFProfileInput
    const fields: Record<string, string> = {}
    for (const band of ['band_24', 'band_5'] as const) {
      const patch = input[band]
      if (!patch) continue
      if (patch.width === 160) fields[`${band}.width`] = 'AU/890 has no 160 MHz channels on AP33.'
      if (band === 'band_24' && patch.width && patch.width !== 20) fields[`${band}.width`] = '2.4 GHz runs at 20 MHz; 40 MHz is expert only.'
      if (patch.power?.mode === 'fixed' && ((patch.power.dbm ?? 0) < 1 || (patch.power.dbm ?? 0) > 25)) fields[`${band}.power`] = 'Use 1 to 25 dBm.'
      if (patch.channels && !patch.channels.length) fields[`${band}.channels`] = 'Allow at least one channel.'
      if (band === 'band_5' && patch.channels?.some((c) => c >= 52 && c <= 144) && !(patch.dfs ?? current.band_5.dfs)) fields[`${band}.channels`] = 'Channels 52 to 144 need DFS turned on.'
    }
    if (Object.keys(fields).length) return invalid(fields)
    const changes = rfChanges(current, input)
    const impact = impactFor(siteId, changes, isDry(request), 'Radios')
    const preview = structuredClone(current)
    applyRF(preview, input)
    return respond(request, siteId, preview, impact, [], () => {
      applyRF(current, input)
      const revision = impact.aps.length ? commit(siteId, impact, 'rf.updated', siteId, 'RF profile') : revisions.current()
      return { revision, object: current }
    })
  }),
  http.post('/api/sites/:siteId/rf/plan', async ({ params, request }) => {
    const denied = guard()
    if (denied) return denied
    if (!siteOf(params.siteId)) return notFound('Site')
    const siteId = String(params.siteId)
    const rf = cfg.rfFor(siteId)
    const proposal: PlanProposal = {
      inputs: { aps: data.aps.filter((ap) => ap.site_id === siteId).length, scans_age_s: Object.fromEntries(data.aps.filter((ap) => ap.site_id === siteId).map((ap) => [ap.id, ap.connected ? 300 : null])) },
      changes: rf.plan
        .filter((row) => row.mode === 'auto')
        .map((row) => {
          const move = row.band === '5' && row.channel === 36
          return {
            ap_id: row.ap_id,
            ap_name: row.ap_name,
            band: row.band,
            from: { channel: row.channel, width: row.width, power_dbm: row.power_dbm, dfs_fallback: null },
            to: { channel: move ? 44 : row.channel, width: row.width, power_dbm: row.power_dbm, dfs_fallback: null },
            cost_before: move ? 0.62 : 0.2,
            cost_after: move ? 0.31 : 0.2,
            keep: !move,
            reason: move ? 'A neighbour network is busy on 36 (seen at −71 dBm).' : 'Within the hysteresis of the best channel.',
            disruption: move ? ('radio_bounce' as const) : ('none' as const),
          }
        }),
    }
    const moving = proposal.changes.filter((c) => !c.keep)
    const impactAll = impactFor(siteId, moving.map((c) => ({ field: 'rf_plan', path: `Radios[${c.band}].RRM.Channel`, from: c.from.channel, to: c.to.channel, disruption: 'radio_bounce' })), isDry(request), 'Radios')
    const impact = { ...impactAll, aps: impactAll.aps.filter((ap) => moving.some((c) => c.ap_id === ap.id)) }
    await delay(200)
    return respond(request, siteId, proposal, impact, [], () => {
      for (const change of moving) {
        const row = rf.plan.find((r) => r.ap_id === change.ap_id && r.band === change.band)
        if (row) Object.assign(row, { channel: change.to.channel, source: 'planner', planned_at: new Date().toISOString() })
      }
      const revision = impact.aps.length ? commit(siteId, impact, 'rf.planned', siteId, 'Channel plan applied') : revisions.current()
      return { revision, object: proposal }
    })
  }),
  http.get('/api/sites/:siteId/rf/neighbours', ({ params, request }) => {
    const band = new URL(request.url).searchParams.get('band')
    return guard() ?? HttpResponse.json(cfg.neighbours(String(params.siteId)).filter((n) => !band || n.band === band))
  }),

  // ---- recommendations ----
  http.get('/api/sites/:siteId/recommendations', ({ params }) => guard() ?? HttpResponse.json(cfg.recommendations.get(String(params.siteId)) ?? [])),
  http.post('/api/sites/:siteId/recommendations/:rid/apply', ({ params, request }) => {
    const denied = guard()
    if (denied) return denied
    const siteId = String(params.siteId)
    const list = cfg.recommendations.get(siteId) ?? []
    const rec = list.find((r) => r.id === params.rid)
    if (!rec) return notFound('Recommendation')
    const change: Change =
      rec.id === 'rec-idle-timeout'
        ? { field: 'idle_timeout_sec', path: 'WLANs[Example].WCInactivity.TimeoutSec', from: 3600, to: 1800, disruption: 'wlan_restart' }
        : { field: 'expert', path: 'Monitor.CCRev', from: '', to: 'AU/890', disruption: 'radio_bounce' }
    const impact = impactFor(siteId, [change], isDry(request), rec.scope === 'wlan' ? 'WLAN Example' : 'Radios')
    return respond(request, siteId, rec, impact, [], () => {
      if (rec.id === 'rec-idle-timeout') {
        const network = cfg.networks.find((n) => n.id === 'wlan-example')
        if (network) network.idle_timeout_sec = 1800
      } else cfg.expertPatches.delete('site:home')
      cfg.recommendations.set(siteId, list.filter((r) => r.id !== rec.id))
      return { revision: commit(siteId, impact, 'site.updated', siteId, `Recommendation: ${rec.title}`), object: rec }
    })
  }),

  // ---- networks (WLANs) ----
  http.get('/api/sites/:siteId/networks', ({ params }) => guard() ?? HttpResponse.json(cfg.networks.filter((n) => n.site_id === params.siteId))),
  http.post('/api/sites/:siteId/networks', async ({ params, request }) => {
    const denied = guard()
    if (denied) return denied
    const siteId = String(params.siteId)
    if (!siteOf(siteId)) return notFound('Site')
    const input = (await request.json().catch(() => ({}))) as WLANInput
    if (input.ssid === undefined) return invalid({ ssid: 'Enter a network name.' })
    const checked = validateWLAN(siteId, input, null)
    if (checked instanceof Response) return checked
    if (Object.keys(checked).length) return invalid(checked)
    const used = new Set(cfg.networks.filter((n) => n.site_id === siteId).map((n) => n.bssid_index))
    const index = Array.from({ length: 15 }, (_, i) => i + 1).find((i) => !used.has(i)) ?? 15
    const created: WLANView = {
      ...structuredClone(cfg.networks[0]),
      id: `wlan-${Math.random().toString(16).slice(2, 10)}`,
      site_id: siteId,
      // New WLANs get the new defaults (design §4.6).
      band_steering: false,
      idle_timeout_sec: 1800,
      bssid_index: index,
      has_psk: false,
      live: { clients: 0, aps_broadcasting: 0, aps_total: data.aps.filter((ap) => ap.site_id === siteId).length },
      expert: { has_patch: false, field: 'expert', shadowed: [], partly_shadowed: [], overridden_on: {} },
      updated_rev: 1,
    }
    applyWLAN(created, input)
    const enabled = created.enabled
    const impact = impactFor(siteId, enabled ? [{ field: 'wlan', path: `WLANs[${created.ssid}]`, from: undefined, to: { SSID: created.ssid, Bands: created.bands }, disruption: 'wlan_restart' }] : [], isDry(request), `WLAN ${created.ssid}`)
    return respond(request, siteId, created, impact, [], () => {
      cfg.networks.push(created)
      const revision = commit(siteId, impact, 'wlan.created', created.id, `WLAN ${created.ssid}: created`)
      return { revision, object: created }
    })
  }),
  http.get('/api/sites/:siteId/networks/:nid', ({ params }) => {
    const network = cfg.networks.find((n) => n.site_id === params.siteId && n.id === params.nid)
    return guard() ?? (network ? HttpResponse.json(network) : notFound('WLAN'))
  }),
  http.patch('/api/sites/:siteId/networks/:nid', async ({ params, request }) => {
    const denied = guard()
    if (denied) return denied
    const siteId = String(params.siteId)
    const network = cfg.networks.find((n) => n.site_id === siteId && n.id === params.nid)
    if (!network) return notFound('WLAN')
    if (ifMatchFails(request, network.updated_rev)) return HttpResponse.json({ error: 'This WLAN changed since you opened it.', code: 'stale', fields: {}, object: network }, { status: 409 })
    const input = (await request.json().catch(() => ({}))) as WLANInput
    const checked = validateWLAN(siteId, input, network)
    if (checked instanceof Response) return checked
    if (Object.keys(checked).length) return invalid(checked)
    const changes = wlanChanges(network, input)
    const impact = impactFor(siteId, changes, isDry(request), `WLAN ${network.ssid}`)
    const preview = structuredClone(network)
    applyWLAN(preview, input)
    return respond(request, siteId, preview, impact, [], () => {
      applyWLAN(network, input)
      network.updated_rev++
      const revision = commit(
        siteId,
        impact,
        'wlan.updated',
        network.id,
        `WLAN ${network.ssid}: ${changes.map((c) => c.field).join(', ') || 'saved'}`,
        changes.map((c) => ({ object: `WLAN ${network.ssid}`, path: c.field, from: c.secret ? 'hmac:…' : c.from, to: c.secret ? 'hmac:…' : c.to })),
      )
      return { revision, object: network }
    })
  }),
  http.delete('/api/sites/:siteId/networks/:nid', ({ params, request }) => {
    const denied = guard()
    if (denied) return denied
    const siteId = String(params.siteId)
    const index = cfg.networks.findIndex((n) => n.site_id === siteId && n.id === params.nid)
    if (index < 0) return notFound('WLAN')
    const network = cfg.networks[index]
    if (ifMatchFails(request, network.updated_rev)) return HttpResponse.json({ error: 'This WLAN changed since you opened it.', code: 'stale', fields: {}, object: network }, { status: 409 })
    const impact = impactFor(siteId, network.enabled ? [{ field: 'wlan', path: `WLANs[${network.ssid}]`, from: { SSID: network.ssid }, to: undefined, disruption: 'wlan_restart' }] : [], isDry(request), `WLAN ${network.ssid}`)
    return respond(request, siteId, network, impact, [], () => {
      cfg.networks.splice(index, 1)
      return { revision: commit(siteId, impact, 'wlan.deleted', network.id, `WLAN ${network.ssid}: deleted`), object: network }
    })
  }),
  ...expertHandlers('wlan', '/api/sites/:siteId/networks/:nid/expert', (params) => {
    const network = cfg.networks.find((n) => n.site_id === params.siteId && n.id === params.nid)
    return network ? { id: network.id, siteId: network.site_id } : null
  }),
  http.post('/api/sites/:siteId/networks/:nid/reveal', ({ params }) => {
    const denied = guard()
    if (denied) return denied
    const network = cfg.networks.find((n) => n.site_id === params.siteId && n.id === params.nid)
    if (!network) return notFound('WLAN')
    const psk = cfg.psks.get(network.id) ?? ''
    const escape = (v: string) => v.replace(/[\\;,:"]/g, (c) => `\\${c}`)
    const body: RevealView = { ssid: network.ssid, psk, wifi_qr: `WIFI:T:${psk ? 'WPA' : 'nopass'};S:${escape(network.ssid)};${psk ? `P:${escape(psk)};` : ''}${network.hidden ? 'H:true;' : ''};` }
    return HttpResponse.json(body)
  }),

  // ---- AP config, expert, actions ----
  http.get('/api/aps/:apId/config', ({ params, request }) => {
    const denied = guard()
    if (denied) return denied
    if (!data.aps.some((ap) => ap.id === params.apId)) return notFound('AP')
    const view = (new URL(request.url).searchParams.get('view') ?? 'effective') as 'effective' | 'normalised' | 'applied'
    return HttpResponse.json(cfg.apConfig(String(params.apId), view))
  }),
  ...expertHandlers('ap', '/api/aps/:apId/expert', (params) => {
    const ap = data.aps.find((a) => a.id === params.apId)
    return ap ? { id: ap.id, siteId: ap.site_id } : null
  }),
  http.get('/api/aps/:apId/telemetry', ({ params }) => {
    const ap = data.aps.find((a) => a.id === params.apId)
    if (!ap) return guard() ?? notFound('AP')
    const detail = data.apDetail(ap)
    return guard() ?? HttpResponse.json({ radios: detail.radios_detail, health: detail.health, uplink: detail.uplink, ip: detail.ip, wlans: detail.wlans_live })
  }),
  http.post('/api/aps/:apId/actions/:action', async ({ params, request }) => {
    const denied = guard()
    if (denied) return denied
    const ap = data.aps.find((a) => a.id === params.apId)
    if (!ap) return notFound('AP')
    const action = String(params.action)
    const body = (await request.json().catch(() => ({}))) as { minutes?: number }
    const extra = apExtras(ap.id)
    const settings = cfg.settingsFor(ap.site_id)
    switch (action) {
      case 'push-config': {
        const revision = commit(ap.site_id, { aps: [{ id: ap.id, name: ap.name, connected: ap.connected, push: 'reconnect', disruption: ['none'] }], disruption: 'none', summary: 'Re-send config' }, 'ap.push', ap.id, `AP ${ap.name}: config re-sent`)
        return HttpResponse.json<ActionResult>({ action, state: 'accepted', action_id: `push-${revision}` })
      }
      case 'locate': {
        if (!settings.experimental) return fail(422, 'Locate is experimental until it is verified on AP33 0.15. Turn on experimental settings for this site first.', 'experimental_disabled')
        const minutes = [1, 5, 15, 60].includes(body.minutes ?? 5) ? (body.minutes ?? 5) : 5
        extra.locate_until = new Date(Date.now() + minutes * 60_000).toISOString()
        return HttpResponse.json<ActionResult>({ action, state: 'accepted', until: extra.locate_until })
      }
      case 'unlocate':
        extra.locate_until = null
        return HttpResponse.json<ActionResult>({ action, state: 'done' })
      case 'reboot':
      case 'restart-radios':
        return fail(501, 'This AP has no verified remote command support yet. Reboot it over SSH: dismist ap33 ssh root@<ap> reboot.', 'unverified_command')
      default:
        return notFound('Action')
    }
  }),

  // ---- clients ----
  http.post('/api/sites/:siteId/clients/:mac/disconnect', () => guard() ?? fail(501, 'Disconnecting a client needs a verified ap-cmd capability, which this AP does not have yet.', 'unverified_command')),
  http.post('/api/sites/:siteId/clients/:mac/block', ({ params, request }) => {
    const denied = guard()
    if (denied) return denied
    const settings = cfg.settingsFor(String(params.siteId))
    const mac = String(params.mac)
    if (!settings.protection.blocked_clients.some((c) => c.mac === mac)) settings.protection.blocked_clients.push({ mac, note: '' })
    const impact = impactFor(String(params.siteId), [{ field: 'protection.blocked_clients', path: 'BlackList', from: null, to: mac, disruption: 'none' }], isDry(request), 'Config')
    return respond(request, String(params.siteId), settings.protection, impact, [], () => ({ revision: commit(String(params.siteId), impact, 'site.updated', String(params.siteId), `Blocked ${mac}`), object: settings.protection }))
  }),
  http.post('/api/sites/:siteId/clients/:mac/unblock', ({ params, request }) => {
    const denied = guard()
    if (denied) return denied
    const settings = cfg.settingsFor(String(params.siteId))
    settings.protection.blocked_clients = settings.protection.blocked_clients.filter((c) => c.mac !== params.mac)
    const impact = impactFor(String(params.siteId), [{ field: 'protection.blocked_clients', path: 'BlackList', from: params.mac, to: null, disruption: 'none' }], isDry(request), 'Config')
    return respond(request, String(params.siteId), settings.protection, impact, [], () => ({ revision: commit(String(params.siteId), impact, 'site.updated', String(params.siteId), `Unblocked ${String(params.mac)}`), object: settings.protection }))
  }),
  http.put('/api/clients/:mac/meta', async ({ params, request }) => {
    const denied = guard()
    if (denied) return denied
    const body = (await request.json().catch(() => ({}))) as { alias?: string | null; notes?: string }
    if (body.alias && body.alias.length > 64) return invalid({ alias: 'Use at most 64 characters.' })
    const client = data.clients.find((c) => c.mac === params.mac)
    if (client) client.alias = body.alias ?? null
    return HttpResponse.json({ alias: body.alias ?? null, notes: body.notes ?? '' })
  }),

  // ---- history ----
  http.get('/api/sites/:siteId/history/samples', ({ params, request }) => {
    const search = new URL(request.url).searchParams
    const range = search.get('range') ?? '24h'
    const step = search.get('step') ?? '10m'
    const minutes = Number(step.replace(/m$/, '')) || 10
    const hours = Number(range.replace(/h$/, '')) || (range.endsWith('d') ? Number(range.replace(/d$/, '')) * 24 : 24)
    const body: SamplesView = { site_id: String(params.siteId), ap_id: search.get('ap') ?? '', range, step, samples: cfg.samples(String(params.siteId), search.get('ap'), minutes, Math.min(500, Math.round((hours * 60) / minutes))) }
    return guard() ?? HttpResponse.json(body)
  }),
  http.get('/api/sites/:siteId/changes', ({ request }) => {
    const before = Number(new URL(request.url).searchParams.get('before') ?? 0)
    const rows = cfg.changes.filter((c) => !before || c.revision < before)
    const page = rows.slice(0, 20)
    const body: ChangePage = {
      changes: page.map(({ revision, at, actor, kind, endpoint, summary, affected_aps }) => ({ revision, at, actor, kind, endpoint, summary, affected_aps })),
      next_before: rows.length > 20 ? page.at(-1)!.revision : null,
    }
    return guard() ?? HttpResponse.json(body)
  }),
  http.get('/api/sites/:siteId/changes/:rev', ({ params }) => {
    const record = cfg.changes.find((c) => String(c.revision) === params.rev)
    return guard() ?? (record ? HttpResponse.json(record) : notFound('Change'))
  }),
  http.post('/api/sites/:siteId/changes/:rev/restore', async ({ params, request }) => {
    const denied = guard()
    if (denied) return denied
    const siteId = String(params.siteId)
    const record = cfg.changes.find((c) => String(c.revision) === params.rev)
    if (!record) return notFound('Change')
    const body = (await request.json().catch(() => ({}))) as RestoreInput
    // Revision 41 changed the passphrase: restoring before it needs the old one or "keep current" (§4.11).
    if (record.revision <= 41) {
      const path = 'networks.wlan-example.psk'
      const keep = body.keep_current?.includes(path) ?? false
      const value = body.secrets?.[path]
      if (!keep && value !== 'previous-passphrase') {
        return fail(422, 'Some secrets at this revision differ from today’s.', 'secret_required', {
          'networks.wlan-example.psk': value ? 'That is not the passphrase Example had at this revision.' : 'Passphrase for Example at this revision differs from today’s. Enter it, or keep the current one.',
        })
      }
    }
    const changes: Change[] = record.diff.map((d) => ({ field: d.path, path: d.path, from: d.to, to: d.from, disruption: 'wlan_restart' }))
    const impact = impactFor(siteId, changes, isDry(request), 'Config')
    return respond(request, siteId, record, impact, [], () => ({ revision: commit(siteId, impact, 'site.restored', siteId, `Restored revision ${record.revision}`), object: record }))
  }),

  // ---- system: migration, backup, sessions ----
  http.get('/api/system/migration', () => guard() ?? HttpResponse.json(cfg.migration)),
  http.post('/api/system/migration/retry', ({ request }) => {
    const denied = guard()
    if (denied) return denied
    if (cfg.migration.state === 'committed') return fail(409, 'The migration has already committed.', 'conflict')
    if (isDry(request)) return HttpResponse.json({ ...cfg.migration, state: 'committed' })
    cfg.migration.state = 'committed'
    return HttpResponse.json(cfg.migration)
  }),
  http.post('/api/system/migration/finalise', () => {
    const denied = guard()
    if (denied) return denied
    if (!cfg.migration.can_finalise) return fail(409, 'Finalise is available 30 days after the migration committed, once every AP verified.', 'conflict')
    cfg.migration.compat = 2
    return HttpResponse.json(cfg.migration)
  }),
  http.get('/api/system/backup', () => {
    const denied = guard()
    if (denied) return denied
    return new HttpResponse(new Uint8Array([0x6d, 0x6f, 0x63, 0x6b]), {
      headers: { 'Content-Type': 'application/octet-stream', 'Content-Disposition': 'attachment; filename="controller-mock.db"' },
    })
  }),
  http.post('/api/system/sessions/revoke-all', () => {
    const denied = guard()
    if (denied) return denied
    data.authSessions.splice(0, data.authSessions.length, ...data.authSessions.filter((s) => s.current))
    return new HttpResponse(null, { status: 204 })
  }),
  http.get('/api/onboarding/ca-bundle.pem', () => guard() ?? new HttpResponse(data.onboarding.ca_pems.map((c) => c.pem).join('\n'), { headers: { 'Content-Type': 'application/x-pem-file' } })),
]
