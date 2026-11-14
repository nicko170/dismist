import { describe, expect, it } from 'vitest'
import type { APView } from '@/gen/api.gen'
import { channelsText, filterAPs, lastBoot, needsAttention, offlineFor, pushChip, siteChannels, sortAPs, bandCounts } from './model'

const now = Date.parse('2026-11-14T12:00:00Z')
const iso = (msAgo: number) => new Date(now - msAgo).toISOString()

function ap(partial: Partial<APView>): APView {
  return {
    id: '02-aa-00-00-00-01',
    name: 'Hallway',
    site_id: 'home',
    model: 'AP33-WW',
    model_source: 'ap',
    firmware: 'apfw-0.15.35064-noro-84df',
    firmware_tested: true,
    status: 'in_sync',
    flags: [],
    connected: true,
    connected_at: iso(3600_000),
    last_seen: iso(1000),
    lan_ip: '192.0.2.21',
    remote_address: '198.51.100.7',
    behind_nat: true,
    pinned_ip: '',
    has_secret: true,
    config_revision: 42,
    applied_revision: 42,
    last_error: '',
    client_count: 10,
    uptime_seconds: 7200,
    radios: [
      { band: '2.4', channel: 1, width: 20, clients: 4, util: 30 },
      { band: '5', channel: 149, width: 80, clients: 6, util: 12 },
    ],
    updated_rev: 1,
    ...partial,
  }
}

describe('AP list helpers', () => {
  const list = [
    ap({ id: '02-aa-00-00-00-01', name: 'Hallway', client_count: 10, uptime_seconds: 100 }),
    ap({ id: '02-aa-00-00-00-02', name: 'Garage', status: 'offline', connected: false, client_count: 0, lan_ip: '192.0.2.22' }),
    ap({ id: '02-aa-00-00-00-03', name: 'Study', status: 'pending', client_count: 3, uptime_seconds: 900 }),
    ap({ id: '02-aa-00-00-00-04', name: 'Attic', flags: ['verify_mismatch'], client_count: 1 }),
  ]

  it('filters by status and query (name, ID with either separator, LAN IP)', () => {
    expect(filterAPs(list, { status: 'offline' }).map((a) => a.name)).toEqual(['Garage'])
    expect(filterAPs(list, { status: 'online' })).toHaveLength(3)
    expect(filterAPs(list, { status: 'attention' }).map((a) => a.name)).toEqual(['Study', 'Attic'])
    expect(filterAPs(list, { q: '192.0.2.22' }).map((a) => a.name)).toEqual(['Garage'])
    expect(filterAPs(list, { q: '02:aa:00:00:00:03' }).map((a) => a.name)).toEqual(['Study'])
    expect(filterAPs(list, { q: 'hall' }).map((a) => a.name)).toEqual(['Hallway'])
  })

  it('sorts by name, worst status, clients and uptime', () => {
    expect(sortAPs(list, 'name').map((a) => a.name)).toEqual(['Attic', 'Garage', 'Hallway', 'Study'])
    expect(sortAPs(list, 'status')[0].name).toBe('Garage')
    expect(sortAPs(list, 'clients')[0].name).toBe('Hallway')
    expect(sortAPs(list, 'uptime')[0].name).toBe('Attic')
    expect(list[0].name).toBe('Hallway') // not mutated
  })

  it('flags attention from status and flags', () => {
    expect(needsAttention(ap({ status: 'rejected' }))).toBe(true)
    expect(needsAttention(ap({ flags: ['local_files'] }))).toBe(true)
    expect(needsAttention(ap({}))).toBe(false)
  })

  it('formats channels and site summaries', () => {
    expect(channelsText(list[0].radios)).toBe('ch 1 · 149/80')
    expect(channelsText([])).toBe('')
    expect(siteChannels(list)).toBe('2.4: 1 · 5: 149/80')
    expect(bandCounts(list)).toEqual({ b24: 12, b5: 18 })
  })

  it('reports how long an AP has been offline', () => {
    expect(offlineFor({ connected: false, last_seen: iso(12 * 60_000) }, now)).toBe('12 m')
    expect(offlineFor({ connected: true, last_seen: iso(0) }, now)).toBeUndefined()
    expect(offlineFor({ connected: false, last_seen: null }, now)).toBeUndefined()
  })
})

describe('lastBoot (bug B: LastRebootTime parsed as "now")', () => {
  const identity = { serial: '', sku: '', boot_count: 3, last_reboot_reason: 'power-on', last_reboot_time: null as string | null, last_boot_state: '', tunnel_ipv6: '' }
  it('ignores a reported time after the boot implied by uptime', () => {
    const result = lastBoot({ identity: { ...identity, last_reboot_time: iso(5_000) }, last_seen: iso(0), uptime_seconds: 3 * 86400, connected: true }, now)
    expect(result.source).toBe('uptime')
    expect(result.reportedIgnored).toBe(true)
    expect(result.at?.toISOString()).toBe(iso(3 * 86400_000))
  })
  it('keeps a plausible reported time', () => {
    const result = lastBoot({ identity: { ...identity, last_reboot_time: iso(3 * 86400_000 + 30_000) }, last_seen: iso(0), uptime_seconds: 3 * 86400, connected: true }, now)
    expect(result.source).toBe('ap')
  })
  it('falls back to uptime, then nothing', () => {
    expect(lastBoot({ identity, last_seen: iso(0), uptime_seconds: 60, connected: true }, now).source).toBe('uptime')
    expect(lastBoot({ identity, last_seen: null, uptime_seconds: 0, connected: false }, now).at).toBeNull()
  })
})

describe('pushChip', () => {
  const base = { applied_revision: 42, config_revision: 43 }
  it('maps push states to chips', () => {
    expect(pushChip({ ap_id: 'x', revision: 43, state: 'notified', error: '' }, base)?.label).toBe('Pending rev 43')
    expect(pushChip({ ap_id: 'x', revision: 43, state: 'rejected', error: 'bad' }, base)?.tone).toBe('crit')
    expect(pushChip({ ap_id: 'x', revision: 43, state: 'timeout', error: '' }, base)?.label).toBe('Timed out rev 43')
    expect(pushChip({ ap_id: 'x', revision: 43, state: 'applied', error: '' }, { applied_revision: 43, config_revision: 43 })?.tone).toBe('good')
    expect(pushChip(undefined, base)).toBeNull()
  })
})
