import { useQuery } from '@tanstack/react-query'
import type { PushEvent } from '@/gen/api.gen'
import { keys } from '@/api/keys'
import { pendingQuery } from '@/api/queries/aps'

/** Latest push lifecycle state per AP, written by the stream's `push` events (§4.10). */
export function usePushStates(siteId: string): Record<string, PushEvent> {
  const { data } = useQuery<Record<string, PushEvent>>({
    queryKey: keys.sitePush(siteId),
    queryFn: () => ({}),
    initialData: {},
    staleTime: Infinity,
  })
  return data ?? {}
}

/** APs waiting to be adopted (the stream's `pending` event invalidates the list). */
export function usePending() {
  return useQuery({ ...pendingQuery(false), retry: false })
}
