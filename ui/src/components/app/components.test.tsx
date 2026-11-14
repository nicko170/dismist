import { describe, expect, it } from 'vitest'
import type { Impact } from '@/api/types'
import { sortAttention } from './AttentionList'
import { diffKind, formatDiffValue } from './DiffView'
import { impactLine, pushLine } from './ImpactNotice'
import { tabIsActive } from './LinkTabs'
import { pushStateFor } from './PushProgress'
import { worstDisruption } from './StatusChip'

const ap = (over: Partial<Impact['aps'][number]> = {}): Impact['aps'][number] => ({
  id: '02-aa-00-00-00-01',
  name: 'Hallway',
  connected: true,
  push: 'notify',
  disruption: ['none'],
  ...over,
})

describe('impact line (design §3.0, §4.8)', () => {
  it('says No AP changes for an empty list', () => {
    expect(impactLine({ aps: [], disruption: 'none', summary: '' })).toBe('No AP changes')
  })

  it('names the worst disruption and how the change arrives', () => {
    const impact: Impact = {
      aps: [ap({ disruption: ['wlan_restart'] }), ap({ id: 'b', disruption: ['radio_bounce'] })],
      disruption: 'wlan_restart',
      summary: '',
    }
    expect(impactLine(impact)).toBe('Applies to 2 APs · Radio restarts · AP session kept')
    expect(pushLine([ap({ push: 'reconnect' })])).toBe('AP reconnects to apply')
    expect(pushLine([ap({ connected: false })])).toBe('1 AP offline, applies on reconnect')
  })

  it('orders disruption classes', () => {
    expect(worstDisruption([])).toBe('none')
    expect(worstDisruption(['wlan_restart', 'stranding_risk', 'radio_bounce'])).toBe('stranding_risk')
  })
})

describe('diff values', () => {
  it('never shows secrets and names absent keys', () => {
    expect(formatDiffValue('[changed]')).toMatch(/secret/)
    expect(formatDiffValue(undefined)).toBe('absent')
    expect(formatDiffValue({ a: 1 })).toBe('{"a":1}')
    expect(diffKind({ path: 'x', from: undefined, to: 1 })).toBe('added')
    expect(diffKind({ path: 'x', from: 1, to: null })).toBe('removed')
    expect(diffKind({ path: 'x', from: 1, to: 2 })).toBe('changed')
  })
})

describe('attention order', () => {
  it('sorts by severity and keeps order within one', () => {
    const items = [
      { id: 'a', severity: 'info' as const },
      { id: 'b', severity: 'crit' as const },
      { id: 'c', severity: 'warn' as const },
      { id: 'd', severity: 'crit' as const },
    ]
    expect(sortAttention(items).map((i) => i.id)).toEqual(['b', 'd', 'c', 'a'])
  })
})

describe('push progress', () => {
  it('uses push events, then the applied revision', () => {
    expect(pushStateFor(43, { ap_id: 'a', revision: 43, state: 'rejected', error: 'bad' }, undefined)).toBe('rejected')
    expect(pushStateFor(43, { ap_id: 'a', revision: 42, state: 'applied', error: '' }, { applied_revision: 43, last_error: '' })).toBe('applied')
    expect(pushStateFor(43, undefined, { applied_revision: 42, last_error: '' })).toBe('pending')
  })
})

describe('link tabs', () => {
  it('matches the index tab exactly and others by prefix', () => {
    expect(tabIsActive('/sites/home/aps/x', '/sites/home/aps/x', true)).toBe(true)
    expect(tabIsActive('/sites/home/aps/x/radios', '/sites/home/aps/x', true)).toBe(false)
    expect(tabIsActive('/sites/home/aps/x/radios', '/sites/home/aps/x/radios', false)).toBe(true)
  })
})
