import type { ReactNode } from 'react'
import { Link } from '@tanstack/react-router'
import { ChevronRight, CircleAlert, Info, TriangleAlert } from 'lucide-react'
import { cn } from '@/lib/utils'

// What needs attention, most severe first (design §3.3 "Attention card").
// Each item is one link; the text says what is wrong, the icon and edge
// only repeat it.

export type AttentionItem = {
  id: string
  severity: 'crit' | 'warn' | 'info'
  title: ReactNode
  detail?: ReactNode
  /** Router path the item links to. */
  to?: string
  search?: Record<string, unknown>
}

const rank = { crit: 0, warn: 1, info: 2 } as const
const icon = {
  crit: <CircleAlert aria-hidden className="size-4 text-destructive" />,
  warn: <TriangleAlert aria-hidden className="size-4 text-warning" />,
  info: <Info aria-hidden className="size-4 text-info" />,
}
const edge = { crit: 'before:bg-status-crit', warn: 'before:bg-status-warn', info: 'before:bg-status-info' }
const spoken = { crit: 'Problem: ', warn: 'Warning: ', info: '' }

/** Stable sort by severity, keeping the caller's order within a severity. */
export function sortAttention<T extends Pick<AttentionItem, 'severity'>>(items: readonly T[]): T[] {
  return items.map((item, index) => ({ item, index })).toSorted((a, b) => rank[a.item.severity] - rank[b.item.severity] || a.index - b.index).map((x) => x.item)
}

export function AttentionList({ items, label = 'Needs attention', className }: { items: readonly AttentionItem[]; label?: string; className?: string }) {
  if (!items.length) return null
  return (
    <ul aria-label={label} className={cn('divide-y overflow-hidden rounded-lg border bg-card', className)}>
      {sortAttention(items).map((item) => {
        const body = (
          <>
            <span className="mt-0.5 shrink-0">{icon[item.severity]}</span>
            <span className="grid min-w-0 flex-1 gap-0.5">
              <span className="text-sm font-medium break-words">
                <span className="sr-only">{spoken[item.severity]}</span>
                {item.title}
              </span>
              {item.detail ? <span className="text-sm break-words text-muted-foreground">{item.detail}</span> : null}
            </span>
            {item.to ? <ChevronRight aria-hidden className="mt-0.5 size-4 shrink-0 text-muted-foreground" /> : null}
          </>
        )
        const row = cn(
          'relative flex min-h-12 items-start gap-3 px-4 py-3 before:absolute before:inset-y-0 before:left-0 before:w-[3px]',
          edge[item.severity],
        )
        return (
          <li key={item.id}>
            {item.to ? (
              <Link
                to={item.to as never}
                search={item.search as never}
                className={cn(row, 'outline-none hover:bg-accent/45 focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring')}
              >
                {body}
              </Link>
            ) : (
              <div className={row}>{body}</div>
            )}
          </li>
        )
      })}
    </ul>
  )
}
