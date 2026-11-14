import { describe, expect, it } from 'vitest'
import { deriveStatus, normaliseAPs } from './live'
import { aps } from './mocks/data'

describe('normaliseAPs', () => {
  it('passes a v1 [APView] through unchanged', () => {
    expect(normaliseAPs(aps)).toEqual(aps)
  })

  it('reads the pre-v1 {aps: [...]} shape and derives status', () => {
    const rows = normaliseAPs({
      aps: [
        { id: '02-aa-00-00-00-01', name: 'Hallway', site_id: 'home', connected: true, applied_revision: 4, config_revision: 4, lan_ip: '192.0.2.21', remote_address: '198.51.100.7', management_ip: '198.51.100.7', last_seen: '2026-11-14T00:00:00Z' },
        { id: '02-aa-00-00-00-02', name: '', site_id: 'home', connected: false, applied_revision: 0, config_revision: 4, last_seen: '0001-01-01T00:00:00Z' },
        { name: 'no id' },
      ],
    })
    expect(rows).toHaveLength(2)
    expect(rows[0]).toMatchObject({ status: 'in_sync', behind_nat: true, pinned_ip: '198.51.100.7', radios: [], flags: [] })
    expect(rows[1]).toMatchObject({ status: 'never_connected', name: '02-aa-00-00-00-02', last_seen: null })
  })
})

describe('deriveStatus', () => {
  it('follows the design order for what the old view carries', () => {
    expect(deriveStatus({ connected: false, last_seen: '2026-11-14T00:00:00Z', applied_revision: 1, config_revision: 1 })).toBe('offline')
    expect(deriveStatus({ connected: false, last_seen: null, applied_revision: 0, config_revision: 1 })).toBe('never_connected')
    expect(deriveStatus({ connected: true, applied_revision: 1, config_revision: 2, last_error: 'bad' })).toBe('rejected')
    expect(deriveStatus({ connected: true, applied_revision: 1, config_revision: 2 })).toBe('applying')
    expect(deriveStatus({ connected: true, applied_revision: 2, config_revision: 2 })).toBe('in_sync')
  })
})
