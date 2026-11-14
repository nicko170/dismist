import { FlaskConical } from 'lucide-react'
import { cn } from '@/lib/utils'

export const EXPERIMENTAL_HELP = 'Not yet verified on AP33 0.15. Test on a lab AP first.'

/** Flask badge for experimental options and actions (design P4, §3.0). */
export function ExperimentalBadge({ className, label = 'Experimental' }: { className?: string; label?: string }) {
  return (
    <span
      title={EXPERIMENTAL_HELP}
      className={cn(
        'inline-flex h-5 shrink-0 items-center gap-1 rounded-full border border-status-warn/45 bg-status-warn/12 px-1.5 text-[0.6875rem] font-medium text-foreground',
        className,
      )}
    >
      <FlaskConical aria-hidden className="size-3" />
      {label}
    </span>
  )
}
