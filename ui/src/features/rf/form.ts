import { z } from 'zod'
import type { Band } from '@/gen/api.gen'
import type { BandAdvanced, BandProfileView, RFProfileInput, RFProfileView } from '@/api/queries/rf'
import { leafSchema } from '@/features/settings/kit/options'
import { channelChips, DFS_5, fallbackChoices, POWER_MAX, POWER_MIN } from './channels'

// RF profile form (design §3.10): per band enabled, width, Auto channel list,
// DFS, power policy, preamble, DFS fallback and the advanced radio options.

export type PowerForm = { mode: 'auto' | 'fixed'; dbm: number | string | null; min: number | string | null; max: number | string | null }

export type BandForm = {
  enabled: boolean
  width: number
  channels: number[]
  dfs: boolean
  dfs_fallback: number
  power: PowerForm
  preamble: BandProfileView['preamble']
  advanced: Omit<BandAdvanced, 'min_probe_rssi'> & { min_probe_rssi: number | string | null }
}

export type RFForm = { managed: boolean; band_24: BandForm; band_5: BandForm; scanning_radio: boolean }

export const BAND_KEY: Record<Band, 'band_24' | 'band_5'> = { '2.4': 'band_24', '5': 'band_5' }

function bandForm(view: BandProfileView, band: Band): BandForm {
  const auto = band === '2.4' ? { min: 8, max: 15 } : { min: 12, max: 20 }
  return {
    enabled: view.enabled,
    width: view.width,
    channels: [...view.channels].sort((a, b) => a - b),
    dfs: view.dfs,
    dfs_fallback: view.dfs_fallback ?? 0,
    power:
      view.power.mode === 'fixed'
        ? { mode: 'fixed', dbm: view.power.dbm, min: auto.min, max: auto.max }
        : { mode: 'auto', dbm: auto.max, min: view.power.min, max: view.power.max },
    preamble: view.preamble ?? 'short',
    advanced: { ...view.advanced, min_probe_rssi: view.advanced?.min_probe_rssi ?? null },
  }
}

export function toRFForm(view: RFProfileView): RFForm {
  return {
    managed: view.managed,
    band_24: bandForm(view.band_24, '2.4'),
    band_5: bandForm(view.band_5, '5'),
    scanning_radio: view.scanning_radio ?? true,
  }
}

const n = (v: number | string | null): number => (typeof v === 'number' ? v : Number(v))

function bandInput(form: BandForm): RFProfileInput['band_24'] {
  return {
    enabled: form.enabled,
    width: form.width,
    channels: form.channels,
    dfs: form.dfs,
    dfs_fallback: form.dfs_fallback,
    power: form.power.mode === 'fixed' ? { mode: 'fixed', dbm: n(form.power.dbm) } : { mode: 'auto', min: n(form.power.min), max: n(form.power.max) },
    preamble: form.preamble,
    advanced: { ...form.advanced, min_probe_rssi: form.advanced.min_probe_rssi === '' || form.advanced.min_probe_rssi == null ? null : n(form.advanced.min_probe_rssi) },
  }
}

/** PUT /api/sites/$id/rf replaces the profile; scanning radio "on" is sent as null (the template). */
export function toRFInput(form: RFForm, base: RFProfileView | undefined): RFProfileInput {
  return {
    managed: form.managed,
    band_24: bandInput(form.band_24),
    band_5: bandInput(form.band_5),
    scanning_radio: form.scanning_radio ? (base?.scanning_radio === true ? true : null) : false,
  }
}

const powerValue = (v: unknown) => Number.isInteger(v) && (v as number) >= POWER_MIN && (v as number) <= POWER_MAX

function bandSchema(band: Band, valid: BandProfileView['valid_channels']) {
  const key = BAND_KEY[band]
  return z
    .object({
      enabled: z.boolean(),
      width: leafSchema(`site.rf.${key}.width`),
      channels: z.array(z.number()),
      dfs: z.boolean(),
      dfs_fallback: z.number(),
      power: z.object({ mode: z.enum(['auto', 'fixed']), dbm: z.unknown(), min: z.unknown(), max: z.unknown() }),
      preamble: z.enum(['auto', 'short', 'long']),
      advanced: z.object({
        taf: z.enum(['default', 'off', 'on']),
        twt: z.boolean(),
        antenna: z.enum(['default', '4x4', '3x3', '2x2', '1x1']),
        min_probe_rssi: z.unknown(),
        mu_mimo: z.boolean().nullable(),
        txbf: z.boolean().nullable(),
      }),
    })
    .superRefine((v, ctx) => {
      const add = (path: (string | number)[], message: string) => ctx.addIssue({ code: 'custom', path, message })
      if (v.enabled && !v.channels.length) add(['channels'], 'Choose at least one channel for Auto.')
      const width = v.width as number
      const chips = channelChips(band, width, valid)
      if (v.channels.some((c) => !chips.some((chip) => chip.value === c))) add(['channels'], `Some channels are not valid at ${width} MHz under AU/890.`)
      if (band === '5' && !v.dfs && v.channels.some((c) => chips.find((chip) => chip.value === c)?.dfs ?? DFS_5.has(c))) {
        add(['channels'], 'DFS channels are chosen; turn on “Use DFS channels” or remove them.')
      }
      const range = `Enter ${POWER_MIN} to ${POWER_MAX} dBm.`
      if (v.power.mode === 'fixed' && !powerValue(v.power.dbm)) add(['power', 'dbm'], range)
      if (v.power.mode === 'auto') {
        if (!powerValue(v.power.min)) add(['power', 'min'], range)
        if (!powerValue(v.power.max)) add(['power', 'max'], range)
        if (powerValue(v.power.min) && powerValue(v.power.max) && (v.power.min as number) > (v.power.max as number)) add(['power', 'max'], 'The maximum must be at least the minimum.')
      }
      if (band === '5' && v.dfs_fallback && !fallbackChoices(width, valid).includes(v.dfs_fallback)) add(['dfs_fallback'], `Choose a non-DFS channel valid at ${width} MHz, or Auto.`)
      const rssi = v.advanced.min_probe_rssi
      if (rssi !== null && rssi !== '' && !(Number.isInteger(rssi) && (rssi as number) >= -95 && (rssi as number) <= -50)) add(['advanced', 'min_probe_rssi'], 'Enter −95 to −50 dBm, or leave it empty.')
    })
}

export function rfSchema(view: RFProfileView) {
  return z.object({
    managed: z.boolean(),
    band_24: bandSchema('2.4', view.band_24.valid_channels),
    band_5: bandSchema('5', view.band_5.valid_channels),
    scanning_radio: z.boolean(),
  })
}
