import { useStreamStatus, type StreamStatus } from '@/api/sse'
import { cn } from '@/lib/utils'

const label: Record<StreamStatus, string> = {
  idle: 'Not connected',
  connecting: 'Connecting',
  live: 'Live',
  reconnecting: 'Reconnecting',
  offline: 'Offline',
}

const dot: Record<StreamStatus, string> = {
  idle: 'bg-status-idle',
  connecting: 'bg-status-idle',
  live: 'bg-status-good',
  reconnecting: 'bg-status-warn',
  offline: 'bg-status-crit',
}

/** Live / Reconnecting / Offline for the site stream (design §2.2, §7.6). */
export function StreamIndicator({ compact = false, className }: { compact?: boolean; className?: string }) {
  const status = useStreamStatus()
  if (status === 'idle') return null
  return (
    <span
      role="status"
      aria-live="polite"
      title={`Live updates: ${label[status]}`}
      data-status={status}
      className={cn(
        'inline-flex items-center gap-2 text-xs font-medium text-muted-foreground',
        compact ? 'size-11 justify-center' : 'h-7 rounded-full border px-2.5',
        className,
      )}
    >
      <span className="relative flex size-2">
        {status === 'live' ? (
          <span aria-hidden className="absolute inset-0 rounded-full bg-status-good/40 motion-safe:animate-ping [animation-iteration-count:2]" />
        ) : null}
        <span aria-hidden className={cn('relative size-2 rounded-full', dot[status])} />
      </span>
      <span className={compact ? 'sr-only' : undefined}>{label[status]}</span>
    </span>
  )
}
