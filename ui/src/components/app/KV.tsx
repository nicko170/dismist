import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

/** Definition list: two columns on phones too (design §3.5 header). */
export function KV({ items, className }: { items: { label: string; value: ReactNode }[]; className?: string }) {
  return (
    <dl className={cn('grid grid-cols-2 gap-x-4 gap-y-3 sm:grid-cols-3 lg:grid-cols-4', className)}>
      {items.map((item) => (
        <div key={item.label} className="grid min-w-0 content-start gap-0.5">
          <dt className="text-xs text-muted-foreground">{item.label}</dt>
          <dd className="min-w-0 text-sm break-words">{item.value}</dd>
        </div>
      ))}
    </dl>
  )
}
