import type { Band } from '@/gen/api.gen'
import type { ValidChannels } from '@/api/queries/rf'

// The AU/890 channel table (design Appendix A, corrected by the live
// `wl chanspecs` on the production AP33, 2026-11-14): 2.4 GHz 1 to 13 at
// 20 MHz; 5 GHz 80 MHz blocks 36-48, 52-64, 100-112, 132-144 and 149-161;
// no 160 MHz under AU/890 on AP33, so it is never offered. The server's
// `valid_channels` wins when present; this table is the fallback and the
// grouping (UNII bands, 80 MHz blocks) the chips need.

export const DFS_5 = new Set([52, 56, 60, 64, 100, 104, 108, 112, 116, 120, 124, 128, 132, 136, 140, 144])

const PRIMARIES_5: Record<number, number[]> = {
  20: [36, 40, 44, 48, 52, 56, 60, 64, 100, 104, 108, 112, 116, 132, 136, 140, 144, 149, 153, 157, 161, 165],
  40: [36, 40, 44, 48, 52, 56, 60, 64, 100, 104, 108, 112, 132, 136, 140, 144, 149, 153, 157, 161],
  80: [36, 40, 44, 48, 52, 56, 60, 64, 100, 104, 108, 112, 132, 136, 140, 144, 149, 153, 157, 161],
}

const PRIMARIES_24: Record<number, number[]> = { 20: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13] }

export const WIDTHS: Record<Band, number[]> = { '2.4': [20], '5': [20, 40, 80] }

export type Primary = { channel: number; dfs: boolean }

/** Valid primaries for a band and width: the server's list, else the AU/890 table. */
export function primaries(band: Band, width: number, valid?: ValidChannels): Primary[] {
  const fromServer = valid?.find((v) => v.width === width)?.primaries
  if (fromServer?.length) return fromServer
  const table = band === '2.4' ? PRIMARIES_24 : PRIMARIES_5
  return (table[width] ?? []).map((channel) => ({ channel, dfs: band === '5' && DFS_5.has(channel) }))
}

/** The first primary of the 20/40/80 MHz block a 5 GHz channel sits in. */
export function blockStart(channel: number, width: number): number {
  if (width <= 20) return channel
  const size = width / 5 // channel numbers per block: 8 at 40 MHz, 16 at 80 MHz
  const origin = channel >= 149 ? 149 : 36
  return origin + Math.floor((channel - origin) / size) * size
}

export type ChannelChip = { value: number; label: string; dfs: boolean; group: string; channels: number[] }

export function unii(channel: number): string {
  if (channel <= 48) return 'UNII-1'
  if (channel <= 64) return 'UNII-2 (DFS)'
  if (channel <= 144) return 'UNII-2e (DFS)'
  return 'UNII-3'
}

/**
 * The chips for the Auto channel list (§3.10): every primary at 20 MHz, one
 * chip per 40 or 80 MHz block above that (valued by the block's first
 * primary), grouped by UNII band on 5 GHz.
 */
export function channelChips(band: Band, width: number, valid?: ValidChannels): ChannelChip[] {
  const list = primaries(band, width, valid)
  if (band === '2.4' || width <= 20) {
    return list.map((p) => ({ value: p.channel, label: String(p.channel), dfs: p.dfs, group: band === '2.4' ? '2.4 GHz' : unii(p.channel), channels: [p.channel] }))
  }
  const blocks = new Map<number, ChannelChip>()
  for (const p of list) {
    const start = blockStart(p.channel, width)
    const chip = blocks.get(start) ?? { value: start, label: '', dfs: false, group: unii(start), channels: [] }
    chip.channels.push(p.channel)
    chip.dfs = chip.dfs || p.dfs
    blocks.set(start, chip)
  }
  return [...blocks.values()].map((chip) => ({
    ...chip,
    label: chip.channels.length > 1 ? `${chip.channels[0]}–${chip.channels[chip.channels.length - 1]}` : String(chip.value),
  }))
}

/** Re-maps an Auto list to a new width: each channel becomes its block start; invalid ones drop. */
export function remapChannels(band: Band, channels: number[], width: number, valid?: ValidChannels): number[] {
  const chips = channelChips(band, width, valid)
  const out = new Set<number>()
  for (const channel of channels) {
    const chip = chips.find((c) => c.channels.includes(channel) || c.value === channel)
    if (chip) out.add(chip.value)
  }
  return [...out].toSorted((a, b) => a - b)
}

/** Whether a static channel is valid for the band, width and DFS setting (§3.6 Radios). */
export function staticChannelError(band: Band, channel: number, width: number, dfs: boolean, valid?: ValidChannels): string | null {
  const p = primaries(band, width, valid).find((x) => x.channel === channel)
  if (!p) return `Channel ${channel} is not available at ${width} MHz under AU/890.`
  if (p.dfs && !dfs) return `Channel ${channel} is a DFS channel. Turn on “Use DFS channels” in the site’s radio settings first.`
  return null
}

/** Non-DFS primaries at a width: the DFS fallback choices. */
export function fallbackChoices(width: number, valid?: ValidChannels): number[] {
  return primaries('5', width, valid)
    .filter((p) => !p.dfs)
    .map((p) => p.channel)
}

export const POWER_MIN = 1
export const POWER_MAX = 25
export const POWER_WARN = 20
