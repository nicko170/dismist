import { useMemo, useState } from 'react'
import { useInfiniteQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import type { APDetail, EventView, RadioView } from '@/gen/api.gen'
import { siteEventsQuery } from '@/api/queries/events'
import { Chip } from '@/components/app/StatusChip'
import { Mono } from '@/components/app/identifiers'
import { RelativeTime } from '@/components/app/RelativeTime'
import { SkeletonRows } from '@/components/app/states'
import { formatBytes } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Card, Dash, Facts } from '@/features/overview/kit/Card'
import { EventDrawer } from '@/features/events/EventDrawer'
import { EventList } from '@/features/events/EventList'
import { bandName, orderRadios, radioLine, uplinkSummary } from './format'

// AP detail Overview tab (design §3.5): health, connection, config, radios
// summary, uplink summary, broadcasting VAPs, recent events.

function Tone({ tone, children }: { tone?: 'warn' | 'crit'; children: React.ReactNode }) {
  return <span className={cn('tabular-nums', tone === 'crit' && 'font-medium text-destructive', tone === 'warn' && 'font-medium text-warning')}>{children}</span>
}

export function tempTone(c: number | null): 'warn' | 'crit' | undefined {
  if (c === null) return undefined
  if (c >= 80) return 'crit'
  if (c >= 70) return 'warn'
  return undefined
}

function pct(part: number, total: number) {
  return total ? Math.round((part / total) * 100) : 0
}

/** The plan disagrees with what the radio reports (§5.9), or no plan is known. */
export function radioMismatch(ap: Pick<APDetail, 'rf_plan' | 'config'>, radio: RadioView): string | null {
  if (radio.role === 'scan' || !radio.band) return null
  const plan = ap.rf_plan?.[radio.band]
  if (!plan) return null
  if (plan.channel && radio.channel && plan.channel !== radio.channel) return `planned ch ${plan.channel}`
  if (plan.width && radio.width && plan.width !== radio.width) return `planned ${plan.width} MHz`
  if (plan.power_dbm && radio.max_power_dbm && plan.power_dbm !== radio.max_power_dbm) return `planned ${plan.power_dbm} dBm`
  return null
}

export function APOverviewTab({ ap, siteId }: { ap: APDetail; siteId: string }) {
  const h = ap.health
  const c = ap.connection
  const cfg = ap.config
  const events = useInfiniteQuery(siteEventsQuery(siteId, { ap: ap.id }))
  const recent = useMemo(() => (events.data?.pages[0]?.events ?? []).slice(0, 5), [events.data])
  const [open, setOpen] = useState<EventView | null>(null)
  const memUsed = h.mem_total_bytes ? h.mem_total_bytes - h.mem_available_bytes : 0
  const rwFree = pct(h.rw_available_bytes, h.rw_total_bytes)
  const drift = cfg.applied_timestamp && cfg.desired_timestamp ? cfg.applied_timestamp - cfg.desired_timestamp : 0
  const access = orderRadios(ap.radios_detail).filter((r) => r.role !== 'scan')
  const offline = !ap.connected

  return (
    <div className="grid min-w-0 gap-4 lg:grid-cols-2">
      <Card title="Health" description={offline ? 'Last reported values; the AP is not connected.' : undefined}>
        <Facts
          items={[
            { label: 'CPU temperature', value: h.cpu_temp_c === null ? <Dash /> : <Tone tone={tempTone(h.cpu_temp_c)}>{h.cpu_temp_c} °C</Tone> },
            { label: 'CPU busy', value: h.cpu_busy === null ? <Dash /> : `${Math.round(h.cpu_busy)} %` },
            { label: 'Memory used', value: h.mem_total_bytes ? `${formatBytes(memUsed)} of ${formatBytes(h.mem_total_bytes)} (${pct(memUsed, h.mem_total_bytes)} %)` : <Dash /> },
            { label: 'Load 1 / 5 / 15', value: h.load?.some((v) => v) ? h.load.map((v) => v.toFixed(2)).join(' / ') : <Dash /> },
            {
              label: '/rw free',
              value: h.rw_total_bytes ? <Tone tone={rwFree < 10 ? 'warn' : undefined}>{`${formatBytes(h.rw_available_bytes)} (${rwFree} %)`}</Tone> : <Dash />,
            },
            { label: 'Agent memory', value: h.agent_heap_bytes ? formatBytes(h.agent_heap_bytes) : <Dash />, hidden: !h.agent_heap_bytes },
          ]}
        />
      </Card>

      <Card title="Connection">
        <Facts
          items={[
            { label: 'Controller RTT', value: c.rtt_ms === null ? <Dash /> : `${Math.round(c.rtt_ms)} ms` },
            { label: 'Path MTU', value: c.pmtu ?? <Dash /> },
            { label: 'Config push', value: c.push_mode === 'notify' ? 'Notify (session kept)' : 'Reconnect' },
            { label: 'TLS cipher', value: c.tls_cipher ? <Mono className="text-xs">{c.tls_cipher}</Mono> : <Dash /> },
            { label: 'Last disconnect', value: <RelativeTime value={c.last_disconnect} fallback="None recorded" /> },
            {
              label: 'AP clock skew',
              value: c.clock_skew_s === null ? <Dash /> : <Tone tone={Math.abs(c.clock_skew_s) > 5 ? 'warn' : undefined}>{`${c.clock_skew_s > 0 ? '+' : ''}${c.clock_skew_s.toFixed(1)} s`}</Tone>,
            },
            { label: 'Tunnel IPv6', value: ap.identity.tunnel_ipv6 ? <Mono>{ap.identity.tunnel_ipv6}</Mono> : null, hidden: !ap.identity.tunnel_ipv6 },
          ]}
        />
      </Card>

      <Card
        title="Config"
        action={
          <Link to={'/sites/$siteId/aps/$apId/config' as never} params={{ siteId, apId: ap.id } as never} className="text-sm text-brand hover:underline">
            View config
          </Link>
        }
      >
        <Facts
          items={[
            {
              label: 'Revision',
              value: (
                <span className="tabular-nums">
                  applied {ap.applied_revision || '–'} of {ap.config_revision || '–'}
                  {ap.applied_revision && ap.applied_revision === ap.config_revision ? <Chip tone="good" className="ml-2">in sync</Chip> : null}
                </span>
              ),
            },
            { label: 'Effective hash', value: cfg.effective_hash ? <Mono className="text-xs" >{cfg.effective_hash.slice(0, 20)}…</Mono> : <Dash /> },
            {
              label: 'Post-delivery check',
              value: cfg.last_verification ? (
                cfg.last_verification.ok ? (
                  <span>
                    <Chip tone="good">Passed</Chip> <RelativeTime value={cfg.last_verification.at} />
                  </span>
                ) : (
                  <span className="grid gap-1">
                    <span>
                      <Chip tone="crit">Mismatch</Chip> <RelativeTime value={cfg.last_verification.at} />
                    </span>
                    <ul className="list-disc pl-4 text-xs text-muted-foreground">
                      {cfg.last_verification.mismatches.map((m) => (
                        <li key={m}>{m}</li>
                      ))}
                    </ul>
                  </span>
                )
              ) : (
                <span className="text-muted-foreground">Not run yet</span>
              ),
            },
            { label: 'Timestamp drift', value: drift ? <Tone tone="warn">{`${drift > 0 ? '+' : ''}${drift} s`}</Tone> : 'None', hidden: !cfg.applied_timestamp },
            { label: 'Last error', value: <span className="text-destructive">{ap.last_error}</span>, hidden: !ap.last_error },
            { label: 'Compile error', value: <span className="text-destructive">{cfg.compile_error}</span>, hidden: !cfg.compile_error },
            { label: 'Recovered from bad config', value: <Chip tone="warn">Yes</Chip>, hidden: !cfg.recovered_from_bad_config },
            { label: 'Country reported', value: cfg.country_reported || <Dash /> },
            {
              label: 'Local config files',
              value: ap.local_files === null ? 'Not checked' : ap.local_files.length ? <Chip tone="warn">{ap.local_files.map((f) => f.name).join(', ')}</Chip> : 'None',
            },
          ]}
        />
      </Card>

      <Card
        title="Radios"
        action={
          <Link to="/sites/$siteId/aps/$apId/radios" params={{ siteId, apId: ap.id }} className="text-sm text-brand hover:underline">
            Details
          </Link>
        }
      >
        {access.length ? (
          <ul className="grid gap-2 text-sm">
            {access.map((radio) => {
              const mismatch = radioMismatch(ap, radio)
              return (
                <li key={radio.dev} className="flex min-w-0 flex-wrap items-center gap-2 tabular-nums">
                  {radioLine(radio)}
                  {mismatch ? <Chip tone="warn">{mismatch}</Chip> : null}
                </li>
              )
            })}
          </ul>
        ) : ap.radios.length ? (
          <ul className="grid gap-2 text-sm tabular-nums">
            {ap.radios.map((r) => (
              <li key={r.band}>
                {bandName(r.band)} · ch {r.channel}/{r.width || 20} · {r.clients} clients · util {Math.round(r.util)} %
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-sm text-muted-foreground">{offline ? 'No radio data while the AP is offline.' : 'Waiting for the AP’s first full report.'}</p>
        )}
      </Card>

      <Card
        title="Uplink"
        action={
          <Link to="/sites/$siteId/aps/$apId/uplink" params={{ siteId, apId: ap.id }} className="text-sm text-brand hover:underline">
            Details
          </Link>
        }
      >
        <p className="text-sm break-words">{uplinkSummary(ap) || <span className="text-muted-foreground">No uplink data yet.</span>}</p>
        {ap.uplink.power?.constrained ? <Chip tone="warn" className="mt-2">PoE constrained</Chip> : null}
        {ap.uplink.inactive_vlans.length ? (
          <p className="mt-2 text-sm text-warning">VLAN {ap.uplink.inactive_vlans.join(', ')} not reaching this AP’s uplink.</p>
        ) : null}
      </Card>

      <Card title="Broadcasting" bodyClassName={ap.wlans_live.length ? 'p-0' : undefined}>
        {ap.wlans_live.length ? (
          <ul className="divide-y text-sm">
            {ap.wlans_live.map((vap) => (
              <li key={`${vap.dev}-${vap.bssid}`} className="grid min-w-0 gap-0.5 px-4 py-2.5">
                <span className="flex flex-wrap items-center justify-between gap-2">
                  <span className="font-medium break-words">{vap.ssid || vap.wlan_id || 'Unnamed WLAN'}</span>
                  <span className="text-xs text-muted-foreground tabular-nums">
                    {bandName(vap.band)} · {vap.clients} {vap.clients === 1 ? 'client' : 'clients'}
                  </span>
                </span>
                <span className="text-xs text-muted-foreground">
                  <Mono>{vap.bssid}</Mono> · {vap.dev}
                </span>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-sm text-muted-foreground">Not broadcasting any WLAN{offline ? ' (offline)' : ''}.</p>
        )}
      </Card>

      <Card
        title="Recent events"
        className="lg:col-span-2"
        bodyClassName="p-0"
        action={
          <Link to="/sites/$siteId/aps/$apId/events" params={{ siteId, apId: ap.id }} className="text-sm text-brand hover:underline">
            All events
          </Link>
        }
      >
        {events.isPending ? (
          <SkeletonRows rows={2} className="p-4" />
        ) : recent.length ? (
          <EventList events={recent} showAP={false} onOpen={setOpen} label={`Recent events for ${ap.name}`} className="rounded-none border-0" />
        ) : (
          <p className="p-4 text-sm text-muted-foreground">No events for this AP yet.</p>
        )}
      </Card>
      <EventDrawer siteId={siteId} summary={open} onOpenChange={(next) => !next && setOpen(null)} apName={ap.name} />
    </div>
  )
}
