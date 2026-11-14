import { describe, expect, it } from 'vitest'
import { normaliseAPs, normaliseDetail, normaliseNeighbours, normaliseSamples, siteClientSeries } from './aps'
import { eventFilters, normaliseEventPage } from './events'
import { normaliseMigration } from './system'

describe('AP normalisers', () => {
  it('accepts the v1 array and the pre-v1 envelope, deriving status', () => {
    const v0 = normaliseAPs({ aps: [{ id: '02-aa-00-00-00-01', connected: true, config_revision: 3, applied_revision: 2, management_ip: '192.0.2.9', lan_ip: '192.0.2.21', remote_address: '198.51.100.7' }] })
    expect(v0[0]).toMatchObject({ status: 'applying', pinned_ip: '192.0.2.9', behind_nat: true, name: '02-aa-00-00-00-01' })
    expect(normaliseAPs([{ id: 'x', status: 'auth_failing' }])[0].status).toBe('auth_failing')
    expect(normaliseAPs([{ id: 'y', connected: false, last_seen: '0001-01-01T00:00:00Z' }])[0].status).toBe('never_connected')
    expect(normaliseAPs(null)).toEqual([])
  })
  it('fills every APDetail section so tabs never crash on an older server', () => {
    const detail = normaliseDetail({ id: '02-aa-00-00-00-01', name: 'Hallway' })
    expect(detail.uplink.ports).toEqual([])
    expect(detail.health.load).toEqual([0, 0, 0])
    expect(detail.capabilities.ap_cmd).toBe(false)
    expect(detail.radios_detail).toEqual([])
  })
  it('sorts neighbours strongest first', () => {
    const rows = normaliseNeighbours({ neighbours: [{ bssid: 'a', rssi: -80 }, { bssid: 'b', rssi: -40 }] })
    expect(rows.map((r) => r.bssid)).toEqual(['b', 'a'])
  })
})

describe('history samples', () => {
  const now = Date.parse('2026-11-14T12:00:00Z')
  it('normalises minute samples and the radios array', () => {
    const rows = normaliseSamples({
      samples: [{ minute: '2026-11-14T11:59:00Z', ap_id: 'a', client_count: 4, radios: [{ band: '5', channel: 149, util_all: 12, clients: 3 }] }],
    })
    expect(rows[0]).toMatchObject({ ap_id: 'a', client_count: 4 })
    expect(rows[0].radios[0]).toMatchObject({ band: '5', util: 12 })
  })
  it('sums APs per step, taking each AP’s peak, with gaps as null', () => {
    const at = (iso: string) => Date.parse(iso)
    const samples = [
      { at: at('2026-11-14T11:50:30Z'), ap_id: 'a', connected: true, client_count: 4, radios: [] },
      { at: at('2026-11-14T11:51:30Z'), ap_id: 'a', connected: true, client_count: 6, radios: [] },
      { at: at('2026-11-14T11:52:30Z'), ap_id: 'b', connected: true, client_count: 3, radios: [] },
    ]
    const series = siteClientSeries(samples, '24h', now)
    expect(series).toHaveLength(145)
    expect(series.find((p) => p.t === at('2026-11-14T11:50:00Z'))?.v).toBe(9)
    expect(series.find((p) => p.t === at('2026-11-14T11:40:00Z'))?.v).toBeNull()
  })
})

describe('events', () => {
  it('normalises pages and drops empty filters', () => {
    const page = normaliseEventPage({ events: [{ id: 'e1', title: 't', count: 0 }], next_before: '' })
    expect(page.events[0].count).toBe(1)
    expect(page.next_before).toBeNull()
    expect(eventFilters({ ap: '', kind: 'dns', severity: undefined })).toEqual({ kind: 'dns' })
  })
})

describe('migration', () => {
  it('falls back to /api/system when the endpoint is missing', () => {
    const view = normaliseMigration({}, { migration: { state: 'legacy', reason: 'not enough disk', report_available: false }, db: { schema: 1, compat: 1 } } as never)
    expect(view).toMatchObject({ state: 'legacy', reason: 'not enough disk', schema: 1, compat: 1, report: null })
  })
  it('reads the report', () => {
    const view = normaliseMigration({ state: 'committed', schema: 2, compat: 1, report: { objects: [{ scope: 'site', id: 'home', name: 'Home', lifted: { 'rf.managed': true }, residual: { Monitor: { CCRev: '' } } }], aps: [{ id: 'a', verified: true, local_files: 'none', bssid_index: 'measured' }] } })
    expect(view.report?.objects[0].residual).toEqual({ Monitor: { CCRev: '' } })
    expect(view.report?.aps[0].mismatches).toEqual([])
  })
})

describe('migration report (internal/migrate V2Report)', () => {
  it('maps sites, networks and APs onto the screen', () => {
    const view = normaliseMigration({
      state: 'committed', schema: 2, compat: 1, can_finalise: false,
      report: {
        verified: true,
        sites: [{ id: 'home', probe: 'ap-1', rf_managed: true, typed: { rf: { managed: true } }, residual: { Radios: [{ Band: '5' }] } }],
        networks: [{ id: 'net-1', site_id: 'home', ssid: 'Example', security: 'wpa2_wpa3_personal', bssid_index: 1, bssid_source: 'predicted', typed: { dtim: 2 } }],
        aps: [{ id: 'ap-1', site_id: 'home', compiled: true, local_files: 'none', rf_override: { '5': { channel: 149 } }, equal: true }],
      },
    })
    expect(view.report?.objects.map((o) => `${o.scope}:${o.name}`)).toEqual(['site:home', 'wlan:Example', 'ap:ap-1'])
    expect(view.report?.objects[1].lifted).toMatchObject({ security: 'wpa2_wpa3_personal', bssid_index: 1, dtim: 2 })
    expect(view.report?.aps).toEqual([{ id: 'ap-1', name: 'ap-1', verified: true, local_files: 'none', bssid_index: 'predicted', mismatches: [] }])
  })
})
