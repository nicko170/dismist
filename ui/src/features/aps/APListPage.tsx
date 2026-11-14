import { useEffect, useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Plus, Search } from 'lucide-react'
import type { APView } from '@/gen/api.gen'
import { siteAPsQuery } from '@/api/queries/aps'
import { useStreamFallbackInterval } from '@/api/sse'
import { buttonVariants } from '@/components/ui/button'
import { inputClass } from '@/components/ui/input'
import { Chip } from '@/components/app/StatusChip'
import { Mono } from '@/components/app/identifiers'
import { PageHeader } from '@/components/app/PageHeader'
import { RelativeTime } from '@/components/app/RelativeTime'
import { EmptyState, ErrorState, SkeletonRows } from '@/components/app/states'
import { formatDuration } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Dash, actionLink } from '@/features/overview/kit/Card'
import { NativeSelect } from '@/features/overview/kit/FilterBar'
import { ResponsiveList, type Column } from '@/features/overview/kit/ResponsiveList'
import { APStatus, PushChip } from './APChips'
import { usePending, usePushStates } from './hooks'
import { channelsText, filterAPs, needsAttention, radioFor, sortAPs, type APFilter, type APSort } from './model'
import { PendingAPs } from './PendingAPs'

// AP list (design §3.4): one request for the site, patched in place by the
// stream; status filter, search and sort live in the URL.

export type APListSearch = { status?: APFilter; q?: string; sort?: Exclude<APSort, 'name'> }

function Nat({ ap }: { ap: APView }) {
  if (!ap.remote_address) return <Dash />
  return (
    <span className="inline-flex min-w-0 flex-wrap items-center gap-1.5">
      <Mono>{ap.remote_address}</Mono>
      {ap.behind_nat ? <Chip tone="idle">via NAT</Chip> : null}
    </span>
  )
}

function Channels({ ap }: { ap: APView }) {
  if (!ap.connected) return <Dash />
  const r24 = radioFor(ap.radios, '2.4')
  const r5 = radioFor(ap.radios, '5')
  return (
    <span className="tabular-nums">
      {r24?.channel ?? '–'} <span className="text-muted-foreground">/</span> {r5?.channel ? `${r5.channel}/${r5.width || 20}` : '–'}
    </span>
  )
}

function PhoneSearch({ value, onChange }: { value: string; onChange: (q: string | undefined) => void }) {
  const [draft, setDraft] = useState(value)
  const [synced, setSynced] = useState(value)
  if (synced !== value) {
    // The URL changed under us (Back, a cleared filter): adopt it.
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
    <label className="relative block md:hidden">
      <span className="sr-only">Search access points</span>
      <Search aria-hidden className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
      <input
        type="search"
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        placeholder="Name, ID or IP"
        autoCapitalize="none"
        autoCorrect="off"
        spellCheck={false}
        className={cn(inputClass, 'pl-9')}
      />
    </label>
  )
}

export function APListPage({ siteId, search, onSearch }: { siteId: string; search: APListSearch; onSearch: (next: Partial<APListSearch>) => void }) {
  const fallback = useStreamFallbackInterval()
  const aps = useQuery({ ...siteAPsQuery(siteId), refetchInterval: fallback })
  const pending = usePending()
  const push = usePushStates(siteId)
  const all = useMemo(() => aps.data ?? [], [aps.data])
  const rows = useMemo(() => sortAPs(filterAPs(all, { status: search.status, q: search.q }), search.sort ?? 'name'), [all, search])
  const counts = {
    all: all.length,
    online: all.filter((ap) => ap.connected).length,
    offline: all.filter((ap) => !ap.connected).length,
    attention: all.filter(needsAttention).length,
  }
  const pendingRows = pending.data ?? []

  const columns: Column<APView>[] = [
    { id: 'name', header: 'Name', primary: true, render: (ap) => <Mono className="text-xs text-muted-foreground">{ap.id}</Mono>, className: 'w-[24%] lg:w-[20%]' },
    {
      id: 'status',
      header: 'Status',
      render: (ap) => (
        <span className="flex flex-wrap gap-1">
          <APStatus ap={ap} />
          <PushChip ap={ap} push={push[ap.id]} />
        </span>
      ),
      className: 'w-[19%] lg:w-[15%]',
    },
    { id: 'clients', header: 'Clients', align: 'end', render: (ap) => (ap.connected ? ap.client_count : <Dash />), className: 'w-[10%] lg:w-[8%]' },
    { id: 'channels', header: 'Channels (2.4 / 5)', render: (ap) => <Channels ap={ap} />, className: 'w-[19%] lg:w-[13%]' },
    { id: 'lan', header: 'LAN IP', render: (ap) => (ap.lan_ip ? <Mono>{ap.lan_ip}</Mono> : <Dash />) },
    { id: 'public', header: 'Public address', priority: 2, render: (ap) => <Nat ap={ap} /> },
    { id: 'uptime', header: 'Uptime', priority: 2, render: (ap) => (ap.connected && ap.uptime_seconds ? formatDuration(ap.uptime_seconds) : <Dash />), className: 'lg:w-[9%]' },
    {
      id: 'model',
      header: 'Model · Firmware',
      priority: 3,
      render: (ap) => (
        <span className="text-xs break-words">
          {ap.model || 'Unknown'}
          {ap.firmware ? <span className="block text-muted-foreground">{ap.firmware}</span> : null}
        </span>
      ),
    },
    { id: 'seen', header: 'Last seen', priority: 3, render: (ap) => (ap.connected ? 'Now' : <RelativeTime value={ap.last_seen} />), className: 'xl:w-[9%]' },
  ]

  const statusLink = (value: APFilter | undefined, label: string, count: number) => (
    <Link
      key={label}
      to="."
      search={((prev: APListSearch) => ({ ...prev, status: value })) as never}
      replace
      aria-current={search.status === value ? 'true' : undefined}
      className={cn(
        'inline-flex h-11 items-center gap-1.5 rounded-md px-3 text-sm text-muted-foreground transition-colors hover:bg-accent fine:md:h-8',
        search.status === value && 'bg-accent font-medium text-foreground',
      )}
    >
      {label} <span className="tabular-nums">{count}</span>
    </Link>
  )

  return (
    <div className="grid min-w-0 gap-5">
      <PageHeader
        title="Access points"
        actions={
          <Link to={'/onboard' as never} search={{ site: siteId } as never} className={buttonVariants({ variant: 'brand' })}>
            <Plus />
            Add AP
          </Link>
        }
      />
      {pendingRows.length ? (
        <div role="status" className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-status-warn/50 bg-status-warn/10 px-4 py-2.5 text-sm">
          <span>{pendingRows.length === 1 ? '1 AP is trying to connect.' : `${pendingRows.length} APs are trying to connect.`}</span>
          <a href="#pending" className={cn(actionLink, 'font-medium')}>
            Review
          </a>
        </div>
      ) : null}

      <div className="grid min-w-0 gap-3 md:flex md:items-end md:justify-between">
        <PhoneSearch value={search.q ?? ''} onChange={(q) => onSearch({ q })} />
        <nav aria-label="Filter by status" className="-mx-1 flex min-w-0 flex-wrap gap-1">
          {statusLink(undefined, 'All', counts.all)}
          {statusLink('online', 'Online', counts.online)}
          {statusLink('offline', 'Offline', counts.offline)}
          {statusLink('attention', 'Attention', counts.attention)}
        </nav>
        <NativeSelect
          label="Sort by"
          value={search.sort ?? 'name'}
          allLabel={null}
          options={[
            { value: 'name', label: 'Name' },
            { value: 'status', label: 'Status (worst first)' },
            { value: 'clients', label: 'Clients' },
            { value: 'uptime', label: 'Uptime' },
          ]}
          onChange={(value) => onSearch({ sort: value === 'name' ? undefined : (value as APListSearch['sort']) })}
          className="md:w-52"
        />
      </div>

      {aps.isPending ? (
        <SkeletonRows rows={4} />
      ) : aps.isError ? (
        <ErrorState error={aps.error} title="Could not load access points" onRetry={() => void aps.refetch()} />
      ) : all.length === 0 ? (
        <EmptyState
          title="No APs in this site"
          action={
            <Link to={'/onboard' as never} search={{ site: siteId } as never} className={buttonVariants({ variant: 'brand' })}>
              Add AP
            </Link>
          }
        >
          Prepare an AP with the CLI, or register one that is trying to connect.
        </EmptyState>
      ) : rows.length === 0 ? (
        <EmptyState title="No access points match">
          <Link to="." search={{} as never} data-inline-link className="text-brand underline underline-offset-4 hover:no-underline">
            Clear the search and filters
          </Link>
        </EmptyState>
      ) : (
        <ResponsiveList
          caption="Access points"
          rows={rows}
          rowKey={(ap) => ap.id}
          columns={columns}
          rowLink={(ap) => ({ to: '/sites/$siteId/aps/$apId', params: { siteId, apId: ap.id }, label: ap.name })}
          renderCard={(ap, title) => (
            <div className="grid min-w-0 gap-1">
              <div className="flex min-w-0 items-center justify-between gap-2">
                <span className="min-w-0 truncate">{title}</span>
                <APStatus ap={ap} />
              </div>
              <p className="text-sm text-muted-foreground tabular-nums">
                {ap.connected ? [`${ap.client_count} clients`, channelsText(ap.radios)].filter(Boolean).join(' · ') : 'Not connected'}
              </p>
              <p className="text-xs break-words text-muted-foreground">
                {[
                  ap.lan_ip || 'No LAN IP yet',
                  ap.behind_nat && ap.remote_address ? `via ${ap.remote_address} NAT` : '',
                  ap.connected && ap.uptime_seconds ? `up ${formatDuration(ap.uptime_seconds).split(' ').slice(0, 2).join(' ')}` : '',
                ]
                  .filter(Boolean)
                  .join(' · ')}
              </p>
              <PushChip ap={ap} push={push[ap.id]} />
            </div>
          )}
        />
      )}
      <PendingAPs pending={pendingRows} siteId={siteId} />
    </div>
  )
}
