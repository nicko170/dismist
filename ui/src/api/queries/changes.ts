import { infiniteQueryOptions, queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { keys, stale } from '@/api/keys'
import type { Actor, DiffEntry, MutationResponse } from '@/gen/api.gen'
import { dryRunQuery, type MutationOptions } from './sites'

// Config history, diff and restore (design §3.13, §4.11). PROVISIONAL types
// from §4.9 ChangeSummary plus the detail and restore shapes F needs; listed
// as requests to C.

export type ChangeAPResult = { id: string; name: string; state: string; error?: string }

export type ChangeSummary = {
  revision: number
  at: string
  actor: Actor
  summary: string
  /** "config" for mutations, "action" for audit entries (locate, reveal, migration…). */
  kind?: 'config' | 'action'
  affected_aps: ChangeAPResult[]
}

export type ChangePage = { changes: ChangeSummary[]; next_before: string | null }

export type ObjectDiff = { kind: 'site' | 'network' | 'ap' | 'rf'; id: string; name: string; diff: DiffEntry[] }

export type ChangeDetail = ChangeSummary & {
  endpoint: string
  objects: ObjectDiff[]
  /** Redacted per-AP effective config diffs. */
  aps: { id: string; name: string; diff: DiffEntry[] }[]
  restorable: boolean
}

/** What a restore will do (the `object` of the restore dry run, §4.11 "Restore"). */
export type RestorePreview = {
  revision: number
  changed: { kind: string; id: string; name: string }[]
  recreated: { id: string; ssid: string; bssid_warning?: string }[]
  removed: { id: string; ssid: string; clients: number }[]
  not_restored: { kind: string; id: string; name: string; reason: string }[]
}

/** One secret the restore needs: enter it, or keep today's value. */
export type RestoreSecret = { value?: string; keep?: boolean }

export type RestoreInput = {
  secrets?: Record<string, RestoreSecret>
  keep_new_wlans?: boolean
}

const changesPath = (siteId: string) => `/api/sites/${encodeURIComponent(siteId)}/changes`

export function changesQuery(siteId: string, filter: { ap?: string } = {}) {
  return infiniteQueryOptions({
    queryKey: [...keys.siteChanges(siteId), filter] as const,
    queryFn: ({ pageParam, signal }) =>
      api.get<ChangePage>(changesPath(siteId), { query: { before: pageParam, ap: filter.ap, limit: 50 }, signal }),
    initialPageParam: undefined as string | undefined,
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
