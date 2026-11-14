import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { keys, stale } from '@/api/keys'
import type { Disruption, MutationResponse } from '@/gen/api.gen'
import { dryRunQuery, type MutationOptions } from './sites'

// Recommendations (design §4.6): a named gap between what an object runs and
// the research default, applied as a normal previewed mutation. The change
// itself is previewed with POST …/apply?dry_run=1 (§9.3 finding 29).

export type Recommendation = {
  id: string
  title: string
  detail: string
  scope: 'site' | 'ap' | 'wlan'
  object_id: string
  disruption: Disruption | string
  /** Where the UI shows it: "rf" also appears on the RF page (§3.10). */
  area?: 'rf' | 'site' | 'wlan' | 'ap'
  /** Informational only: nothing to apply. */
  informational?: boolean
}

export const recommendationsKey = (siteId: string) => [...keys.site(siteId), 'recommendations'] as const

export function recommendationsQuery(siteId: string) {
  return queryOptions({
    queryKey: recommendationsKey(siteId),
    queryFn: ({ signal }) =>
      api.get<Recommendation[]>(`/api/sites/${encodeURIComponent(siteId)}/recommendations`, { signal }),
    staleTime: stale.config,
  })
}

export function applyRecommendation(siteId: string, id: string, options: MutationOptions = {}) {
  return api.post<MutationResponse<unknown>>(
    `/api/sites/${encodeURIComponent(siteId)}/recommendations/${encodeURIComponent(id)}/apply`,
    undefined,
    { query: dryRunQuery(options), signal: options.signal },
  )
}
