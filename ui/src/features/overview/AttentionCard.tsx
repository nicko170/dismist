import { Link } from '@tanstack/react-router'
import { ChevronRight, CircleAlert, Info, TriangleAlert } from 'lucide-react'
import { cn } from '@/lib/utils'
import type { AttentionItem } from './attention'

const icon = {
  crit: { Icon: CircleAlert, className: 'text-destructive', label: 'Critical' },
  warn: { Icon: TriangleAlert, className: 'text-warning', label: 'Warning' },
  info: { Icon: Info, className: 'text-info', label: 'Note' },
} as const

/** Shown only when there is something to act on (design §3.3 item 2). */
export function AttentionCard({ items }: { items: AttentionItem[] }) {
  if (!items.length) return null
  const crit = items.some((item) => item.severity === 'crit')
  return (
    <section
      aria-labelledby="attention-heading"
      className={cn('rounded-lg border bg-card', crit ? 'border-destructive/40' : 'border-status-warn/50')}
    >
      <h2 id="attention-heading" className="border-b px-4 py-2.5 text-sm font-semibold">
        Needs attention <span className="font-normal text-muted-foreground tabular-nums">({items.length})</span>
      </h2>
      <ul className="divide-y">
        {items.map((item) => {
          const m = icon[item.severity]
          return (
            <li key={item.id}>
              <Link
                to={item.to as never}
                search={item.search as never}
                className="flex min-h-12 items-start gap-3 px-4 py-2.5 transition-colors hover:bg-accent/60 focus-visible:bg-accent/60 focus-visible:outline-none"
              >
                <span className={cn('mt-0.5 shrink-0', m.className)}>
                  <m.Icon aria-hidden className="size-4" />
                  <span className="sr-only">{m.label}: </span>
                </span>
                <span className="grid min-w-0 flex-1 gap-0.5">
                  <span className="text-sm font-medium break-words">{item.title}</span>
                  {item.detail ? <span className="text-xs break-words text-muted-foreground">{item.detail}</span> : null}
                </span>
                <ChevronRight aria-hidden className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
              </Link>
            </li>
          )
        })}
      </ul>
    </section>
  )
}
