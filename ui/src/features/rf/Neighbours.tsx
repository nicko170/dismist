import { useMemo } from 'react'
import { Link, useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { ArrowLeft, Radar, ShieldCheck } from 'lucide-react'
import { neighboursQuery, rfQuery, type Neighbour } from '@/api/queries/rf'
import type { Band } from '@/gen/api.gen'
import { PageHeader } from '@/components/app/PageHeader'
import { EmptyState, ErrorState, SkeletonRows } from '@/components/app/states'
import { Mono } from '@/components/app/identifiers'
import { RelativeTime } from '@/components/app/RelativeTime'
import { buttonVariants } from '@/components/ui/button'
import { Segmented } from '@/features/settings/kit/primitives'
import { cn } from '@/lib/utils'
import { formatUtil } from '@/lib/format'

// Neighbour scan (design §3.10 "Neighbours", §5.10): a per-band channel
// histogram with the site's own channels marked, and the neighbour table.
// Neighbour SSIDs are other people's data: shown here only.

type Bin = { channel: number; count: number; strongest: number | null; own: string[] }

export function histogram(neighbours: Neighbour[], ownChannels: Map<number, string[]>): Bin[] {
  const bins = new Map<number, Bin>()
  for (const n of neighbours) {
    const bin = bins.get(n.channel) ?? { channel: n.channel, count: 0, strongest: null, own: [] }
    bin.count++
    bin.strongest = bin.strongest == null ? n.rssi : Math.max(bin.strongest, n.rssi)
    bins.set(n.channel, bin)
  }
  for (const [channel, aps] of ownChannels) {
    const bin = bins.get(channel) ?? { channel, count: 0, strongest: null, own: [] }
    bin.own = aps
    bins.set(channel, bin)
  }
  return [...bins.values()].toSorted((a, b) => a.channel - b.channel)
}

export function NeighboursPage({ siteId, band }: { siteId: string; band?: Band }) {
  const navigate = useNavigate()
  const scans = useQuery(neighboursQuery(siteId, band))
  const rf = useQuery(rfQuery(siteId))
  const own = useMemo(() => {
    const map = new Map<number, string[]>()
    for (const row of rf.data?.plan ?? []) {
      if (band && row.band !== band) continue
      const channel = row.actual?.channel ?? row.channel
      map.set(channel, [...(map.get(channel) ?? []), row.ap_name])
    }
    return map
  }, [rf.data, band])
  const list = (scans.data?.neighbours ?? []).filter((n) => !band || n.band === band).toSorted((a, b) => b.rssi - a.rssi)
  const bins = histogram(list, own)
  const max = Math.max(1, ...bins.map((b) => b.count))

  return (
    <div className="grid gap-5">
      <Link to={`/sites/${encodeURIComponent(siteId)}/rf` as never} className={buttonVariants({ variant: 'ghost', size: 'sm', className: 'justify-self-start' })}>
        <ArrowLeft /> Radio settings
      </Link>
      <PageHeader title="Neighbours" />
      <p className="flex items-start gap-2 text-sm text-muted-foreground">
        <ShieldCheck aria-hidden className="mt-0.5 size-4 shrink-0" />
        Neighbouring networks are other people’s data. They are shown here only and never exported to metrics.
      </p>
      <Segmented
        aria-label="Band"
        value={band ?? 'all'}
        onValueChange={(v) => void navigate({ to: '.' as never, search: (v === 'all' ? {} : { band: v }) as never, replace: true })}
        options={[
          { value: 'all', label: 'Both bands' },
          { value: '2.4', label: '2.4 GHz' },
          { value: '5', label: '5 GHz' },
        ]}
        className="justify-self-start"
      />
      {scans.isError ? (
        <ErrorState error={scans.error} title="Could not load the neighbour scans" onRetry={() => void scans.refetch()} />
      ) : !scans.data ? (
        <SkeletonRows rows={3} />
      ) : (
        <>
          {scans.data.scans.length ? (
            <p className="text-sm text-muted-foreground">
              Last scan:{' '}
              {scans.data.scans.map((s, i) => (
                <span key={s.ap_id}>
                  {s.ap_name} <RelativeTime value={s.at} fallback="none yet" />
                  {i < scans.data!.scans.length - 1 ? ', ' : ''}
                </span>
              ))}
              . Scans arrive in about 1 in 10 full telemetry frames.
            </p>
          ) : null}
          <section aria-labelledby="histogram-heading" className="grid gap-3 rounded-xl border bg-card p-4 md:p-5">
            <h2 id="histogram-heading" className="text-base font-semibold">
              Networks per channel
            </h2>
            {bins.length ? (
              <ul className="grid gap-1.5" aria-label="Neighbour networks per channel">
                {bins.map((bin) => (
                  <li
                    key={bin.channel}
                    className="grid grid-cols-[3.5rem_minmax(0,1fr)] items-center gap-3 text-sm"
                    title={`Channel ${bin.channel}: ${bin.count} network${bin.count === 1 ? '' : 's'}${bin.strongest != null ? `, strongest ${bin.strongest} dBm` : ''}${bin.own.length ? `; your AP: ${bin.own.join(', ')}` : ''}`}
                  >
                    <span className="text-right font-medium tabular-nums">ch {bin.channel}</span>
                    <span className="flex min-w-0 flex-wrap items-center gap-2">
                      <span
                        aria-hidden
                        className={cn('h-3 min-w-1 rounded-r-[4px] bg-chart-2', bin.count === 0 && 'bg-transparent')}
                        style={{ width: `${Math.max(bin.count ? 4 : 0, (bin.count / max) * 60)}%` }}
                      />
                      <span className="tabular-nums">
                        {bin.count} network{bin.count === 1 ? '' : 's'}
                        {bin.strongest != null ? <span className="text-muted-foreground"> · strongest {bin.strongest} dBm</span> : null}
                      </span>
                      {bin.own.length ? (
                        <span className="inline-flex items-center gap-1 rounded-full border border-brand/40 bg-brand-soft px-2 text-xs font-medium">
                          <Radar aria-hidden className="size-3" /> Your AP: {bin.own.join(', ')}
                        </span>
                      ) : null}
                    </span>
                  </li>
                ))}
              </ul>
            ) : (
              <EmptyState title="No neighbour networks heard yet." />
            )}
          </section>
          <NeighbourTable list={list} />
        </>
      )}
    </div>
  )
}

function NeighbourTable({ list }: { list: Neighbour[] }) {
  if (!list.length) return null
  return (
    <section aria-labelledby="neighbours-heading" className="grid gap-3">
      <h2 id="neighbours-heading" className="text-base font-semibold">
        {list.length} neighbour network{list.length === 1 ? '' : 's'}
      </h2>
      <ul className="grid gap-2 md:hidden">
        {list.map((n) => (
          <li key={`${n.bssid}:${n.seen_by}`} className="grid gap-1 rounded-lg border bg-card p-3 text-sm">
            <span className="font-medium break-words">{n.ssid || <span className="text-muted-foreground">(hidden)</span>}</span>
            <Mono className="text-xs">{n.bssid}</Mono>
            <span className="text-muted-foreground">
              ch {n.channel}/{n.width} · {n.rssi} dBm · seen by {n.seen_by}
              {n.station_count != null ? ` · ${n.station_count} clients` : ''}
            </span>
          </li>
        ))}
      </ul>
      <div className="hidden overflow-hidden rounded-xl border bg-card md:block">
        <table className="w-full text-sm">
          <thead className="border-b bg-muted/50 text-left text-xs text-muted-foreground">
            <tr>
              <th scope="col" className="px-4 py-2.5 font-medium">SSID</th>
              <th scope="col" className="hidden px-4 py-2.5 font-medium lg:table-cell">BSSID</th>
              <th scope="col" className="px-4 py-2.5 font-medium">Channel</th>
              <th scope="col" className="px-4 py-2.5 text-right font-medium">Signal</th>
              <th scope="col" className="px-4 py-2.5 font-medium">Seen by</th>
              <th scope="col" className="hidden px-4 py-2.5 text-right font-medium xl:table-cell">Load</th>
              <th scope="col" className="hidden px-4 py-2.5 font-medium xl:table-cell">Last seen</th>
            </tr>
          </thead>
          <tbody className="divide-y">
            {list.map((n) => (
              <tr key={`${n.bssid}:${n.seen_by}`}>
                <td className="px-4 py-2.5 break-words">{n.ssid || <span className="text-muted-foreground">(hidden)</span>}</td>
                <td className="hidden px-4 py-2.5 lg:table-cell">
                  <Mono>{n.bssid}</Mono>
                </td>
                <td className="px-4 py-2.5 tabular-nums">
                  {n.channel}/{n.width}
                </td>
                <td className="px-4 py-2.5 text-right tabular-nums">{n.rssi} dBm</td>
                <td className="px-4 py-2.5">{n.seen_by}</td>
                <td className="hidden px-4 py-2.5 text-right tabular-nums xl:table-cell">
                  {n.station_count != null ? `${n.station_count} clients` : '–'}
                  {n.util != null ? ` · ${formatUtil(n.util)}` : ''}
                </td>
                <td className="hidden px-4 py-2.5 xl:table-cell">
                  <RelativeTime value={n.last_seen} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  )
}
