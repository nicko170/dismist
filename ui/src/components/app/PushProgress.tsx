import { useQuery } from '@tanstack/react-query'
import { keys } from '@/api/keys'
import type { APView, PushEvent, PushState } from '@/api/types'
import { cn } from '@/lib/utils'
import { PushStateChip } from './StatusChip'

// Per-AP apply progress after a save (design §3.0 "Saving"): Pending rev 42,
// Applied, Rejected, Timed out. The site stream's `push` events fill
// ['site', id, 'push']; an AP list whose applied_revision has reached the
// revision counts as applied even without a push event (reconnect mode).

export type PushProgressAP = { id: string; name: string }

export function pushStateFor(revision: number, push: PushEvent | undefined, ap: Pick<APView, 'applied_revision' | 'last_error'> | undefined): PushState | 'pending' {
  if (push && push.revision >= revision) return push.state
  if (ap && ap.applied_revision >= revision) return 'applied'
  return 'pending'
}

export function PushProgress({ siteId, revision, aps, className }: { siteId: string; revision: number; aps: readonly PushProgressAP[]; className?: string }) {
  const { data: pushes } = useQuery<Record<string, PushEvent>>({
    queryKey: keys.sitePush(siteId),
    queryFn: () => ({}),
    staleTime: Number.POSITIVE_INFINITY,
    // The stream owns this cache entry; never refetch over it.
    refetchOnMount: false,
    refetchOnWindowFocus: false,
  })
  const { data: list } = useQuery<APView[]>({ queryKey: keys.siteAPs(siteId), enabled: false })
  if (!aps.length) return null
  return (
    <ul aria-label={`Apply progress for revision ${revision}`} aria-live="polite" className={cn('flex flex-wrap gap-2', className)}>
      {aps.map((ap) => {
        const push = pushes?.[ap.id]
        const state = pushStateFor(revision, push, list?.find((row) => row.id === ap.id))
        return (
          <li key={ap.id} className="flex min-w-0 items-center gap-1.5 text-sm">
            <span className="truncate">{ap.name || ap.id}</span>
            <PushStateChip state={state} revision={state === 'pending' ? revision : undefined} />
            {state === 'rejected' && push?.error ? <span className="text-xs break-words text-destructive">{push.error}</span> : null}
          </li>
        )
      })}
    </ul>
  )
}
