import type { ReactNode } from 'react'
import { Link } from '@tanstack/react-router'
import { cn } from '@/lib/utils'

/** A headline number that links to the filtered list behind it (design §3.3). */
export function StatTile({
  label,
  value,
  detail,
  to,
  search,
  tone,
  icon,
  valueClassName,
}: {
  label: string
  value: ReactNode
  detail?: ReactNode
  to?: string
  search?: Record<string, unknown>
  tone?: 'crit' | 'warn'
  icon?: ReactNode
  valueClassName?: string
}) {
  const body = (
    <>
      <span className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
        {icon ? <span aria-hidden className="[&_svg]:size-3.5">{icon}</span> : null}
        {label}
      </span>
      <span
        className={cn(
          'text-2xl leading-tight font-semibold tabular-nums',
          tone === 'crit' && 'text-destructive',
          tone === 'warn' && 'text-warning',
          valueClassName,
        )}
      >
        {value}
      </span>
      {detail ? <span className="min-w-0 truncate text-xs text-muted-foreground">{detail}</span> : null}
    </>
  )
  const className = 'grid min-h-24 min-w-0 content-start gap-1 rounded-lg border bg-card px-4 py-3'
  if (!to) return <div className={className}>{body}</div>
  return (
    <Link
      to={to as never}
      search={search as never}
      className={cn(className, 'transition-colors hover:bg-accent/60 focus-visible:ring-3 focus-visible:ring-ring/40 outline-none')}
    >
      {body}
    </Link>
  )
}
