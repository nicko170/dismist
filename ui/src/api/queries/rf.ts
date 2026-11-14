import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { keys, stale } from '@/api/keys'
import type { Band, MutationResponse } from '@/gen/api.gen'
import type { Recommendation } from './recommendations'
import { dryRunQuery, type MutationOptions } from './sites'

// Site RF profile, channel plan and neighbours (design §3.10, §4.9
// RFProfileView, PlanProposal, Neighbour). PROVISIONAL until C's phase-2
// DTOs land in gen/api.gen.ts.

export type PowerPolicy = { mode: 'fixed'; dbm: number } | { mode: 'auto'; min: number; max: number }

export type BandAdvanced = {
  taf: 'default' | 'off' | 'on'
  twt: boolean
  antenna: 'default' | '4x4' | '3x3' | '2x2' | '1x1'
  min_probe_rssi: number | null
  mu_mimo: boolean | null
  txbf: boolean | null
}

export type ValidChannels = { width: number; primaries: { channel: number; dfs: boolean }[] }[]

export type BandProfileView = {
  enabled: boolean
  width: number
  channels: number[]
  dfs: boolean
  dfs_fallback: number
  power: PowerPolicy
  preamble: 'auto' | 'short' | 'long'
  advanced: BandAdvanced
  valid_channels?: ValidChannels
}

export type PlanStatus = 'match' | 'mismatch' | 'cac' | 'radio_missing' | 'offline'

export type PlanRow = {
  ap_id: string
  ap_name: string
  band: Band
  mode: 'auto' | 'static'
  channel: number
  width: number
  power_dbm: number
  dfs_fallback: number | null
  /** "initial assignment" or "planner, 3 Dec" (§3.6 Radios). */
  source?: string
  actual: { channel: number; width: number; max_power_dbm: number; util: number; clients: number } | null
  status: PlanStatus
}

export type RFProfileView = {
  managed: boolean
  country: string
  ccrev: string | null
  band_24: BandProfileView
  band_5: BandProfileView
  scanning_radio: boolean | null
  plan: PlanRow[]
  recommendations: Recommendation[]
  updated_rev?: number
}

/** PUT /api/sites/$id/rf: the editable profile. */
export type RFProfileInput = {
  managed: boolean
  band_24: Omit<BandProfileView, 'valid_channels'>
  band_5: Omit<BandProfileView, 'valid_channels'>
  scanning_radio: boolean | null
}

export type PlanChange = {
  ap_id: string
  ap_name?: string
  band: string
  from: { channel: number; power_dbm: number }
  to: { channel: number; power_dbm: number; dfs_fallback: number | null }
  cost_before: number
  cost_after: number
  keep: boolean
  reason: string
  disruption: 'radio_bounce' | 'none'
}

export type PlanProposal = {
  inputs: { aps: number; scans_age_s: Record<string, number | null> }
  /** Lets apply refuse (409) when the inputs moved since the preview. */
  inputs_hash?: string
  changes: PlanChange[]
}

export type Neighbour = {
  ssid: string
  bssid: string
  band: string
  channel: number
  width: number
  rssi: number
  seen_by: string
  station_count: number | null
  util: number | null
  last_seen: string
  /** True for a BSSID of one of this site's own APs (§5.10). */
  own?: boolean
}

export type NeighboursView = {
  neighbours: Neighbour[]
  /** Per AP: when its last scan arrived (scans come in about 1 in 10 full frames). */
  scans: { ap_id: string; ap_name: string; at: string | null }[]
}

const rfPath = (siteId: string) => `/api/sites/${encodeURIComponent(siteId)}/rf`

export function rfQuery(siteId: string) {
  return queryOptions({
    queryKey: keys.siteRF(siteId),
    queryFn: ({ signal }) => api.get<RFProfileView>(rfPath(siteId), { signal }),
    staleTime: stale.config,
  })
}

export function putRF(siteId: string, body: RFProfileInput, options: MutationOptions = {}) {
  return api.put<MutationResponse<RFProfileView>>(rfPath(siteId), body, {
    query: dryRunQuery(options),
    ifMatch: options.ifMatch,
    signal: options.signal,
  })
}

export function planChannels(siteId: string, options: MutationOptions & { inputsHash?: string } = {}) {
  return options.dryRun
    ? api.post<PlanProposal>(`${rfPath(siteId)}/plan`, {}, { query: { dry_run: 1 }, signal: options.signal })
    : api.post<MutationResponse<RFProfileView>>(
        `${rfPath(siteId)}/plan`,
        options.inputsHash ? { inputs_hash: options.inputsHash } : {},
        { signal: options.signal },
      )
}

export function neighboursQuery(siteId: string, band: Band | undefined) {
  return queryOptions({
    queryKey: [...keys.siteRF(siteId), 'neighbours', band ?? 'all'] as const,
    queryFn: ({ signal }) => api.get<NeighboursView>(`${rfPath(siteId)}/neighbours`, { query: { band }, signal }),
    staleTime: stale.live,
  })
}
