import { useId, type ReactNode } from 'react'
import { cn } from '@/lib/utils'

// Local kit for the monitoring screens (workstream E). These are candidates
// for promotion to components/app (WS-D, design §7.3): Card ≈ Section,
// ResponsiveList, StatTile, SignalChip, UtilBar, TimeSeries, Sparkline,
// ConfirmDialog, DetailDrawer, ExperimentalBadge, FilterBar.

/** A titled card: an h2 with an optional trailing action, then the body. */
export function Card({
  title,
  action,
  description,
  children,
  className,
  bodyClassName,
  id,
}: {
  title: ReactNode
  action?: ReactNode
  description?: ReactNode
  children: ReactNode
  className?: string
  bodyClassName?: string
  id?: string
}) {
  const headingId = useId()
  return (
    <section id={id} aria-labelledby={headingId} className={cn('min-w-0 rounded-lg border bg-card', className)}>
      <div className="flex min-h-12 flex-wrap items-center justify-between gap-x-3 gap-y-1 border-b px-4 py-2">
        <div className="grid min-w-0 gap-0.5">
          <h2 id={headingId} className="text-sm font-semibold">
            {title}
          </h2>
          {description ? <p className="text-xs text-muted-foreground">{description}</p> : null}
        </div>
        {action ? <div className="flex items-center gap-2">{action}</div> : null}
      </div>
      <div className={cn('min-w-0 p-4', bodyClassName)}>{children}</div>
    </section>
  )
}

/** A label/value row list for cards (stacks on phones). */
export function Facts({ items, className }: { items: { label: ReactNode; value: ReactNode; hidden?: boolean }[]; className?: string }) {
  return (
    <dl className={cn('grid gap-x-4 gap-y-2 text-sm', className)}>
      {items
        .filter((item) => !item.hidden)
        .map((item, index) => (
          <div key={index} className="grid min-w-0 grid-cols-[minmax(0,2fr)_minmax(0,3fr)] gap-3">
            <dt className="text-muted-foreground">{item.label}</dt>
            <dd className="min-w-0 break-words tabular-nums">{item.value}</dd>
          </div>
        ))}
    </dl>
  )
}

/** "–" for missing values, so empty cells never collapse. */
export function Dash({ children }: { children?: ReactNode }): ReactNode {
  if (children === null || children === undefined || children === '' || children === false) {
    return (
      <span className="text-muted-foreground" aria-label="not reported">
        –
      </span>
    )
  }
  return children
}
