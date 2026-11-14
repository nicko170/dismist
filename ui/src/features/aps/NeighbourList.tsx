import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import type { Neighbour } from '@/api/queries/aps'
import { neighboursQuery } from '@/api/queries/aps'
import { Mono } from '@/components/app/identifiers'
import { RelativeTime } from '@/components/app/RelativeTime'
import { ErrorState, SkeletonRows } from '@/components/app/states'
import { ResponsiveList, type Column } from '@/features/overview/kit/ResponsiveList'
import { SignalChip } from '@/features/overview/kit/SignalChip'
import { bandName } from './format'
import { formatUtil } from '@/lib/format'

// Neighbouring BSSs heard by the scanning radio (design §5.10, phase 3).
// Neighbour SSIDs and BSSIDs stay in the authenticated UI (P7). Reusable for
// the site-level /rf/neighbours screen (workstream F's route).

export function NeighbourTable({ rows, showSeenBy = true, apNames }: { rows: Neighbour[]; showSeenBy?: boolean; apNames?: Map<string, string> }) {
  const columns: Column<Neighbour>[] = [
    { id: 'ssid', header: 'SSID', render: (n) => <span className="font-medium break-words">{n.ssid || <span className="text-muted-foreground">Hidden</span>}</span>, className: 'w-[22%]' },
    { id: 'bssid', header: 'BSSID', render: (n) => <Mono className="text-xs">{n.bssid}</Mono> },
    { id: 'chan', header: 'Channel', render: (n) => `${bandName(n.band)} · ${n.channel}${n.width ? `/${n.width}` : ''}` },
    { id: 'rssi', header: 'Signal', render: (n) => <SignalChip rssi={n.rssi} /> },
    ...(showSeenBy ? [{ id: 'seen', header: 'Heard by', priority: 2 as const, render: (n: Neighbour) => apNames?.get(n.seen_by) ?? n.seen_by }] : []),
    { id: 'util', header: 'Load', priority: 2, render: (n) => [n.station_count != null ? `${n.station_count} stations` : '', n.util != null ? formatUtil(n.util) : ''].filter(Boolean).join(' · ') || '–' },
    { id: 'last', header: 'Last seen', priority: 3, render: (n) => <RelativeTime value={n.last_seen} /> },
  ]
  return (
    <ResponsiveList
      caption="Neighbouring networks"
      rows={rows}
      rowKey={(n) => `${n.seen_by}-${n.bssid}`}
      columns={columns}
      renderCard={(n) => (
        <div className="grid min-w-0 gap-1">
          <div className="flex min-w-0 items-center justify-between gap-2">
            <span className="min-w-0 truncate font-medium">{n.ssid || 'Hidden network'}</span>
            <SignalChip rssi={n.rssi} />
          </div>
          <p className="text-xs text-muted-foreground">
            {bandName(n.band)} ch {n.channel}
            {n.width ? `/${n.width}` : ''} · <Mono>{n.bssid}</Mono>
          </p>
          {showSeenBy ? <p className="text-xs text-muted-foreground">Heard by {apNames?.get(n.seen_by) ?? n.seen_by}</p> : null}
        </div>
      )}
    />
  )
}

export function APNeighbours({ siteId, apId }: { siteId: string; apId: string }) {
  const query = useQuery({ ...neighboursQuery(siteId, apId), retry: false })
  // The server filters on ?ap=; the rows are filtered here too, so this
  // tab never shows what another AP's scanning radio heard.
  const rows = useMemo(() => (query.data ?? []).filter((n) => !n.seen_by || n.seen_by === apId), [query.data, apId])
  if (query.isPending) return <SkeletonRows rows={2} />
  if (query.isError) {
    const status = (query.error as { status?: number }).status
    if (status === 404 || status === 501) return <p className="text-sm text-muted-foreground">Neighbour scans are not available from this controller yet.</p>
    return <ErrorState error={query.error} title="Could not load neighbour scans" onRetry={() => void query.refetch()} />
  }
  if (!rows.length) return <p className="text-sm text-muted-foreground">No neighbouring networks reported yet. The scanning radio reports them every few minutes.</p>
  return <NeighbourTable rows={rows} showSeenBy={false} />
}
