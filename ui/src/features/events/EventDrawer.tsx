import type { ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import type { EventView } from '@/gen/api.gen'
import { eventQuery, kindLabel } from '@/api/queries/events'
import { RelativeTime } from '@/components/app/RelativeTime'
import { Identifier } from '@/components/app/identifiers'
import { ErrorState, SkeletonRows } from '@/components/app/states'
import { DetailDrawer } from '@/features/overview/kit/ConfirmDialog'
import { Facts } from '@/features/overview/kit/Card'
import { SeverityIcon } from './EventList'

// The event detail drawer (design §3.13): the stream carries only a summary,
// so the drawer fetches the full event and shows its typed fields.

type Data = Record<string, unknown>

const text = (value: unknown): ReactNode => {
  if (value === null || value === undefined || value === '') return <span className="text-muted-foreground">–</span>
  if (typeof value === 'boolean') return value ? 'Yes' : 'No'
  if (Array.isArray(value)) return value.length ? value.map((v) => (typeof v === 'object' ? JSON.stringify(v) : String(v))).join(', ') : '–'
  if (typeof value === 'object') return <code className="font-mono text-xs break-all">{JSON.stringify(value)}</code>
  return String(value)
}

const pick = (data: Data, ...names: string[]) => {
  for (const name of names) if (data[name] !== undefined) return data[name]
  return undefined
}

/** Labelled typed fields per kind; anything else is listed generically. */
function typedFields(kind: string, data: Data): { label: string; value: ReactNode }[] {
  const known: [string, string[]][] = []
  switch (kind) {
    case 'dhcp_failure':
      known.push(['VLAN', ['vlan', 'vlan_id']], ['Attempts', ['attempts']], ['Requested IP', ['requested_ip']], ['Leased IP', ['leased_ip']], ['DHCP server', ['server', 'dhcp_server']], ['NAK', ['nak']], ['Declined', ['declined']])
      break
    case 'dns':
    case 'dns_server':
    case 'dns_latency':
      known.push(['Server', ['server']], ['Failures', ['failures']], ['Latency', ['latency_ms', 'latency']], ['Query', ['query']])
      break
    case 'rsn_config':
      known.push(['Previous channels', ['previous', 'previous_channels']], ['Current channels', ['current', 'current_channels']], ['WLANs torn down', ['wlans_torn_down', 'torn_down']])
      break
    case 'port':
      known.push(['Port', ['port']], ['Speed', ['speed', 'speed_mbps']], ['State', ['state', 'up']])
      break
    case 'authorized':
      known.push(['WLAN', ['ssid', 'wlan']], ['Band', ['band']], ['Duration', ['duration_ms', 'total_ms']])
      break
  }
  const used = new Set<string>()
  const rows = known
    .map(([label, names]) => {
      names.forEach((n) => used.add(n))
      const value = pick(data, ...names)
      return value === undefined ? null : { label, value: text(value) }
    })
    .filter((row): row is { label: string; value: ReactNode } => row !== null)
  for (const [key, value] of Object.entries(data)) {
    if (used.has(key) || key === 'timeline') continue
    rows.push({ label: key.replace(/_/g, ' '), value: text(value) })
  }
  return rows
}

function Timeline({ steps }: { steps: unknown }) {
  if (!Array.isArray(steps) || !steps.length) return null
  return (
    <section className="grid gap-2">
      <h3 className="text-sm font-semibold">Connect timeline</h3>
      <ol className="grid gap-1 border-l pl-4 text-sm">
        {steps.map((step, index) => {
          const s = (step ?? {}) as Data
          const label = String(s.step ?? s.name ?? s.stage ?? `Step ${index + 1}`)
          const ms = s.ms ?? s.offset_ms ?? s.elapsed_ms
          return (
            <li key={index} className="relative before:absolute before:top-2 before:-left-[1.3rem] before:size-2 before:rounded-full before:bg-chart-2">
              {label}
              {typeof ms === 'number' ? <span className="ml-2 text-xs text-muted-foreground tabular-nums">+{ms} ms</span> : null}
            </li>
          )
        })}
      </ol>
    </section>
  )
}

export function EventDrawer({
  siteId,
  summary,
  onOpenChange,
  apName,
}: {
  siteId: string
  summary: EventView | null
  onOpenChange: (open: boolean) => void
  apName?: string
}) {
  const query = useQuery({ ...eventQuery(siteId, summary?.id ?? ''), enabled: Boolean(summary) })
  const event = query.data ?? summary
  return (
    <DetailDrawer open={Boolean(summary)} onOpenChange={onOpenChange} title={summary?.title ?? 'Event'}>
      {!event ? null : (
        <div className="grid gap-5">
          <Facts
            items={[
              {
                label: 'Severity',
                value: (
                  <span className="inline-flex items-center gap-1.5">
                    <SeverityIcon severity={event.severity} resolved={Boolean(event.resolved_by)} />
                    {event.resolved_by ? `Resolved by ${event.resolved_by}` : event.severity}
                  </span>
                ),
              },
              { label: 'Kind', value: kindLabel(event.kind) },
              {
                label: 'AP',
                value: event.ap_id ? (
                  <Link to="/aps/$apId" params={{ apId: event.ap_id }} className="text-brand hover:underline">
                    {apName || event.ap_name || event.ap_id}
                  </Link>
                ) : (
                  'Controller'
                ),
              },
              { label: 'First seen', value: <RelativeTime value={event.first_seen} /> },
              { label: 'Last seen', value: <RelativeTime value={event.last_seen} />, hidden: event.last_seen === event.first_seen },
              { label: 'Count', value: event.count, hidden: event.count <= 1 },
              {
                label: 'Client',
                value: event.client_mac ? (
                  <span className="inline-flex flex-wrap items-center gap-1">
                    <Link to="/clients/$mac" params={{ mac: event.client_mac }} className="text-brand hover:underline">
                      Open client
                    </Link>
                    <Identifier value={event.client_mac} />
                  </span>
                ) : null,
                hidden: !event.client_mac,
              },
              { label: 'PACE type', value: event.pace_type, hidden: event.pace_type === null || event.pace_type === undefined },
            ]}
          />
          {event.detail && event.detail !== event.title ? <p className="text-sm break-words">{event.detail}</p> : null}
          {query.isPending && summary ? <SkeletonRows rows={2} /> : null}
          {query.isError ? <ErrorState error={query.error} title="Could not load the event’s details" onRetry={() => void query.refetch()} /> : null}
          {query.data ? (
            <>
              <Timeline steps={(query.data.data ?? {}).timeline} />
              {typedFields(query.data.kind, query.data.data ?? {}).length ? (
                <section className="grid gap-2">
                  <h3 className="text-sm font-semibold">Details</h3>
                  <Facts items={typedFields(query.data.kind, query.data.data ?? {})} />
                </section>
              ) : null}
            </>
          ) : null}
        </div>
      )}
    </DetailDrawer>
  )
}
