import { CircleAlert, CircleCheck, Info, TriangleAlert } from 'lucide-react'
import type { EventView, Severity } from '@/gen/api.gen'
import { RelativeTime } from '@/components/app/RelativeTime'
import { cn } from '@/lib/utils'
import { kindLabel } from '@/api/queries/events'

// Event rows (design §3.13): severity icon with text, relative time, AP
// name, title, "×12" for upserted or repeated events, resolved events
// greyed. The AP and the time show on every row, phones included. A row
// opens the detail drawer.

const severityMeta: Record<Severity, { label: string; icon: typeof Info; className: string }> = {
  info: { label: 'Info', icon: Info, className: 'text-info' },
  warning: { label: 'Warning', icon: TriangleAlert, className: 'text-warning' },
  error: { label: 'Error', icon: CircleAlert, className: 'text-destructive' },
}

export function SeverityIcon({ severity, resolved }: { severity: Severity; resolved?: boolean }) {
  if (resolved) {
    return (
      <span className="inline-flex text-muted-foreground" title="Resolved">
        <CircleCheck aria-hidden className="size-4" />
        <span className="sr-only">Resolved</span>
      </span>
    )
  }
  const meta = severityMeta[severity] ?? severityMeta.info
  return (
    <span className={cn('inline-flex', meta.className)} title={meta.label}>
      <meta.icon aria-hidden className="size-4" />
      <span className="sr-only">{meta.label}</span>
    </span>
  )
}

export function EventRow({
  event,
  apName,
  showAP = true,
  onOpen,
}: {
  event: EventView
  apName?: string
  showAP?: boolean
  onOpen?: (event: EventView) => void
}) {
  const resolved = Boolean(event.resolved_by)
  const detail = event.detail && event.detail.trim() !== event.title.trim() ? event.detail : ''
  const name = apName || event.ap_name || (event.ap_id ? event.ap_id : 'Controller')
  const body = (
    <>
      <span className="mt-0.5 shrink-0">
        <SeverityIcon severity={event.severity} resolved={resolved} />
      </span>
      <span className="grid min-w-0 flex-1 gap-0.5">
        <span className={cn('text-sm font-medium break-words', resolved && 'text-muted-foreground')}>
          {event.title}
          {event.count > 1 ? <span className="ml-1.5 text-xs font-normal text-muted-foreground tabular-nums">×{event.count}</span> : null}
        </span>
        {detail ? <span className="text-xs break-words text-muted-foreground">{detail}</span> : null}
        <span className="flex min-w-0 flex-wrap gap-x-2 text-xs text-muted-foreground">
          {showAP ? <span className="truncate">{name}</span> : null}
          <RelativeTime value={event.last_seen || event.first_seen} />
          <span>{kindLabel(event.kind)}</span>
          {resolved ? <span>resolved by {event.resolved_by}</span> : null}
        </span>
      </span>
    </>
  )
  if (!onOpen) return <div className="flex min-w-0 items-start gap-3 px-4 py-3">{body}</div>
  return (
    <button
      type="button"
      onClick={() => onOpen(event)}
      className="flex min-h-11 w-full min-w-0 items-start gap-3 px-4 py-3 text-left transition-colors hover:bg-accent/60 focus-visible:bg-accent/60 focus-visible:outline-none"
    >
      {body}
    </button>
  )
}

export function EventList({
  events,
  apNames,
  showAP = true,
  onOpen,
  label,
  className,
}: {
  events: EventView[]
  apNames?: Map<string, string>
  showAP?: boolean
  onOpen?: (event: EventView) => void
  label: string
  className?: string
}) {
  return (
    <ul aria-label={label} className={cn('divide-y overflow-hidden rounded-lg border bg-card', className)}>
      {events.map((event) => (
        <li key={event.id}>
          <EventRow event={event} apName={apNames?.get(event.ap_id)} showAP={showAP} onOpen={onOpen} />
        </li>
      ))}
    </ul>
  )
}
