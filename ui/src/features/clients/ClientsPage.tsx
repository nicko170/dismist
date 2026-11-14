import { useEffect, useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Download, Search } from 'lucide-react'
import type { SessionView } from '@/gen/api.gen'
import { siteAPsQuery } from '@/api/queries/aps'
import { siteClientsQuery, siteRecentQuery } from '@/api/queries/clients'
import { Button } from '@/components/ui/button'
import { inputClass } from '@/components/ui/input'
import { Mono } from '@/components/app/identifiers'
import { PageHeader } from '@/components/app/PageHeader'
import { RelativeTime } from '@/components/app/RelativeTime'
import { EmptyState, ErrorState, SkeletonRows } from '@/components/app/states'
import { formatBytes } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Dash, actionLink } from '@/features/overview/kit/Card'
import { FilterBar, NativeSelect } from '@/features/overview/kit/FilterBar'
import { ResponsiveList, type Column } from '@/features/overview/kit/ResponsiveList'
import { downloadCsv, toCsv } from '@/features/overview/kit/csv'
import { ClientList } from './ClientList'
import { BANDS, SIGNALS, filterClients, signalLabel, sortClients, type ClientFilters, type ClientSort } from './model'

// Clients (design §3.12): the site's live clients in one request (every
// 10 s while visible), or sessions that ended in the last 24 h.

export type ClientsSearch = ClientFilters & { view?: 'recent'; sort?: Exclude<ClientSort, 'signal'> }

function SearchBox({ value, onChange, className }: { value: string; onChange: (q: string | undefined) => void; className?: string }) {
  const [draft, setDraft] = useState(value)
  const [synced, setSynced] = useState(value)
  if (synced !== value) {
    setSynced(value)
    setDraft(value)
  }
  useEffect(() => {
    if (draft === value) return
    const timer = window.setTimeout(() => onChange(draft.trim() || undefined), 250)
    return () => window.clearTimeout(timer)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draft])
  return (
    <label className={cn('relative block', className)}>
      <span className="sr-only">Search clients</span>
      <Search aria-hidden className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
      <input
        type="search"
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        placeholder="Name, MAC or IP"
        autoCapitalize="none"
        autoCorrect="off"
        spellCheck={false}
        className={cn(inputClass, 'pl-9')}
      />
    </label>
  )
}

function RecentList({ siteId, sessions }: { siteId: string; sessions: SessionView[] }) {
  const columns: Column<SessionView>[] = [
    { id: 'mac', header: 'Client', primary: true, render: (s) => (s.ip ? <Mono className="text-xs text-muted-foreground">{s.ip}</Mono> : null), className: 'w-[24%]' },
    { id: 'ap', header: 'AP', render: (s) => s.ap_name || s.ap_id || <Dash /> },
    { id: 'wlan', header: 'WLAN', render: (s) => s.ssid || <Dash /> },
    { id: 'ended', header: 'Ended', render: (s) => <RelativeTime value={s.disconnected_at ?? s.last_seen} /> },
    { id: 'reason', header: 'Reason', priority: 2, render: (s) => s.end_reason || <Dash /> },
    { id: 'usage', header: 'Usage (down / up)', priority: 2, render: (s) => `${formatBytes(s.down_bytes)} / ${formatBytes(s.up_bytes)}` },
  ]
  return (
    <ResponsiveList
      caption="Recent sessions"
      rows={sessions}
      rowKey={(s) => `${s.mac}-${s.connected_at}`}
      columns={columns}
      rowLink={(s) => ({ to: '/sites/$siteId/clients/$mac', params: { siteId, mac: s.mac }, label: s.mac })}
      renderCard={(s, title) => (
        <div className="grid min-w-0 gap-1">
          <span className="min-w-0">{title}</span>
          <p className="text-sm text-muted-foreground">{[s.ssid, s.ap_name, s.ip].filter(Boolean).join(' · ')}</p>
          <p className="text-xs text-muted-foreground">
            Ended <RelativeTime value={s.disconnected_at ?? s.last_seen} />
            {s.end_reason ? ` · ${s.end_reason}` : ''}
          </p>
        </div>
      )}
    />
  )
}

export function ClientsPage({ siteId, search, onSearch }: { siteId: string; search: ClientsSearch; onSearch: (next: Partial<ClientsSearch>) => void }) {
  const recentView = search.view === 'recent'
  const live = useQuery({ ...siteClientsQuery(siteId), enabled: !recentView })
  const recent = useQuery({ ...siteRecentQuery(siteId), enabled: recentView, retry: false })
  const aps = useQuery(siteAPsQuery(siteId))
  const all = useMemo(() => live.data ?? [], [live.data])
  const rows = useMemo(() => sortClients(filterClients(all, search), search.sort ?? 'signal'), [all, search])
  const wlans = useMemo(() => [...new Map(all.filter((c) => c.ssid).map((c) => [c.wlan_id || c.ssid, c.ssid])).entries()], [all])
  const sessions = useMemo(
    () => (recent.data ?? []).filter((s) => (!search.ap || s.ap_id === search.ap) && (!search.q || [s.mac, s.ip, s.ssid].some((v) => v.toLowerCase().includes(search.q!.toLowerCase())))),
    [recent.data, search.ap, search.q],
  )

  const filters = [
    { id: 'ap', label: 'Access point', value: search.ap ?? '', options: (aps.data ?? []).map((ap) => ({ value: ap.id, label: ap.name })), allLabel: 'All APs' },
    ...(recentView
      ? []
      : [
          { id: 'wlan', label: 'WLAN', value: search.wlan ?? '', options: wlans.map(([id, ssid]) => ({ value: id, label: ssid })), allLabel: 'All WLANs' },
          { id: 'band', label: 'Band', value: search.band ?? '', options: BANDS.map((b) => ({ value: b, label: `${b} GHz` })), allLabel: 'Both bands' },
          { id: 'signal', label: 'Signal', value: search.signal ?? '', options: SIGNALS.map((s) => ({ value: s, label: signalLabel[s] })), allLabel: 'Any signal' },
        ]),
  ]

  const exportCsv = () => {
    const header = ['name', 'mac', 'private', 'ip', 'ssid', 'ap', 'band', 'channel', 'rssi', 'snr', 'signal', 'down_bytes', 'up_bytes', 'connected_seconds']
    downloadCsv(
      `clients-${siteId}.csv`,
      toCsv(
        header,
        rows.map((c) => [c.alias || c.hostname || c.name, c.mac, c.private_mac, c.ip, c.ssid, c.ap_name, c.band, c.channel, c.rssi, c.snr ?? '', c.signal, c.down_bytes, c.up_bytes, c.connected_seconds]),
      ),
    )
  }

  const viewLink = (view: 'live' | 'recent', label: string) => (
    <Link
      to="."
      search={((prev: ClientsSearch) => ({ ...prev, view: view === 'recent' ? 'recent' : undefined })) as never}
      replace
      aria-current={(view === 'recent') === recentView ? 'true' : undefined}
      className={cn(
        'inline-flex h-11 items-center rounded-md px-3 text-sm text-muted-foreground transition-colors hover:bg-accent fine:md:h-8',
        (view === 'recent') === recentView && 'bg-accent font-medium text-foreground',
      )}
    >
      {label}
    </Link>
  )

  return (
    <div className="grid min-w-0 gap-5">
      <PageHeader
        title="Clients"
        meta={recentView ? 'Sessions that ended in the last 24 hours.' : `${all.length} connected now.`}
        actions={
          !recentView ? (
            <Button variant="outline" onClick={exportCsv} disabled={!rows.length}>
              <Download />
              Export CSV
            </Button>
          ) : null
        }
      />
      <div className="grid min-w-0 gap-3">
        <div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
          <nav aria-label="Client view" className="flex gap-1">
            {viewLink('live', 'Connected now')}
            {viewLink('recent', 'Recent')}
          </nav>
          {!recentView ? (
            <NativeSelect
              label="Sort by"
              hideLabel
              value={search.sort ?? 'signal'}
              allLabel={null}
              options={[
                { value: 'signal', label: 'Sort: weakest signal first' },
                { value: 'name', label: 'Sort: name' },
                { value: 'usage', label: 'Sort: usage' },
              ]}
              onChange={(v) => onSearch({ sort: v === 'signal' ? undefined : (v as ClientsSearch['sort']) })}
              className="w-56"
            />
          ) : null}
        </div>
        <div className="flex min-w-0 flex-wrap items-end gap-3">
          <SearchBox value={search.q ?? ''} onChange={(q) => onSearch({ q })} className="w-full md:w-64" />
          <FilterBar
            filters={filters}
            onChange={(values) => onSearch(values)}
            onClear={() => onSearch({ ap: undefined, wlan: undefined, band: undefined, signal: undefined })}
          />
        </div>
      </div>

      {recentView ? (
        recent.isPending ? (
          <SkeletonRows rows={4} />
        ) : recent.isError ? (
          <ErrorState error={recent.error} title="Could not load recent sessions" onRetry={() => void recent.refetch()} />
        ) : sessions.length ? (
          <RecentList siteId={siteId} sessions={sessions} />
        ) : (
          <EmptyState title="No sessions ended in the last 24 hours" />
        )
      ) : live.isPending ? (
        <SkeletonRows rows={5} />
      ) : live.isError ? (
        <ErrorState error={live.error} title="Could not load clients" onRetry={() => void live.refetch()} />
      ) : all.length === 0 ? (
        <EmptyState
          title="No clients connected right now"
          action={
            <Link to="." search={{ view: 'recent' } as never} className={actionLink}>
              See recent sessions
            </Link>
          }
        />
      ) : rows.length === 0 ? (
        <EmptyState title="No clients match">
          <Link to="." search={{} as never} data-inline-link className="text-brand underline underline-offset-4 hover:no-underline">
            Clear the search and filters
          </Link>
        </EmptyState>
      ) : (
        <ClientList clients={rows} siteId={siteId} />
      )}
    </div>
  )
}
