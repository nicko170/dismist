import { describe, expect, it } from 'vitest'
import type { ClientView } from '@/gen/api.gen'
import { normaliseClient, normaliseClients, normaliseSessions } from '@/api/queries/clients'
import { bandText, displayName, filterClients, sortClients } from './model'

const client = (partial: Partial<ClientView>): ClientView => normaliseClient({ mac: '02-aa-bb-00-00-01', rssi: -60, ...partial } as Record<string, unknown>)

describe('client normalisation', () => {
  it('derives private MAC, self-assigned IP and signal', () => {
    const c = normaliseClient({ mac: '02:AA:BB:00:00:01', ip: '169.254.10.2', rssi: -80, hostname: 'phone' })
    expect(c.mac).toBe('02-aa-bb-00-00-01')
    expect(c.private_mac).toBe(true)
    expect(c.self_assigned).toBe(true)
    expect(c.signal).toBe('poor')
    expect(c.name).toBe('phone')
  })
  it('accepts the pre-v1 envelope and field names', () => {
    const rows = normaliseClients({ clients: [{ mac: '00-11-22-33-44-55', tx_bytes: 5, rx_bytes: 7, average_rssi: -61, ap_id: 'x' }] })
    expect(rows[0]).toMatchObject({ private_mac: false, down_bytes: 5, up_bytes: 7, avg_rssi: -61 })
  })
  it('normalises sessions and drops Go zero times', () => {
    const [s] = normaliseSessions({ sessions: [{ mac: '02-aa-bb-00-00-01', disconnected_at: '0001-01-01T00:00:00Z', connected_at: '2026-11-14T10:00:00Z' }] })
    expect(s.disconnected_at).toBeNull()
  })
})

describe('client filters and sort', () => {
  const rows = [
    client({ mac: '02-aa-bb-00-00-01', hostname: 'phone', rssi: -54, band: '5', ap_id: 'a', ssid: 'Example', down_bytes: 10 }),
    client({ mac: '02-aa-bb-00-00-02', hostname: 'printer', rssi: -78, band: '2.4', ap_id: 'b', ip: '192.0.2.102', down_bytes: 1000 }),
    client({ mac: '00-11-22-33-44-55', alias: 'TV', rssi: -66, band: '5', ap_id: 'a' }),
  ]
  it('filters by query (name, MAC in any form, IP), AP, band and signal', () => {
    expect(filterClients(rows, { q: 'print' }).map((c) => c.hostname)).toEqual(['printer'])
    expect(filterClients(rows, { q: '00:11:22' }).map(displayName)).toEqual(['TV'])
    expect(filterClients(rows, { q: '192.0.2.102' })).toHaveLength(1)
    expect(filterClients(rows, { ap: 'a', band: '5' })).toHaveLength(2)
    expect(filterClients(rows, { signal: 'poor' }).map(displayName)).toEqual(['printer'])
  })
  it('sorts weakest signal first, by name, or by usage', () => {
    expect(sortClients(rows).map(displayName)).toEqual(['printer', 'TV', 'phone'])
    expect(sortClients(rows, 'name').map(displayName)).toEqual(['phone', 'printer', 'TV'])
    expect(sortClients(rows, 'usage')[0].hostname).toBe('printer')
  })
  it('formats the band and channel', () => {
    expect(bandText({ band: '5', channel: 149 })).toBe('5 GHz ch149')
    expect(bandText({ band: '', channel: 0 })).toBe('')
  })
})
