import type { ReactNode } from 'react'
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
    <div className={cn('grid gap-2', className)} aria-busy="true" aria-label="Loading">
      {Array.from({ length: rows }, (_, i) => (
        <Skeleton key={i} className="h-14 w-full" />
      ))}
    </div>
  )
}
