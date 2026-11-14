import { describe, expect, it } from 'vitest'
import type { APView, EventView, SystemView } from '@/gen/api.gen'
import { attentionItems } from './attention'

const now = Date.parse('2026-11-14T12:00:00Z')
const base = {
  id: '02-aa-00-00-00-01',
  name: 'Hallway',
  site_id: 'home',
  model: '',
  model_source: 'ap',
  firmware: '',
  firmware_tested: true,
  status: 'in_sync',
  flags: [],
  connected: true,
  connected_at: null,
  last_seen: null,
  lan_ip: '',
  remote_address: '',
  behind_nat: false,
  pinned_ip: '',
  has_secret: true,
  config_revision: 42,
  applied_revision: 42,
  last_error: '',
  client_count: 0,
  uptime_seconds: 0,
  radios: [],
  updated_rev: 1,
} satisfies APView

describe('attentionItems', () => {
  it('is empty when everything is fine', () => {
    expect(attentionItems({ siteId: 'home', aps: [base], now })).toEqual([])
  })

  it('lists problems most severe first, each with a link', () => {
    const items = attentionItems({
      siteId: 'home',
      now,
      aps: [
        { ...base, id: 'a', name: 'Study', status: 'pending', config_revision: 43 },
        { ...base, id: 'b', name: 'Garage', status: 'offline', connected: false, last_seen: new Date(now - 12 * 60_000).toISOString() },
        { ...base, id: 'c', name: 'Attic', status: 'rejected', last_error: 'bad radio', flags: ['verify_mismatch'] },
      ],
      pendingCount: 1,
      recommendations: 2,
    })
    expect(items.map((i) => i.severity)).toEqual(['crit', 'crit', 'warn', 'warn', 'info', 'info'])
    expect(items.map((i) => i.title)).toContain('Garage offline for 12 m')
    expect(items.find((i) => i.id === 'rej-c')?.detail).toBe('bad radio')
    expect(items.find((i) => i.id === 'off-b')?.to).toBe('/sites/home/aps/b')
    expect(items.find((i) => i.id === 'pending')).toMatchObject({ to: '/onboard', search: { site: 'home' } })
  })

  it('warns about the certificate, legacy mode and inactive VLANs', () => {
    const system = {
      wss: { cert: { not_after: new Date(now + 5 * 86400_000).toISOString() } },
      migration: { state: 'legacy', reason: 'not enough disk', report_available: false },
    } as unknown as SystemView
    const event = { id: 'e1', kind: 'inactive_vlan', title: 'VLAN 30 is not reaching Hallway', detail: '', resolved_by: null } as unknown as EventView
    const items = attentionItems({ siteId: 'home', aps: [], now, system, events: [event] })
    expect(items.map((i) => i.id)).toEqual(['cert', 'vlan-e1', 'legacy'])
    expect(items[0]).toMatchObject({ severity: 'crit', title: 'The AP endpoint certificate expires in 5 d' })
  })
})
