import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { apQuery } from '@/api/queries/aps'
import { EVENT_KINDS, EVENT_RANGES, EVENT_SEVERITIES } from '@/api/queries/events'
import { normaliseMac } from '@/lib/mac'
import { usePageTitle } from '@/features/shell/title'
import { compact, pickEnum, pickString, pickTime } from '@/features/overview/kit/search'
import { APKernelLog } from '@/features/aps/APKernelLog'
import { EventsView, type EventsSearch } from '@/features/events/EventsView'

// AP detail, Events tab (design §3.5): the site's events for this AP, plus
// the "AP log" view of the kernel-log ring at ?kind=log (phase 4).
type Search = Omit<EventsSearch, 'ap'> & { kind?: string }

export const Route = createFileRoute('/sites/$siteId/aps/$apId/events')({
  validateSearch: (search: Record<string, unknown>): Search =>
    compact({
      client: normaliseMac(pickString(search.client, 64) ?? '') ?? undefined,
      kind: pickEnum(search.kind, [...EVENT_KINDS, 'log'] as string[]),
      severity: pickEnum(search.severity, EVENT_SEVERITIES as unknown as string[]),
      range: pickEnum(search.range, Object.keys(EVENT_RANGES)),
      before: pickTime(search.before),
    }),
  staticData: { title: 'Events' },
  component: EventsTab,
})

function EventsTab() {
  const { siteId, apId } = Route.useParams()
  const search = Route.useSearch()
  const navigate = useNavigate({ from: Route.fullPath })
  const ap = useQuery(apQuery(apId))
  usePageTitle(ap.data ? `${ap.data.name} events` : null)
  const log = search.kind === 'log'
  return (
    <div className="grid min-w-0 gap-4">
      <div role="group" aria-label="Event source" className="flex gap-1">
        <a
          href="?"
          onClick={(e) => {
            e.preventDefault()
            void navigate({ search: {}, replace: true })
          }}
          aria-current={!log ? 'true' : undefined}
          className={`inline-flex h-11 items-center rounded-md px-3 text-sm fine:md:h-8 ${!log ? 'bg-accent font-medium' : 'text-muted-foreground hover:bg-accent'}`}
        >
          Events
        </a>
        <a
          href="?kind=log"
          onClick={(e) => {
            e.preventDefault()
            void navigate({ search: { kind: 'log' }, replace: true })
          }}
          aria-current={log ? 'true' : undefined}
          className={`inline-flex h-11 items-center rounded-md px-3 text-sm fine:md:h-8 ${log ? 'bg-accent font-medium' : 'text-muted-foreground hover:bg-accent'}`}
        >
          AP log
        </a>
      </div>
      {log ? (
        <APKernelLog apId={apId} />
      ) : (
        <EventsView
          siteId={siteId}
          fixedAP={apId}
          search={search}
          onSearch={(next) => void navigate({ search: (prev) => compact({ ...prev, ...next, ap: undefined }) as Search, replace: true })}
        />
      )}
    </div>
  )
}

