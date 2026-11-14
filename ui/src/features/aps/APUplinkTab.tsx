import type { APDetail } from '@/gen/api.gen'
import { Chip } from '@/components/app/StatusChip'
import { Identifier, Mono } from '@/components/app/identifiers'
import { RelativeTime } from '@/components/app/RelativeTime'
import { formatDuration } from '@/lib/format'
import { Card, Dash, Facts } from '@/features/overview/kit/Card'
import { bitsText, speedText, watts } from './format'

// AP detail Uplink tab (design §3.5): wired ports, the switch it is plugged
// into (LLDP), PoE power, the management IP lease, inactive VLANs and the
// controller link.

export function APUplinkTab({ ap }: { ap: APDetail }) {
  const { ports, lldp, power, inactive_vlans: inactive } = ap.uplink
  const ip = ap.ip
  return (
    <div className="grid min-w-0 gap-4 lg:grid-cols-2">
      {inactive.length ? (
        <div role="alert" className="grid gap-1 rounded-lg border border-status-warn/50 bg-status-warn/10 px-4 py-3 text-sm lg:col-span-2">
          {inactive.map((vlan) => (
            <p key={vlan}>
              <span className="font-medium">VLAN {vlan} is not reaching this AP’s uplink.</span> Clients on WLANs that use it cannot get an address. Check the switch port’s
              tagged VLANs.
            </p>
          ))}
        </div>
      ) : null}

      <Card title="Ports" className="lg:col-span-2" bodyClassName={ports.length ? 'p-0' : undefined}>
        {ports.length ? (
          <ul className="divide-y">
            {ports.map((port) => {
              const errors = Object.entries(port.errors ?? {}).filter(([, v]) => v)
              return (
                <li key={port.name} className="grid min-w-0 gap-1 px-4 py-3 text-sm">
                  <span className="flex flex-wrap items-center gap-2">
                    <span className="font-medium">{port.name}</span>
                    <Chip tone={port.up ? 'good' : 'idle'}>{port.up ? 'Link up' : 'Down'}</Chip>
                    {port.up ? (
                      <span className="text-muted-foreground tabular-nums">
                        {speedText(port.speed_mbps)}
                        {port.duplex ? ` · ${port.duplex} duplex` : ''}
                      </span>
                    ) : null}
                  </span>
                  {port.up ? (
                    <span className="text-xs text-muted-foreground tabular-nums">
                      Rx {bitsText(port.rx_bps)} · Tx {bitsText(port.tx_bps)}
                    </span>
                  ) : null}
                  {errors.length ? (
                    <span className="text-xs text-warning">
                      {errors.map(([k, v]) => `${k.replace(/_/g, ' ')} ${v}`).join(' · ')}
                    </span>
                  ) : null}
                </li>
              )
            })}
          </ul>
        ) : (
          <p className="text-sm text-muted-foreground">No port data yet.</p>
        )}
        {ports.length ? (
          <p className="border-t px-4 py-2 text-xs text-muted-foreground">Rates are averaged over the AP’s 60 s report. The eth0 Tx counter is not yet validated against the switch.</p>
        ) : null}
      </Card>

      <Card title="Switch (LLDP)">
        {lldp.length ? (
          <div className="grid gap-4">
            {lldp.map((n) => (
              <Facts
                key={`${n.port}-${n.chassis_id}`}
                items={[
                  { label: 'On port', value: n.port || <Dash /> },
                  { label: 'Switch', value: (n.system_name || n.system_desc || '').slice(0, 64) || <Dash /> },
                  { label: 'Switch port', value: [n.port_id, n.port_desc].filter(Boolean).join(' · ') || <Dash /> },
                  { label: 'Native VLAN', value: n.native_vlan || <Dash /> },
                  { label: 'VLANs', value: n.vlans.length ? n.vlans.join(', ') : <Dash /> },
                  { label: 'Management address', value: n.mgmt_address ? <Identifier value={n.mgmt_address} /> : <Dash /> },
                  { label: 'Chassis ID', value: n.chassis_id ? <Mono className="text-xs">{n.chassis_id}</Mono> : <Dash /> },
                ]}
              />
            ))}
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">The switch has not sent LLDP to this AP.</p>
        )}
      </Card>

      <Card title="Power">
        {power ? (
          <Facts
            items={[
              { label: 'Source', value: power.source || <Dash /> },
              { label: 'Allocated by the switch', value: watts(power.allocated_w) },
              { label: 'Requested', value: watts(power.requested_w) },
              { label: 'Needed', value: watts(power.needed_w) },
              { label: 'Budget', value: watts(power.budget_w) },
              { label: 'Constrained', value: power.constrained ? <Chip tone="warn">Yes: some radios may be limited</Chip> : 'No' },
            ]}
          />
        ) : (
          <p className="text-sm text-muted-foreground">No PoE information reported.</p>
        )}
      </Card>

      <Card title="IP">
        {ip.address ? (
          <Facts
            items={[
              { label: 'Management VLAN', value: ip.vlan || <Dash /> },
              { label: 'Address', value: <Identifier value={ip.prefix ? `${ip.address}/${ip.prefix}` : ip.address} /> },
              { label: 'DHCP', value: ip.dhcp_state || <Dash /> },
              { label: 'Lease renewed', value: <RelativeTime value={ip.renewed_at} fallback="–" /> },
              { label: 'Lease time', value: ip.lease_seconds ? formatDuration(ip.lease_seconds) : <Dash /> },
              {
                label: 'Gateway',
                value: ip.gateway ? (
                  <span className="grid">
                    <Identifier value={ip.gateway} />
                    {ip.gateway_mac ? <Mono className="text-xs text-muted-foreground">{ip.gateway_mac}</Mono> : null}
                  </span>
                ) : (
                  <Dash />
                ),
              },
              { label: 'DNS servers', value: ip.dns.length ? <Mono>{ip.dns.join(', ')}</Mono> : <Dash /> },
              { label: 'DHCP server', value: ip.dhcp_server ? <Mono>{ip.dhcp_server}</Mono> : <Dash /> },
              { label: 'Domain', value: ip.domain || <Dash />, hidden: !ip.domain },
              { label: 'NTP (from DHCP)', value: <Mono>{ip.ntp.join(', ')}</Mono>, hidden: !ip.ntp.length },
              {
                label: 'Duplicate address',
                value: <Chip tone="crit">Detected on {ip.dad_failed.join(', ')}</Chip>,
                hidden: !ip.dad_failed.length,
              },
            ]}
          />
        ) : (
          <p className="text-sm text-muted-foreground">
            {ap.lan_ip ? (
              <>
                LAN IP <Mono>{ap.lan_ip}</Mono> (from the AP’s hello). Lease details arrive with the first full report.
              </>
            ) : (
              'No address reported yet.'
            )}
          </p>
        )}
      </Card>

      <Card title="Controller link">
        <Facts
          items={[
            { label: 'Public address', value: ap.remote_address ? <Identifier value={ap.remote_address} /> : <Dash /> },
            { label: 'RTT', value: ap.connection.rtt_ms === null ? <Dash /> : `${Math.round(ap.connection.rtt_ms)} ms` },
            { label: 'Path MTU', value: ap.connection.pmtu ?? <Dash /> },
            { label: 'Tunnel IPv6', value: ap.identity.tunnel_ipv6 ? <Mono className="text-xs">{ap.identity.tunnel_ipv6}</Mono> : <Dash /> },
          ]}
        />
      </Card>
    </div>
  )
}
