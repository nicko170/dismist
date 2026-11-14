import type { ClientView } from '@/gen/api.gen'
import { Chip } from '@/components/app/StatusChip'
import { Mono } from '@/components/app/identifiers'
import { formatDuration } from '@/lib/format'
import { Dash } from '@/features/overview/kit/Card'
import { ResponsiveList, type Column } from '@/features/overview/kit/ResponsiveList'
import { SignalChip } from '@/features/overview/kit/SignalChip'
import { bandText, displayName } from './model'

// Live clients as a table (md+) or cards (phones), each row a link to the
// client detail (design §3.12).

export function PrivateBadge({ show }: { show: boolean }) {
  if (!show) return null
  return (
    <Chip tone="idle" className="h-5 text-[0.6875rem]">
      <span title="A randomised (locally administered) address">private</span>
    </Chip>
  )
}

export function IPValue({ client }: { client: Pick<ClientView, 'ip' | 'self_assigned'> }) {
  if (!client.ip) return <Dash />
  return (
    <span className="inline-flex min-w-0 flex-wrap items-center gap-1.5">
      <Mono>{client.ip}</Mono>
      {client.self_assigned ? <Chip tone="crit">self-assigned (DHCP failing)</Chip> : null}
    </span>
  )
}

const short = (seconds: number) => formatDuration(seconds).split(' ').slice(0, 2).join(' ')

export function ClientList({ clients, siteId, showAP = true }: { clients: ClientView[]; siteId: string; showAP?: boolean }) {
  const columns: Column<ClientView>[] = [
    {
      id: 'name',
      header: 'Name',
      primary: true,
      className: 'w-[22%] lg:w-[18%]',
      render: (c) => (
        <span className="flex min-w-0 flex-wrap items-center gap-1.5 text-xs text-muted-foreground xl:hidden">
          <Mono>{c.mac}</Mono>
          <PrivateBadge show={c.private_mac} />
        </span>
      ),
    },
    { id: 'signal', header: 'Signal', render: (c) => <SignalChip compact signal={c.signal} rssi={c.rssi} />, className: 'w-[22%] lg:w-[17%] whitespace-nowrap' },
    { id: 'band', header: 'Band', render: (c) => bandText(c) || <Dash /> },
    ...(showAP ? [{ id: 'ap', header: 'AP', render: (c: ClientView) => c.ap_name || <Dash /> }] : []),
    { id: 'ip', header: 'IP', render: (c) => <IPValue client={c} /> },
    { id: 'wlan', header: 'WLAN', priority: 2, render: (c) => c.ssid || <Dash /> },
    { id: 'for', header: 'Connected for', priority: 2, render: (c) => (c.connected_seconds ? short(c.connected_seconds) : <Dash />) },
    {
      id: 'mac',
      header: 'MAC',
      priority: 3,
      render: (c) => (
        <span className="flex min-w-0 flex-wrap items-center gap-1.5">
          <Mono className="text-xs">{c.mac}</Mono>
          <PrivateBadge show={c.private_mac} />
        </span>
      ),
    },
  ]
  return (
    <ResponsiveList
      caption="Clients"
      rows={clients}
      rowKey={(c) => c.mac}
      columns={columns}
      rowLink={(c) => ({ to: '/sites/$siteId/clients/$mac', params: { siteId, mac: c.mac }, label: displayName(c) })}
      renderCard={(c, title) => (
        <div className="grid min-w-0 gap-1">
          <div className="flex min-w-0 items-center justify-between gap-2">
            <span className="min-w-0 truncate">{title}</span>
            <SignalChip signal={c.signal} rssi={c.rssi} />
          </div>
          <p className="text-sm break-words text-muted-foreground">
            {[c.ip, c.ssid, showAP ? c.ap_name : '', bandText(c)].filter(Boolean).join(' · ')}
            {c.self_assigned ? (
              <Chip tone="crit" className="ml-1.5">
                DHCP failing
              </Chip>
            ) : null}
          </p>
          <p className="flex min-w-0 flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
            <Mono>{c.mac}</Mono>
            <PrivateBadge show={c.private_mac} />
            {c.connected_seconds ? <span>· connected {short(c.connected_seconds)}</span> : null}
          </p>
        </div>
      )}
    />
  )
}
