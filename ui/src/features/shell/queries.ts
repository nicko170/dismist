import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { keys, stale } from '@/api/keys'
import type { SiteSummary } from '@/api/types'

// The shell needs the site list for the switcher, the `/` redirect and the
// site layout's not-found check. Workstream F's api/queries/sites.ts owns
// site mutations and SiteView; it shares this key, so both stay coherent.

const emptyCounts = { aps: 0, aps_online: 0, clients: 0, wlans: 0, pending_aps: 0 }

/** Accepts the v1 `[SiteSummary]` and the pre-v1 `{sites: [...]}` without counts. */
export function normaliseSites(body: unknown): SiteSummary[] {
  const rows = Array.isArray(body) ? body : ((body as { sites?: unknown[] } | null)?.sites ?? [])
  return rows
    .filter((row): row is Record<string, unknown> => typeof row === 'object' && row !== null && 'id' in row)
    .map((row) => ({
      id: String(row.id),
      name: typeof row.name === 'string' && row.name ? row.name : String(row.id),
      country: typeof row.country === 'string' ? row.country : '',
      timezone: typeof row.timezone === 'string' ? row.timezone : '',
      counts: { ...emptyCounts, ...(row.counts as object | undefined) },
    }))
}

export const sitesQuery = queryOptions({
  queryKey: keys.sites,
  queryFn: async ({ signal }) => normaliseSites(await api.get<unknown>('/api/sites', { signal })),
  staleTime: stale.config,
})
