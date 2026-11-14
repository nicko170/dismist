import { infiniteQueryOptions, queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { keys, stale } from '@/api/keys'
import type { AffectedAP, ChangeDetail, ChangePage, MutationResponse, RestoreInput, RestorePlan } from '@/gen/api.gen'
import { dryRunQuery, type MutationOptions } from './sites'

// Config history, diff and restore (design §3.13, §4.11): the generated
// contract (internal/api, tygo).

export type { ChangeDetail, ChangePage, ChangeSummary, RestoreInput } from '@/gen/api.gen'
export type ChangeAPResult = AffectedAP
/** What a restore will do (the `object` of the restore dry run, §4.11 "Restore"). */
export type RestorePreview = RestorePlan

const changesPath = (siteId: string) => `/api/sites/${encodeURIComponent(siteId)}/changes`

export function changesQuery(siteId: string, filter: { ap?: string } = {}) {
  return infiniteQueryOptions({
    queryKey: [...keys.siteChanges(siteId), filter] as const,
    queryFn: ({ pageParam, signal }) =>
      api.get<ChangePage>(changesPath(siteId), { query: { before: pageParam, ap: filter.ap, limit: 50 }, signal }),
    initialPageParam: undefined as number | undefined,
    getNextPageParam: (last) => last.next_before ?? undefined,
    staleTime: stale.config,
  })
}

export function changeQuery(siteId: string, revision: number) {
  return queryOptions({
    queryKey: [...keys.siteChanges(siteId), 'rev', revision] as const,
    queryFn: ({ signal }) => api.get<ChangeDetail>(`${changesPath(siteId)}/${revision}`, { signal }),
    staleTime: stale.config,
  })
}

export function restoreChange(siteId: string, revision: number, body: RestoreInput, options: MutationOptions = {}) {
  return api.post<MutationResponse<RestorePreview>>(`${changesPath(siteId)}/${revision}/restore`, body, {
    query: dryRunQuery(options),
    signal: options.signal,
  })
}
