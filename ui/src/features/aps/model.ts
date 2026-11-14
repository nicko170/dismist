import type { APDetail, APStatus, APView, PushEvent, RadioSummary } from '@/gen/api.gen'
import { formatDuration } from '@/lib/format'
import { parseTime } from '@/lib/time'

// Pure AP helpers shared by the overview, the AP list and AP detail
// (design §3.3, §3.4, §3.5).

export const STATUS_ORDER: APStatus[] = [
  'auth_failing',
  'source_mismatch',
  'config_error',
  'never_connected',
  'offline',
  'rejected',
  'pending',
  'applying',
  'in_sync',
]

import { AP_FILTERS, AP_SORTS } from '@/features/overview/kit/params'

export { AP_FILTERS, AP_SORTS }
export type APFilter = (typeof AP_FILTERS)[number]
export type APSort = (typeof AP_SORTS)[number]

const ATTENTION: APStatus[] = ['auth_failing', 'source_mismatch', 'config_error', 'rejected', 'pending']
const ATTENTION_FLAGS = ['verify_mismatch', 'local_files', 'rf_plan_missing', 'config_error', 'rejected', 'auth_failing', 'source_mismatch']

export function needsAttention(ap: APView): boolean {
  return ATTENTION.includes(ap.status) || ap.flags.some((flag) => ATTENTION_FLAGS.includes(flag))
}

export function matchesQuery(ap: APView, q: string): boolean {
  const needle = q.trim().toLowerCase()
  if (!needle) return true
  return [ap.name, ap.id, ap.id.replace(/-/g, ':'), ap.lan_ip, ap.remote_address].some((v) => v?.toLowerCase().includes(needle))
}

export function filterAPs(aps: APView[], { status, q }: { status?: APFilter; q?: string }): APView[] {
  return aps.filter((ap) => {
    if (status === 'online' && !ap.connected) return false
    if (status === 'offline' && ap.connected) return false
    if (status === 'attention' && !needsAttention(ap)) return false
    return matchesQuery(ap, q ?? '')
  })
}

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' })

export function sortAPs(aps: APView[], sort: APSort = 'name'): APView[] {
  const byName = (a: APView, b: APView) => collator.compare(a.name, b.name)
  switch (sort) {
    case 'status':
      return aps.toSorted((a, b) => STATUS_ORDER.indexOf(a.status) - STATUS_ORDER.indexOf(b.status) || byName(a, b))
    case 'clients':
      return aps.toSorted((a, b) => b.client_count - a.client_count || byName(a, b))
    case 'uptime':
      return aps.toSorted((a, b) => b.uptime_seconds - a.uptime_seconds || byName(a, b))
    default:
      return aps.toSorted(byName)
  }
}

/** Worst first (the overview's top 5): status precedence, then fewer clients last. */
export function worstFirst(aps: APView[]): APView[] {
  return sortAPs(aps, 'status')
}

export function radioFor(radios: RadioSummary[] | undefined, band: '2.4' | '5'): RadioSummary | undefined {
  return radios?.find((radio) => radio.band === band)
}

/** "ch 1 · 149/80" (2.4 GHz channel, then 5 GHz channel/width). */
export function channelsText(radios: RadioSummary[] | undefined): string {
  const r24 = radioFor(radios, '2.4')
  const r5 = radioFor(radios, '5')
  const parts: string[] = []
  if (r24?.channel) parts.push(`ch ${r24.channel}`)
  if (r5?.channel) parts.push(`${r5.channel}/${r5.width || 20}`)
  return parts.join(' · ')
}

export function bandCounts(aps: APView[]): { b24: number; b5: number } {
  let b24 = 0
  let b5 = 0
  for (const ap of aps) {
    if (!ap.connected) continue
    b24 += radioFor(ap.radios, '2.4')?.clients ?? 0
    b5 += radioFor(ap.radios, '5')?.clients ?? 0
  }
  return { b24, b5 }
}

/** The channel tile: distinct channels per band across connected APs. */
export function siteChannels(aps: APView[]): string {
  const c24 = new Set<string>()
  const c5 = new Set<string>()
  for (const ap of aps) {
    if (!ap.connected) continue
    const r24 = radioFor(ap.radios, '2.4')
    const r5 = radioFor(ap.radios, '5')
    if (r24?.channel) c24.add(String(r24.channel))
    if (r5?.channel) c5.add(`${r5.channel}/${r5.width || 20}`)
  }
  const parts: string[] = []
  if (c24.size) parts.push(`2.4: ${[...c24].join(', ')}`)
  if (c5.size) parts.push(`5: ${[...c5].join(', ')}`)
  return parts.join(' · ')
}

/** "12 m" since the AP was last seen, for "Offline · 12 m". */
export function offlineFor(ap: Pick<APView, 'connected' | 'last_seen'>, now: number): string | undefined {
  if (ap.connected) return undefined
  const seen = parseTime(ap.last_seen)
  if (!seen) return undefined
  return formatDuration(Math.max(0, (now - seen.getTime()) / 1000)).split(' ').slice(0, 2).join(' ')
}

/** Secondary status chips on detail pages: every other status and flag. */
export const flagLabels: Record<string, string> = {
  verify_mismatch: 'Verification mismatch',
  rf_plan_missing: 'Auto channel on fallback',
  local_files: 'Local config files on AP',
  auth_failing: 'Auth failing',
  source_mismatch: 'Source mismatch',
  config_error: 'Config error',
  never_connected: 'Never connected',
  offline: 'Offline',
  rejected: 'Config rejected',
  pending: 'Pending',
  applying: 'Applying',
}

export function apPath(ap: Pick<APView, 'site_id' | 'id'>, tab = ''): string {
  return `/sites/${encodeURIComponent(ap.site_id)}/aps/${encodeURIComponent(ap.id)}${tab ? `/${tab}` : ''}`
}

// ---- push progress (phase 2, SSE `push`) ----

export type PushChip = { label: string; tone: 'info' | 'good' | 'warn' | 'crit' }

/**
 * The per-AP apply chip (design §3.0 "Saving"): Pending rev N, Applied,
 * Rejected, Timed out. A push older than 10 minutes is not shown once the
 * AP's revisions agree.
 */
export function pushChip(push: PushEvent | undefined, ap: Pick<APView, 'applied_revision' | 'config_revision'>): PushChip | null {
  if (!push) return null
  switch (push.state) {
    case 'notified':
    case 'fetched':
      return { label: `Pending rev ${push.revision}`, tone: 'info' }
    case 'fallback_reconnect':
      return { label: `Reconnecting for rev ${push.revision}`, tone: 'warn' }
    case 'applied':
      return ap.applied_revision >= push.revision ? { label: `Applied rev ${push.revision}`, tone: 'good' } : null
    case 'rejected':
      return { label: `Rejected rev ${push.revision}`, tone: 'crit' }
    case 'timeout':
      return { label: `Timed out rev ${push.revision}`, tone: 'warn' }
    default:
      return null
  }
}

// ---- boot facts (design §3.5; bug B) ----

/**
 * When the AP last booted. The hello's LastRebootTime is preferred, but a
 * known decoder bug (B) reports it as "now" (the moment of the hello). An AP
 * cannot have rebooted after it booted, so a reported time more than 5
 * minutes after the boot time implied by the uptime is ignored and the boot
 * time is derived from the uptime instead.
 */
export function lastBoot(detail: Pick<APDetail, 'identity' | 'last_seen' | 'uptime_seconds' | 'connected'>, now: number) {
  const reported = parseTime(detail.identity?.last_reboot_time)
  const reference = parseTime(detail.last_seen)?.getTime() ?? now
  const fromUptime = detail.connected && detail.uptime_seconds > 0 ? new Date(reference - detail.uptime_seconds * 1000) : null
  if (reported && fromUptime && reported.getTime() > fromUptime.getTime() + 300_000) {
    return { at: fromUptime, source: 'uptime' as const, reportedIgnored: true }
  }
  if (reported) return { at: reported, source: 'ap' as const, reportedIgnored: false }
  if (fromUptime) return { at: fromUptime, source: 'uptime' as const, reportedIgnored: false }
  return { at: null, source: null, reportedIgnored: false }
}

/** "Unknown until first connection" before an AP has said hello. */
export function modelText(ap: Pick<APView, 'model' | 'connected_at' | 'last_seen'>): string {
  return ap.model || 'Unknown until first connection'
}
