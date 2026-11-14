import type { ClientView, Signal } from '@/gen/api.gen'

// Client list filters and sort (design §3.12). One response per site; these
// run client-side.

export const CLIENT_SORTS = ['signal', 'name', 'usage'] as const
export type ClientSort = (typeof CLIENT_SORTS)[number]
export const SIGNALS: Signal[] = ['excellent', 'good', 'fair', 'poor']
export const BANDS = ['2.4', '5'] as const

export type ClientFilters = { q?: string; ap?: string; wlan?: string; band?: string; signal?: string }

export function matchesClient(client: ClientView, q: string): boolean {
  const needle = q.trim().toLowerCase()
  if (!needle) return true
  const macCompact = needle.replace(/[:.-]/g, '')
  return (
    [client.name, client.alias ?? '', client.hostname, client.ip, ...client.ipv6].some((v) => v.toLowerCase().includes(needle)) ||
    (macCompact.length >= 2 && client.mac.replace(/-/g, '').includes(macCompact))
  )
}

export function filterClients(clients: ClientView[], filters: ClientFilters): ClientView[] {
  return clients.filter(
    (c) =>
      (!filters.ap || c.ap_id === filters.ap) &&
      (!filters.wlan || c.wlan_id === filters.wlan || c.ssid === filters.wlan) &&
      (!filters.band || c.band === filters.band) &&
      (!filters.signal || c.signal === filters.signal) &&
      matchesClient(c, filters.q ?? ''),
  )
}

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' })

export function displayName(c: Pick<ClientView, 'alias' | 'hostname' | 'name' | 'mac'>): string {
  return c.alias || c.hostname || (c.name && c.name !== c.mac ? c.name : '') || c.mac
}

export function sortClients(clients: ClientView[], sort: ClientSort = 'signal'): ClientView[] {
  switch (sort) {
    case 'name':
      return clients.toSorted((a, b) => collator.compare(displayName(a), displayName(b)))
    case 'usage':
      return clients.toSorted((a, b) => b.down_bytes + b.up_bytes - (a.down_bytes + a.up_bytes))
    default:
      // Weakest first: the clients that need attention lead.
      return clients.toSorted((a, b) => (a.rssi || -200) - (b.rssi || -200) || collator.compare(displayName(a), displayName(b)))
  }
}

export function bandText(c: Pick<ClientView, 'band' | 'channel'>): string {
  const band = c.band === '2.4' ? '2.4 GHz' : c.band === '5' ? '5 GHz' : c.band ? `${c.band} GHz` : ''
  return [band, c.channel ? `ch${c.channel}` : ''].filter(Boolean).join(' ')
}

export const signalLabel: Record<Signal, string> = { excellent: 'Excellent', good: 'Good', fair: 'Fair', poor: 'Poor' }
