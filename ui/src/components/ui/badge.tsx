import * as React from 'react'
import { cn } from '@/lib/utils'

export function Badge({ className, ...props }: React.ComponentProps<'span'>) {
  return (
    <span
      data-slot="badge"
      className={cn(
        'inline-flex h-5 min-w-5 shrink-0 items-center justify-center rounded-full px-1.5 text-xs font-semibold tabular-nums',
        className,
      )}
      {...props}
    />
  )
}
