import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { keys, stale } from '@/api/keys'
import type {
  Band,
  MutationResponse,
  NeighbourPage,
  PlanSelection,
  PlanProposal,
  RFProfileInput as GeneratedRFProfileInput,
  RFProfileView,
  ValidChannels as GeneratedValidChannels,
} from '@/gen/api.gen'
import { dryRunQuery, type MutationOptions } from './sites'

// Site RF profile, channel plan and neighbours (design §3.10, §4.9): the
// generated contract (internal/api, tygo).

export type {
  BandAdvanced,
  BandProfileView,
  Neighbour,
  PlanChange,
  PlanProposal,
  PlanRow,
  PlanStatus,
  PowerPolicy,
  RFProfileView,
} from '@/gen/api.gen'
export type ValidChannels = GeneratedValidChannels[]
/** PUT /api/sites/$id/rf: a merge patch over the profile; scanning_radio null restores the template's scan radio. */
export type RFProfileInput = Omit<GeneratedRFProfileInput, 'scanning_radio'> & { scanning_radio?: boolean | null }
/** GET /api/sites/$id/rf/neighbours: other networks, the site's own BSSIDs, and each AP's last scan. */
export type NeighboursView = NeighbourPage

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

/**
 * POST /api/sites/$id/rf/plan: a dry run returns the proposal; otherwise the
 * proposal is applied (only the `only` AP and band pairs, else every change
 * that is not "keep"). Both answer a MutationResponse whose object is the
 * proposal.
 */
export function planChannels(siteId: string, options: MutationOptions & { only?: PlanSelection[] } = {}) {
  return api.post<MutationResponse<PlanProposal>>(`${rfPath(siteId)}/plan`, options.only ? { only: options.only } : {}, {
    query: dryRunQuery(options),
    signal: options.signal,
  })
}

export function neighboursQuery(siteId: string, band: Band | undefined) {
  return queryOptions({
    queryKey: [...keys.siteRF(siteId), 'neighbours', band ?? 'all'] as const,
    queryFn: ({ signal }) => api.get<NeighboursView>(`${rfPath(siteId)}/neighbours`, { query: { band }, signal }),
    staleTime: stale.live,
  })
}
