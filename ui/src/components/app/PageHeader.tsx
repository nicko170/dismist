import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

/**
 * The page H1. It takes focus after each navigation (tabIndex -1) so screen
 * readers announce the new page (design §2.4 "Scroll and focus"). On phones
 * the H1 stands alone: no eyebrow, no tagline (§2.2).
 */
export function PageHeader({
  title,
  meta,
  actions,
  className,
}: {
  title: ReactNode
  meta?: ReactNode
  actions?: ReactNode
  className?: string
}) {
  return (
    <header className={cn('flex flex-wrap items-end justify-between gap-x-6 gap-y-3', className)}>
      <div className="grid min-w-0 gap-1">
        <h1 tabIndex={-1} data-page-title className="text-2xl font-semibold break-words md:text-[1.75rem] md:leading-tight">
          {title}
        </h1>
        {meta ? <div className="text-sm text-muted-foreground max-md:hidden">{meta}</div> : null}
      </div>
      {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
    </header>
  )
}
