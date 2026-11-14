import { useEffect, useState } from 'react'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import type { Impact, Warning } from '@/api/types'

// The impact preview (design §7.4 "Impact preview", §4.8 "Dry run"): while
// the form is dirty and valid, a 600 ms debounced ?dry_run=1 call fills the
// save bar's impact line and the "View diff" dialog. Each new value cancels
// the request in flight (the query's AbortSignal).

export type DryRunResult = { impact: Impact; warnings?: Warning[] | null; revision?: number }

export function useDebounced<T>(value: T, delay: number): [T, boolean] {
  const [settled, setSettled] = useState(value)
  const serial = JSON.stringify(value)
  useEffect(() => {
    const timer = window.setTimeout(() => setSettled(value), delay)
    return () => window.clearTimeout(timer)
    // The serialised value is the dependency: a new object with the same content does not restart the wait.
    // oxlint-disable-next-line react-hooks/exhaustive-deps
  }, [serial, delay])
  return [settled, JSON.stringify(settled) !== serial]
}

export function useDryRun<T>({
  scope,
  values,
  enabled,
  run,
  delay = 600,
}: {
  /** Identifies the object ("wlan", siteId, wlanId); part of the query key. */
  scope: readonly unknown[]
  values: T
  /** Usually isDirty && isValid. */
  enabled: boolean
  run: (values: T, signal: AbortSignal) => Promise<DryRunResult>
  delay?: number
}) {
  const [settled, waiting] = useDebounced(values, delay)
  const query = useQuery({
    queryKey: ['dry-run', ...scope, settled],
    queryFn: ({ signal }) => run(settled, signal),
    enabled: enabled && !waiting,
    retry: false,
    staleTime: 0,
    gcTime: 30_000,
    refetchOnWindowFocus: false,
    placeholderData: keepPreviousData,
  })
  return {
    impact: enabled ? (query.data?.impact ?? null) : null,
    warnings: enabled ? (query.data?.warnings ?? []) : [],
    loading: enabled && (waiting || query.isFetching),
    error: enabled && !waiting ? query.error : null,
  }
}
