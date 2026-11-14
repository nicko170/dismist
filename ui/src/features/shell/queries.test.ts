import { describe, expect, it } from 'vitest'
import { normaliseSites } from './queries'
import { switchTarget } from './SiteSwitcher'
import { activeNavId } from './nav'
import { formatTitle } from './title'

describe('normaliseSites', () => {
  it('accepts v1 arrays and the pre-v1 {sites} wrapper', () => {
    expect(normaliseSites([{ id: 'home', name: 'Home', country: 'AU', timezone: 'Australia/Sydney', counts: { aps: 2, aps_online: 1 } }])[0].counts).toMatchObject({ aps: 2, aps_online: 1, clients: 0 })
    expect(normaliseSites({ sites: [{ id: 'lab' }] })).toEqual([
      { id: 'lab', name: 'lab', country: '', timezone: '', counts: { aps: 0, aps_online: 0, clients: 0, wlans: 0, pending_aps: 0 } },
    ])
    expect(normaliseSites(null)).toEqual([])
  })
})

describe('shell helpers', () => {
  it('switches site keeping the top-level section', () => {
    expect(switchTarget('/sites/home/aps/02-aa-00-00-00-01/radios', 'lab')).toBe('/sites/lab/aps')
    expect(switchTarget('/system', 'lab')).toBe('/sites/lab/overview')
  })

  it('knows which nav item a path belongs to', () => {
    expect(activeNavId('/sites/home/settings/general')).toBe('settings')
    expect(activeNavId('/onboard/register')).toBe('onboard')
    expect(activeNavId('/sites/home/unknown')).toBeNull()
  })

  it('formats document titles with the offline prefix', () => {
    expect(formatTitle('Access points', 'Home', 1)).toBe('(1) Access points · Home · Dismist')
    expect(formatTitle('Home', 'Home', 0)).toBe('Home · Dismist')
    expect(formatTitle(undefined, undefined, 0)).toBe('Dismist')
  })
})
