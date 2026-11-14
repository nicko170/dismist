import { useEffect, useMemo, useRef, useState } from 'react'
import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowUp, Download } from 'lucide-react'
import type { EventView } from '@/gen/api.gen'
import { keys, type EventFilters } from '@/api/keys'
import { siteAPsQuery } from '@/api/queries/aps'
import { EVENT_KINDS, EVENT_RANGES, EVENT_SEVERITIES, kindLabel, siteEventsQuery } from '@/api/queries/events'
import { Button } from '@/components/ui/button'
import { inputClass } from '@/components/ui/input'
import { EmptyState, ErrorState, SkeletonRows } from '@/components/app/states'
import { normaliseMac } from '@/lib/mac'
import { cn } from '@/lib/utils'
import { FilterBar } from '@/features/overview/kit/FilterBar'
import { downloadCsv, toCsv } from '@/features/overview/kit/csv'
import { EventDrawer } from './EventDrawer'
import { EventList } from './EventList'

// The stored event list (design §3.13) for a site, or one AP (the AP detail
// Events tab passes `fixedAP`). Filters live in the URL; older pages load
// with the before= cursor; events that arrive over the stream while the
// list is scrolled, or that a filtered list cannot place, raise a "New
// events" pill instead of moving the rows under the reader.

export type EventsSearch = EventFilters & { before?: string }

const rangeLabels: Record<string, string> = { '1h': 'Last hour', '24h': 'Last 24 hours', '7d': 'Last 7 days', '30d': 'Last 30 days' }
const severityLabels: Record<string, string> = { info: 'Info', warning: 'Warning', error: 'Error' }

export function EventsView({
  siteId,
  search,
  onSearch,
  fixedAP,
}: {
  siteId: string
  search: EventsSearch
  onSearch: (next: Partial<EventsSearch>) => void
  fixedAP?: string
}) {
  const qc = useQueryClient()
  const filters: EventFilters = { ap: fixedAP ?? search.ap, client: search.client, kind: search.kind, severity: search.severity, range: search.range }
  const isDefault = !fixedAP && !search.ap && !search.client && !search.kind && !search.severity && !search.range && !search.before
  const query = useInfiniteQuery(siteEventsQuery(siteId, filters, { before: search.before }))
  const aps = useQuery(siteAPsQuery(siteId))
  const apNames = useMemo(() => new Map((aps.data ?? []).map((ap) => [ap.id, ap.name])), [aps.data])
  const events = useMemo(() => query.data?.pages.flatMap((page) => page.events) ?? [], [query.data])
  const [open, setOpen] = useState<EventView | null>(null)

  // "New events (n)": unseen rows at the top while scrolled, or stream
  // events a filtered list could not place (keys.siteNewEvents).
  const streamed = useQuery<number>({ queryKey: keys.siteNewEvents(siteId), queryFn: () => 0, initialData: 0, staleTime: Infinity })
  const [baseline, setBaseline] = useState<Set<string> | null>(null)
  const scrolled = useRef(false)
  useEffect(() => {
    const onScroll = () => {
      scrolled.current = window.scrollY > 240
    }
    onScroll()
    window.addEventListener('scroll', onScroll, { passive: true })
    return () => window.removeEventListener('scroll', onScroll)
  }, [])
  const firstPage = query.data?.pages[0]?.events
  useEffect(() => {
    if (!firstPage) return
    if (!baseline || !scrolled.current) setBaseline(new Set(firstPage.map((e) => e.id)))
  }, [firstPage, baseline])
  const unseen = isDefault && baseline && firstPage ? firstPage.filter((e) => !baseline.has(e.id)).length : 0
  const pending = unseen + (isDefault ? 0 : streamed.data ?? 0)

  const showNew = () => {
    window.scrollTo({ top: 0, behavior: 'smooth' })
    setBaseline(firstPage ? new Set(firstPage.map((e) => e.id)) : null)
    if (!isDefault && streamed.data) {
      qc.setQueryData(keys.siteNewEvents(siteId), 0)
      void query.refetch()
    }
  }

  const apOptions = (aps.data ?? []).map((ap) => ({ value: ap.id, label: ap.name }))
  const filterDefs = [
    ...(fixedAP ? [] : [{ id: 'ap', label: 'Access point', value: search.ap ?? '', options: apOptions, allLabel: 'All APs' }]),
    { id: 'kind', label: 'Kind', value: search.kind ?? '', options: EVENT_KINDS.map((k) => ({ value: k, label: kindLabel(k) })), allLabel: 'All kinds' },
    { id: 'severity', label: 'Severity', value: search.severity ?? '', options: EVENT_SEVERITIES.map((s) => ({ value: s, label: severityLabels[s] })), allLabel: 'Any severity' },
    { id: 'range', label: 'Time', value: search.range ?? '', options: Object.keys(EVENT_RANGES).map((r) => ({ value: r, label: rangeLabels[r] })), allLabel: 'Any time' },
  ]

  const [clientDraft, setClientDraft] = useState(search.client ?? '')
  const [clientSynced, setClientSynced] = useState(search.client ?? '')
  if (clientSynced !== (search.client ?? '')) {
    setClientSynced(search.client ?? '')
    setClientDraft(search.client ?? '')
  }
  const clientInvalid = clientDraft.trim() !== '' && !normaliseMac(clientDraft)

  const exportCsv = () => {
    const header = ['first_seen', 'last_seen', 'severity', 'kind', 'ap', 'title', 'detail', 'count', 'resolved_by', 'client_mac']
    const rows = events.map((e) => [e.first_seen, e.last_seen, e.severity, e.kind, apNames.get(e.ap_id) ?? e.ap_name ?? e.ap_id, e.title, e.detail, e.count, e.resolved_by ?? '', e.client_mac])
    downloadCsv(`events-${siteId}${fixedAP ? `-${fixedAP}` : ''}.csv`, toCsv(header, rows))
  }

  return (
    <div className="grid min-w-0 gap-4">
      <div className="flex min-w-0 flex-wrap items-end justify-between gap-3">
        <FilterBar
          filters={filterDefs}
          onChange={(values) => onSearch({ ...values, before: undefined })}
          onClear={() => onSearch({ ap: undefined, kind: undefined, severity: undefined, range: undefined, client: undefined, before: undefined })}
          extra={
            <form
              className="grid min-w-0 gap-1"
              onSubmit={(event) => {
                event.preventDefault()
                if (clientInvalid) return
                onSearch({ client: normaliseMac(clientDraft) ?? undefined, before: undefined })
              }}
            >
              <label htmlFor="event-client" className="text-xs text-muted-foreground">
                Client MAC
              </label>
              <input
                id="event-client"
                value={clientDraft}
                onChange={(e) => setClientDraft(e.target.value)}
                onBlur={() => !clientInvalid && clientDraft !== (search.client ?? '') && onSearch({ client: normaliseMac(clientDraft) ?? undefined, before: undefined })}
                placeholder="aa-bb-cc-dd-ee-ff"
                autoCapitalize="none"
                autoCorrect="off"
                spellCheck={false}
                aria-invalid={clientInvalid || undefined}
                aria-describedby={clientInvalid ? 'event-client-error' : undefined}
                className={cn(inputClass, 'w-52 font-mono')}
              />
              {clientInvalid ? (
                <span id="event-client-error" role="alert" className="text-xs text-destructive">
                  Enter a MAC address (12 hex digits).
                </span>
              ) : null}
            </form>
          }
        />
        <Button variant="outline" size="sm" onClick={exportCsv} disabled={!events.length}>
          <Download />
          Export CSV
        </Button>
      </div>

      {search.before ? (
        <p className="text-sm text-muted-foreground">
          Showing events before {new Date(search.before).toLocaleString()}.{' '}
          <button type="button" className="inline-flex min-h-11 items-center text-brand underline underline-offset-4 hover:no-underline fine:md:min-h-0" onClick={() => onSearch({ before: undefined })}>
            Show the newest
          </button>
        </p>
      ) : null}

      {pending > 0 ? (
        <div className="sticky top-[calc(3.5rem+env(safe-area-inset-top)+0.5rem)] z-10 flex justify-center">
          <Button size="sm" variant="brand" className="rounded-full shadow-md" onClick={showNew}>
            <ArrowUp />
            New events ({pending})
          </Button>
        </div>
      ) : null}

      {query.isPending ? (
        <SkeletonRows rows={6} />
      ) : query.isError ? (
        <ErrorState error={query.error} title="Could not load events" onRetry={() => void query.refetch()} />
      ) : events.length === 0 ? (
        <EmptyState title={isDefault ? 'No events yet' : 'No events match these filters'}>
          {isDefault ? 'Connections, config deliveries and AP events appear here as they happen.' : 'Try a longer time range or clear the filters.'}
        </EmptyState>
      ) : (
        <>
          <EventList events={events} apNames={apNames} showAP={!fixedAP} onOpen={setOpen} label="Events" />
          {query.hasNextPage ? (
            <Button variant="outline" className="justify-self-center" disabled={query.isFetchingNextPage} onClick={() => void query.fetchNextPage()}>
              {query.isFetchingNextPage ? 'Loading…' : 'Load older events'}
            </Button>
          ) : (
            <p className="text-center text-xs text-muted-foreground">No older events.</p>
          )}
        </>
      )}
      <EventDrawer siteId={siteId} summary={open} onOpenChange={(next) => !next && setOpen(null)} apName={open ? apNames.get(open.ap_id) : undefined} />
    </div>
  )
}
