import { Skeleton } from '@/components/ui/skeleton'

/** Route pending state (after 300 ms): the page layout as skeletons. */
export function PendingPage() {
  return (
    <div className="grid gap-6" aria-busy="true" aria-label="Loading">
      <Skeleton className="h-8 w-48" />
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        {Array.from({ length: 4 }, (_, i) => (
          <Skeleton key={i} className="h-24" />
        ))}
      </div>
      <Skeleton className="h-56" />
    </div>
  )
}
