import { describe, expect, it } from 'vitest'
import { homeRF } from '@/features/settings/mocks/config-data'
import { blockStart, channelChips, fallbackChoices, primaries, remapChannels, staticChannelError, WIDTHS } from './channels'
import { rfSchema, toRFForm, toRFInput } from './form'
import { histogram } from './Neighbours'

// AU/890 on AP33 (live `wl chanspecs`, 2026-11-14): 80 MHz blocks 36-48,
// 52-64, 100-112, 132-144, 149-161; no 160 MHz; 2.4 GHz 1 to 13.

describe('AU/890 channels', () => {
  it('never offers 160 MHz', () => {
    expect(WIDTHS['5']).toEqual([20, 40, 80])
    expect(WIDTHS['2.4']).toEqual([20])
  })

  it('has one chip per 80 MHz block, with DFS marked', () => {
    const chips = channelChips('5', 80)
    expect(chips.map((c) => c.label)).toEqual(['36–48', '52–64', '100–112', '132–144', '149–161'])
    expect(chips.filter((c) => c.dfs).map((c) => c.value)).toEqual([52, 100, 132])
    expect(chips.map((c) => c.group)).toEqual(['UNII-1', 'UNII-2 (DFS)', 'UNII-2e (DFS)', 'UNII-2e (DFS)', 'UNII-3'])
  })

  it('keeps 116 and 165 at 20 MHz only', () => {
    expect(primaries('5', 20).map((p) => p.channel)).toContain(116)
    expect(primaries('5', 20).map((p) => p.channel)).toContain(165)
    expect(primaries('5', 40).map((p) => p.channel)).not.toContain(116)
    expect(primaries('5', 80).map((p) => p.channel)).not.toContain(165)
  })

  it('offers 2.4 GHz channels 1 to 13', () => {
    expect(channelChips('2.4', 20).map((c) => c.value)).toEqual([1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13])
  })

  it('finds block starts', () => {
    expect(blockStart(153, 80)).toBe(149)
    expect(blockStart(64, 80)).toBe(52)
    expect(blockStart(112, 80)).toBe(100)
    expect(blockStart(44, 40)).toBe(44)
    expect(blockStart(48, 40)).toBe(44)
    expect(blockStart(161, 40)).toBe(157)
  })

  it('re-maps the Auto list when the width changes', () => {
    expect(remapChannels('5', [36, 149], 40)).toEqual([36, 149])
    expect(remapChannels('5', [44, 157], 80)).toEqual([36, 149])
    expect(remapChannels('5', [116, 165, 36], 80)).toEqual([36])
  })

  it('validates static channels for width and DFS', () => {
    expect(staticChannelError('5', 149, 80, false)).toBeNull()
    expect(staticChannelError('5', 52, 80, false)).toMatch(/DFS channel/)
    expect(staticChannelError('5', 52, 80, true)).toBeNull()
    expect(staticChannelError('5', 165, 80, false)).toMatch(/not available at 80 MHz/)
    expect(staticChannelError('2.4', 14, 20, false)).toMatch(/not available/)
  })

  it('offers only non-DFS fallbacks', () => {
    expect(fallbackChoices(80)).toEqual([36, 40, 44, 48, 149, 153, 157, 161])
  })

  it('prefers the server’s valid channel list', () => {
    const valid = [{ width: 80, primaries: [{ channel: 36, dfs: false }, { channel: 40, dfs: false }] }]
    expect(channelChips('5', 80, valid).map((c) => c.label)).toEqual(['36–40'])
  })
})

describe('RF form', () => {
  const view = homeRF()

  it('round-trips the live profile: 5 GHz 80 MHz fixed 17 dBm, 2.4 GHz 20 MHz fixed 12 dBm', () => {
    const form = toRFForm(view)
    expect(form.band_5).toMatchObject({ width: 80, power: { mode: 'fixed', dbm: 17 } })
    expect(form.band_24).toMatchObject({ width: 20, power: { mode: 'fixed', dbm: 12 } })
    const input = toRFInput(form, view)
    expect(input.band_5.power).toEqual({ mode: 'fixed', dbm: 17 })
    expect(input.band_24.power).toEqual({ mode: 'fixed', dbm: 12 })
    expect(input.scanning_radio).toBeNull()
    expect(rfSchema(view).safeParse(form).success).toBe(true)
  })

  it('refuses DFS channels without DFS, power 0 and inverted ranges', () => {
    const form = toRFForm(view)
    const issues = (patch: object) =>
      rfSchema(view)
        .safeParse({ ...form, band_5: { ...form.band_5, ...patch } })
        .error?.issues.map((i) => i.message) ?? []
    expect(issues({ channels: [52] })).toContain('DFS channels are chosen; turn on “Use DFS channels” or remove them.')
    expect(issues({ channels: [52], dfs: true })).toEqual([])
    expect(issues({ power: { mode: 'fixed', dbm: 0, min: 1, max: 2 } })).toContain('Enter 1 to 25 dBm.')
    expect(issues({ power: { mode: 'auto', dbm: 1, min: 20, max: 12 } })).toContain('The maximum must be at least the minimum.')
    expect(issues({ channels: [] })).toContain('Choose at least one channel for Auto.')
    expect(issues({ dfs_fallback: 52 })).toContain('Choose a non-DFS channel valid at 80 MHz, or Auto.')
  })
})

describe('neighbour histogram', () => {
  it('counts other networks per channel and marks the site’s own channels', () => {
    const bins = histogram(
      [
        { ssid: 'A', bssid: '02-bb-00-00-00-01', band: '5', channel: 36, width: 80, rssi: -70, seen_by: 'x', station_count: null, util: null, last_seen: '' },
        { ssid: 'B', bssid: '02-bb-00-00-00-02', band: '5', channel: 36, width: 80, rssi: -60, seen_by: 'x', station_count: null, util: null, last_seen: '' },
        { ssid: 'own', bssid: '02-aa-00-00-01-11', band: '5', channel: 149, width: 80, rssi: -40, seen_by: 'x', station_count: null, util: null, last_seen: '', own: true },
      ],
      new Map([[149, ['Hallway']]]),
    )
    expect(bins).toEqual([
      { channel: 36, count: 2, strongest: -60, own: [] },
      { channel: 149, count: 0, strongest: null, own: ['Hallway'] },
    ])
  })
})
