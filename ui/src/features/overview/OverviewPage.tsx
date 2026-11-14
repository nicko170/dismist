import { useMemo, useState } from 'react'
import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { AudioWaveform, RadioTower, Smartphone, Wifi } from 'lucide-react'
import type { EventView } from '@/gen/api.gen'
import { RANGES, samplesQuery, siteAPsQuery, siteClientSeries, type HistoryRange } from '@/api/queries/aps'
import { recentEventsQuery } from '@/api/queries/events'
import { systemQuery } from '@/api/queries/system'
import { buttonVariants } from '@/components/ui/button'
import { PageHeader } from '@/components/app/PageHeader'
import { EmptyState, ErrorState, SkeletonRows } from '@/components/app/states'
import { sitesQuery } from '@/features/shell/queries'
import { useNow } from '@/lib/time'
import { cn } from '@/lib/utils'
import { APStatus, PushChip } from '@/features/aps/APChips'
import { usePending, usePushStates } from '@/features/aps/hooks'
import { apPath, bandCounts, channelsText, siteChannels, worstFirst } from '@/features/aps/model'
import { EventDrawer } from '@/features/events/EventDrawer'
import { EventList } from '@/features/events/EventList'
import { AttentionCard } from './AttentionCard'
import { GlobalSearch } from './GlobalSearch'
import { attentionItems } from './attention'
import { Card, actionLink } from './kit/Card'
import { StatTile } from './kit/StatTile'
import { TimeSeries } from './kit/TimeSeries'
import { ccrevFor, recommendationCountQuery, siteViewQuery } from './site'

// Site overview (design §3.3): "is my Wi-Fi OK, and if not, what needs
// attention?" in one screen. The AP list is the stream-patched site list;
// the chart is the site sum of the stored samples.

const rangeLabel: Record<HistoryRange, string> = { '1h': '1 h', '24h': '24 h', '7d': '7 d' }

export function OverviewPage({ siteId, range }: { siteId: string; range: HistoryRange }) {
  const now = useNow()
  const sites = useQuery(sitesQuery)
  const summary = sites.data?.find((s) => s.id === siteId)
  const site = useQuery({ ...siteViewQuery(siteId), retry: false })
  const aps = useQuery(siteAPsQuery(siteId))
  const system = useQuery({ ...systemQuery, refetchInterval: false, retry: false })
  const pending = usePending()
  const push = usePushStates(siteId)
  const recs = useQuery(recommendationCountQuery(siteId))
  const events = useInfiniteQuery(recentEventsQuery(siteId))
  const samples = useQuery({ ...samplesQuery(siteId, { range }), retry: false })
  const [openEvent, setOpenEvent] = useState<EventView | null>(null)

  const list = useMemo(() => aps.data ?? [], [aps.data])
  const recent = useMemo(() => (events.data?.pages[0]?.events ?? []).slice(0, 10), [events.data])
  const apNames = useMemo(() => new Map(list.map((ap) => [ap.id, ap.name])), [list])
  const items = useMemo(
    () =>
      attentionItems({
        siteId,
        aps: list,
        now,
        pendingCount: pending.data?.length ?? summary?.counts.pending_aps ?? 0,
        system: system.data,
        events: events.data?.pages[0]?.events,
        recommendations: recs.data ?? 0,
        push,
      }),
    [siteId, list, now, pending.data, summary, system.data, events.data, recs.data, push],
  )

  const name = site.data?.name ?? summary?.name ?? siteId
  const country = site.data?.country ?? summary?.country ?? ''
  const timezone = site.data?.timezone ?? summary?.timezone ?? ''
  const ccrev = country ? ccrevFor(country, site.data?.ccrev) : ''
  const online = list.filter((ap) => ap.connected).length
  const offline = list.length - online
  const clients = list.reduce((sum, ap) => sum + (ap.connected ? ap.client_count : 0), 0)
  const bands = bandCounts(list)
  const wlans = site.data?.counts?.wlans ?? summary?.counts.wlans ?? 0
  const series = useMemo(() => siteClientSeries(samples.data ?? [], range, now), [samples.data, range, now])

  return (
    <div className="grid min-w-0 gap-5">
      <PageHeader title={name} meta={[country, timezone, ccrev].filter(Boolean).join(' · ')} actions={<GlobalSearch siteId={siteId} />} />

      <AttentionCard items={items} />

      {aps.isError ? <ErrorState error={aps.error} title="Could not load the site’s access points" onRetry={() => void aps.refetch()} /> : null}

      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <StatTile
          label="Access points"
          icon={<RadioTower />}
          value={aps.isPending ? '…' : `${online}/${list.length}`}
          detail={offline ? `${offline} offline` : list.length ? 'All online' : 'None yet'}
          tone={offline ? 'crit' : undefined}
          to={`/sites/${siteId}/aps`}
          search={offline ? { status: 'offline' } : undefined}
        />
        <StatTile
          label="Clients"
          icon={<Smartphone />}
          value={aps.isPending ? '…' : clients}
          detail={`2.4 GHz ${bands.b24} · 5 GHz ${bands.b5}`}
          to={`/sites/${siteId}/clients`}
        />
        <StatTile label="WLANs" icon={<Wifi />} value={wlans} detail="enabled" to={`/sites/${siteId}/wlans`} />
        <StatTile label="Channels" icon={<AudioWaveform />} value={siteChannels(list) || '–'} to={`/sites/${siteId}/rf`} />
      </div>

      {!aps.isPending && list.length === 0 ? (
        <EmptyState
          title="This site has no access points yet"
          action={
            <Link to={'/onboard' as never} search={{ site: siteId } as never} className={buttonVariants({ variant: 'brand' })}>
              Add an AP
            </Link>
          }
        >
          Prepare an AP with the CLI, or adopt one that is already trying to connect.
        </EmptyState>
      ) : null}
      {!aps.isPending && wlans === 0 && summary ? (
        <EmptyState
          title="Create a WLAN so the APs have something to broadcast"
          action={
            <Link to={`/sites/${siteId}/wlans/new` as never} className={buttonVariants({ variant: 'outline' })}>
              Create a WLAN
            </Link>
          }
        />
      ) : null}

      <Card
        title={`Clients, ${rangeLabel[range]}`}
        action={
          <div role="group" aria-label="Chart range" className="flex rounded-md border p-0.5">
            {(Object.keys(RANGES) as HistoryRange[]).map((r) => (
              <Link
                key={r}
                to="."
                search={((prev: Record<string, unknown>) => ({ ...prev, range: r === '24h' ? undefined : r })) as never}
                replace
                aria-current={r === range ? 'true' : undefined}
                className={cn(
                  'inline-flex h-11 min-w-11 items-center justify-center rounded px-2 text-xs text-muted-foreground fine:md:h-7',
                  r === range && 'bg-accent font-medium text-foreground',
                )}
              >
                {rangeLabel[r]}
              </Link>
            ))}
          </div>
        }
      >
        {samples.isPending ? (
          <SkeletonRows rows={1} className="[&>*]:h-44" />
        ) : samples.isError ? (
          <p className="text-sm text-muted-foreground">Client history is not available from this controller yet.</p>
        ) : series.every((p) => p.v === null) ? (
          <p className="text-sm text-muted-foreground">No client history in this range yet.</p>
        ) : (
          <TimeSeries points={series} unit="clients" label={`Clients connected to ${name}, last ${rangeLabel[range]}`} />
        )}
      </Card>

      <div className="grid min-w-0 gap-5 lg:grid-cols-2">
        <Card
          title="Access points"
          action={
            <Link to="/sites/$siteId/aps" params={{ siteId }} className={actionLink}>
              All access points
            </Link>
          }
          bodyClassName="p-0"
        >
          {aps.isPending ? (
            <SkeletonRows rows={3} className="p-4" />
          ) : list.length ? (
            <ul className="divide-y">
              {worstFirst(list)
                .slice(0, 5)
                .map((ap) => (
                  <li key={ap.id} className="relative flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 px-4 py-3 has-[a:hover]:bg-accent/50">
                    <Link
                      to={apPath(ap) as never}
                      className="flex min-h-11 min-w-0 flex-1 items-center font-medium after:absolute after:inset-0 hover:underline fine:md:min-h-0"
                    >
                      <span className="truncate">{ap.name}</span>
                    </Link>
                    <APStatus ap={ap} />
                    <PushChip ap={ap} push={push[ap.id]} />
                    <span className="w-full text-xs text-muted-foreground tabular-nums">
                      {ap.connected ? `${ap.client_count} clients${channelsText(ap.radios) ? ` · ${channelsText(ap.radios)}` : ''}` : ap.lan_ip || 'No address yet'}
                    </span>
                  </li>
                ))}
            </ul>
          ) : (
            <p className="p-4 text-sm text-muted-foreground">No access points yet.</p>
          )}
        </Card>

        <Card
          title="Recent events"
          action={
            <Link to="/sites/$siteId/events" params={{ siteId }} className={actionLink}>
              All events
            </Link>
          }
          bodyClassName="p-0"
        >
          {events.isPending ? (
            <SkeletonRows rows={3} className="p-4" />
          ) : events.isError ? (
            <div className="p-4">
              <ErrorState error={events.error} title="Could not load events" onRetry={() => void events.refetch()} />
            </div>
          ) : recent.length ? (
            <EventList events={recent} apNames={apNames} onOpen={setOpenEvent} label="Recent events" className="rounded-none border-0" />
          ) : (
            <p className="p-4 text-sm text-muted-foreground">No events yet.</p>
          )}
        </Card>
      </div>
      <EventDrawer
        siteId={siteId}
        summary={openEvent}
        onOpenChange={(next) => !next && setOpenEvent(null)}
        apName={openEvent ? apNames.get(openEvent.ap_id) : undefined}
      />
    </div>
  )
}
