import type { ReactNode } from 'react'
import { Link } from '@tanstack/react-router'
import { cn } from '@/lib/utils'
import { Sparkline } from './Sparkline'
import type { Tone } from './StatusChip'

// One number that matters, with its unit and one line of context (§7.3).
// Tiles sit in a grid of two on phones. A `to` makes the whole tile a link.

const toneText: Partial<Record<Tone, string>> = {
  warn: 'text-warning',
  crit: 'text-destructive',
}

export function StatTile({
  label,
  value,
  unit,
  detail,
  tone,
  to,
  spark,
  className,
}: {
  label: string
  value: ReactNode
  unit?: string
  detail?: ReactNode
  tone?: Tone
  /** Router path the tile links to (relative to the basepath). */
  to?: string
  /** Recent values for a sparkline under the number. */
  spark?: readonly (number | null)[]
  className?: string
}) {
  const body = (
    <>
      <span className="text-sm text-muted-foreground">{label}</span>
      <span className={cn('flex items-baseline gap-1 text-2xl leading-tight font-semibold tracking-tight tabular-nums', tone && toneText[tone])}>
        {value}
        {unit ? <span className="text-sm font-medium text-muted-foreground">{unit}</span> : null}
      </span>
      {detail ? <span className="text-xs text-muted-foreground">{detail}</span> : null}
      {spark && spark.length > 1 ? <Sparkline values={spark} label={label} className="mt-1 h-6" /> : null}
    </>
  )
  const frame = cn('relative grid min-w-0 content-start gap-1 rounded-lg border bg-card px-4 py-3', className)
  if (!to) return <div className={frame}>{body}</div>
  return (
    <Link
      to={to as never}
      className={cn(frame, 'transition-colors duration-150 outline-none hover:border-input/60 hover:bg-accent/40 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring')}
    >
      {body}
    </Link>
  )
}

/** Tiles in two columns on phones, up to four from md. */
export function StatGrid({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn('grid grid-cols-2 gap-3 md:grid-cols-4', className)}>{children}</div>
}
