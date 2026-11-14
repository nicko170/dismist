import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { PageHeader } from '@/components/app/PageHeader'
import { EVENT_KINDS, EVENT_RANGES, EVENT_SEVERITIES } from '@/api/queries/events'
import { compact, pickEnum, pickString, pickTime } from '@/features/overview/kit/search'
import { EventsView, type EventsSearch } from '@/features/events/EventsView'
import { normaliseMac } from '@/lib/mac'

// Events (design §3.13): stored events, newest first, filters in the URL.
export const Route = createFileRoute('/sites/$siteId/events/')({
  validateSearch: (search: Record<string, unknown>): EventsSearch =>
    compact({
      ap: pickString(search.ap, 64),
      client: normaliseMac(pickString(search.client, 64) ?? '') ?? undefined,
      kind: pickEnum(search.kind, [...EVENT_KINDS, 'dns_server', 'dns_latency', 'gtk_rekey', 'radar'] as string[]),
      severity: pickEnum(search.severity, EVENT_SEVERITIES as unknown as string[]),
      range: pickEnum(search.range, Object.keys(EVENT_RANGES)),
      before: pickTime(search.before),
    }),
  staticData: { title: 'Events', nav: 'events' },
  component: EventsRoute,
})

function EventsRoute() {
  const { siteId } = Route.useParams()
  const search = Route.useSearch()
  const navigate = useNavigate({ from: Route.fullPath })
  return (
    <div className="grid min-w-0 gap-5">
      <PageHeader title="Events" meta="Connections, config deliveries and what the APs report, newest first." />
      <EventsView
        siteId={siteId}
        search={search}
        onSearch={(next) => void navigate({ search: (prev) => compact({ ...prev, ...next }), replace: true })}
      />
    </div>
  )
}
