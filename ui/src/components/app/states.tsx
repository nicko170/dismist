import { useEffect, useState, type ReactNode } from 'react'
import { CircleAlert, RotateCw } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { describeError } from '@/api/errors'
import { cn } from '@/lib/utils'

// Per-region states (design §3.0): empty explains and offers the next
// action; error says what failed, the API code, and Retry. Never raw JSON.

export function EmptyState({
  title,
  action,
  icon,
  className,
  children,
}: {
  title: string
  action?: ReactNode
  icon?: ReactNode
  className?: string
  children?: ReactNode
}) {
  return (
    <div className={cn('flex flex-col items-start gap-3 rounded-lg border border-dashed px-5 py-8 sm:items-center sm:text-center', className)}>
      {icon ? <div className="text-muted-foreground [&_svg]:size-6">{icon}</div> : null}
      <div className="grid gap-1">
        <p className="text-sm font-medium">{title}</p>
        {children ? <div className="text-sm text-muted-foreground">{children}</div> : null}
      </div>
      {action}
    </div>
  )
}

export function ErrorState({
  error,
  title = 'Could not load this',
  onRetry,
  className,
}: {
  error: unknown
  title?: string
  onRetry?: () => void
  className?: string
}) {
  const { message, code } = describeError(error)
  return (
    <div role="alert" className={cn('flex flex-col gap-3 rounded-lg border border-destructive/30 bg-destructive/5 px-5 py-4', className)}>
      <div className="flex gap-3">
        <CircleAlert aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
        <div className="grid min-w-0 gap-1">
          <p className="text-sm font-medium">{title}</p>
          <p className="text-sm break-words text-muted-foreground">{message}</p>
          {code ? <p className="text-xs text-muted-foreground">Code: <span className="font-mono">{code}</span></p> : null}
        </div>
      </div>
      {onRetry ? (
        <Button variant="outline" size="sm" className="self-start" onClick={onRetry}>
          <RotateCw /> Retry
        </Button>
      ) : null}
    </div>
  )
}

export function SkeletonRows({ rows = 3, className }: { rows?: number; className?: string }) {
  return (
    <div className={cn('grid gap-2', className)} role="status" aria-busy="true" aria-label="Loading">
      {Array.from({ length: rows }, (_, i) => (
        <Skeleton key={i} className="h-14 w-full" />
      ))}
    </div>
  )
}

/** Placeholder stat tiles (two columns on phones, four from md). */
export function SkeletonTiles({ count = 4, className }: { count?: number; className?: string }) {
  return (
    <div className={cn('grid grid-cols-2 gap-3 md:grid-cols-4', className)} role="status" aria-busy="true" aria-label="Loading">
      {Array.from({ length: count }, (_, i) => (
        <div key={i} className="grid gap-2 rounded-lg border bg-card px-4 py-3">
          <Skeleton className="h-3.5 w-1/2" />
          <Skeleton className="h-7 w-2/3" />
        </div>
      ))}
    </div>
  )
}

/** A placeholder card with a title and a few lines. */
export function SkeletonCard({ lines = 3, className }: { lines?: number; className?: string }) {
  return (
    <div className={cn('grid gap-3 rounded-lg border bg-card p-4', className)} role="status" aria-busy="true" aria-label="Loading">
      <Skeleton className="h-5 w-1/3" />
      {Array.from({ length: lines }, (_, i) => (
        <Skeleton key={i} className={cn('h-4', i === lines - 1 ? 'w-2/3' : 'w-full')} />
      ))}
    </div>
  )
}

/** A placeholder definition list in the KV layout. */
export function SkeletonKV({ items = 6, className }: { items?: number; className?: string }) {
  return (
    <div className={cn('grid grid-cols-2 gap-x-4 gap-y-3 sm:grid-cols-3 lg:grid-cols-4', className)} role="status" aria-busy="true" aria-label="Loading">
      {Array.from({ length: items }, (_, i) => (
        <div key={i} className="grid gap-1.5">
          <Skeleton className="h-3 w-1/2" />
          <Skeleton className="h-4 w-3/4" />
        </div>
      ))}
    </div>
  )
}

/**
 * A region's loading, error and empty states in one place (design §3.0):
 * the skeleton appears only after 300 ms, errors offer Retry.
 */
export function QueryState({
  query,
  skeleton,
  title,
  children,
}: {
  query: { isPending: boolean; isError: boolean; error: unknown; refetch: () => unknown }
  skeleton?: ReactNode
  title?: string
  children: ReactNode
}) {
  if (query.isError) return <ErrorState error={query.error} title={title} onRetry={() => void query.refetch()} />
  if (query.isPending) return <Delayed>{skeleton ?? <SkeletonRows />}</Delayed>
  return children
}

/** Renders its children only after `ms` (no skeleton flash on fast loads). */
export function Delayed({ ms = 300, children }: { ms?: number; children: ReactNode }) {
  const [shown, setShown] = useState(ms <= 0)
  useEffect(() => {
    if (ms <= 0) return
    const timer = window.setTimeout(() => setShown(true), ms)
    return () => window.clearTimeout(timer)
  }, [ms])
  return shown ? children : null
}
