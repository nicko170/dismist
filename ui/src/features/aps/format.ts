import type { APDetail, PortView, RadioView } from '@/gen/api.gen'

// Display helpers for AP telemetry (design §3.5).

export const bandName = (band: string | null | undefined) => (band === '2.4' ? '2.4 GHz' : band === '5' ? '5 GHz' : band ? `${band} GHz` : 'Scan')

export const PHY: Record<number, string> = { 1: '802.11b', 2: '802.11g', 3: '802.11a', 4: '802.11n', 5: '802.11n', 6: '802.11ac', 7: '802.11ac', 9: '802.11ax', 10: '802.11be' }

export function phyName(phy: number): string {
  return PHY[phy] ?? (phy ? `PHY ${phy}` : '–')
}

/** "max 17 dBm" when the AP reports only the target (31.7 is usually absent). */
export function powerText(radio: Pick<RadioView, 'tx_power_dbm' | 'max_power_dbm'>): string {
  if (radio.tx_power_dbm) return `${radio.tx_power_dbm} dBm`
  if (radio.max_power_dbm) return `max ${radio.max_power_dbm} dBm`
  return '–'
}

export function speedText(mbps: number): string {
  if (!mbps) return '–'
  return mbps >= 1000 ? `${mbps / 1000}G` : `${mbps}M`
}

export function bitsText(bps: number | null | undefined): string {
  if (bps == null || !Number.isFinite(bps)) return '–'
  const units = ['bit/s', 'kbit/s', 'Mbit/s', 'Gbit/s']
  let v = bps
  let i = 0
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000
    i++
  }
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`
}

export function watts(value: number | null | undefined): string {
  return value == null ? '–' : `${value.toFixed(1)} W`
}

/** "eth0 1G full duplex → switch port ge-0/0/7 (native 1, tagged 30) · PoE 19.2 of 25.5 W". */
export function uplinkSummary(detail: Pick<APDetail, 'uplink'>): string {
  const eth0: PortView | undefined = detail.uplink.ports.find((p) => p.name === 'eth0') ?? detail.uplink.ports[0]
  const lldp = detail.uplink.lldp.find((n) => n.port === eth0?.name) ?? detail.uplink.lldp[0]
  const parts: string[] = []
  if (eth0) parts.push(eth0.up ? `${eth0.name} ${speedText(eth0.speed_mbps)}${eth0.duplex ? ` ${eth0.duplex} duplex` : ''}` : `${eth0.name} down`)
  if (lldp) {
    const tagged = lldp.vlans.filter((v) => v !== lldp.native_vlan)
    const vlans = [lldp.native_vlan ? `native ${lldp.native_vlan}` : '', tagged.length ? `tagged ${tagged.join(', ')}` : ''].filter(Boolean).join(', ')
    parts.push(`→ ${lldp.system_name || lldp.chassis_id || 'switch'}${lldp.port_id ? ` port ${lldp.port_id}` : ''}${vlans ? ` (${vlans})` : ''}`)
  }
  const power = detail.uplink.power
  if (power && (power.allocated_w || power.needed_w)) {
    parts.push(`· PoE${power.source ? ` via ${power.source}` : ''} ${watts(power.needed_w)} of ${watts(power.allocated_w ?? power.budget_w)}`)
  }
  return parts.join(' ')
}

export function radioLine(radio: Pick<RadioView, 'band' | 'channel' | 'width' | 'tx_power_dbm' | 'max_power_dbm' | 'clients' | 'util'>): string {
  return [
    bandName(radio.band),
    radio.channel ? `ch ${radio.channel}/${radio.width || 20}` : 'off',
    powerText(radio),
    `${radio.clients} ${radio.clients === 1 ? 'client' : 'clients'}`,
    `util ${Math.round(radio.util)} %`,
  ].join(' · ')
}

/** The radio's role label: r2 reports band 0 on channel 165, so it is "Scan", never 2.4 GHz. */
export function radioTitle(radio: Pick<RadioView, 'dev' | 'role' | 'band'>): string {
  if (radio.role === 'scan') return `Scanning radio (${radio.dev})`
  return `${bandName(radio.band)} (${radio.dev})`
}

/** Radios in display order: 2.4 GHz access, 5 GHz access, then the scan radio. */
export function orderRadios(radios: RadioView[]): RadioView[] {
  const rank = (r: RadioView) => (r.role === 'scan' ? 2 : r.band === '2.4' ? 0 : 1)
  return radios.toSorted((a, b) => rank(a) - rank(b) || a.dev.localeCompare(b.dev))
}
