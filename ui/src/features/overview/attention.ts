import type { APView, EventView, PushEvent, SystemView } from '@/gen/api.gen'
import { apPath, offlineFor } from '@/features/aps/model'
import { daysLeft } from '@/features/system/cert'

// The overview's Attention card (design §3.3): most severe first, each item a
// link to where it can be fixed. Pure, so it is unit-tested.

export type AttentionSeverity = 'crit' | 'warn' | 'info'
export type AttentionItem = {
  id: string
  severity: AttentionSeverity
  title: string
  detail?: string
  to: string
  search?: Record<string, string>
}

const rank: Record<AttentionSeverity, number> = { crit: 0, warn: 1, info: 2 }

export function attentionItems({
  siteId,
  aps,
  now,
  pendingCount = 0,
  system,
  events = [],
  recommendations = 0,
  push = {},
}: {
  siteId: string
  aps: APView[]
  now: number
  pendingCount?: number
  system?: SystemView
  events?: EventView[]
  recommendations?: number
  push?: Record<string, PushEvent>
}): AttentionItem[] {
  const items: AttentionItem[] = []
  const add = (item: AttentionItem) => items.push(item)

  for (const ap of aps) {
    const to = apPath(ap)
    const flags = new Set([ap.status, ...ap.flags])
    if (flags.has('auth_failing')) add({ id: `auth-${ap.id}`, severity: 'crit', title: `${ap.name}: authentication is failing`, detail: 'The AP’s secret does not match the one stored for it.', to })
    if (flags.has('source_mismatch'))
      add({ id: `src-${ap.id}`, severity: 'crit', title: `${ap.name} is connecting from an address it is not pinned to`, to })
    if (flags.has('config_error'))
      add({ id: `cerr-${ap.id}`, severity: 'crit', title: `dismist could not compile ${ap.name}’s config`, detail: 'It keeps receiving its last delivered config.', to })
    if (ap.status === 'offline') {
      const since = offlineFor(ap, now)
      add({ id: `off-${ap.id}`, severity: 'crit', title: since ? `${ap.name} offline for ${since}` : `${ap.name} is offline`, to })
    }
    if (ap.status === 'never_connected') add({ id: `never-${ap.id}`, severity: 'warn', title: `${ap.name} has never connected`, to })
    if (flags.has('rejected'))
      add({
        id: `rej-${ap.id}`,
        severity: 'crit',
        title: `${ap.name} rejected rev ${ap.config_revision}`,
        detail: ap.last_error || undefined,
        to,
      })
    // The server marks an AP `pending` once its change is 2 min old (§3.4).
    if (ap.status === 'pending') {
      add({ id: `pend-${ap.id}`, severity: 'warn', title: `${ap.name} has not applied rev ${ap.config_revision} after 2 min`, to })
    }
    if (flags.has('verify_mismatch'))
      add({ id: `ver-${ap.id}`, severity: 'warn', title: `${ap.name} failed its post-delivery check`, detail: 'What the AP reports differs from the config it was sent.', to })
    if (flags.has('rf_plan_missing'))
      add({ id: `rf-${ap.id}`, severity: 'warn', title: `${ap.name}: an Auto band is running on the compile fallback`, to: `/sites/${encodeURIComponent(siteId)}/rf` })
    if (flags.has('local_files'))
      add({ id: `lf-${ap.id}`, severity: 'warn', title: `${ap.name} has local config files that change what it runs`, to })
    if (flags.has('poe_constrained')) add({ id: `poe-${ap.id}`, severity: 'warn', title: `${ap.name} is short of PoE power`, to: apPath(ap, 'uplink') })
    if (flags.has('cpu_hot')) add({ id: `hot-${ap.id}`, severity: 'warn', title: `${ap.name} CPU is above 80 °C`, to })
    if (push[ap.id]?.state === 'timeout') add({ id: `pto-${ap.id}`, severity: 'warn', title: `${ap.name} did not fetch rev ${push[ap.id].revision}`, to })
  }

  const vlanEvents = events.filter((e) => e.kind === 'inactive_vlan' && !e.resolved_by)
  for (const event of vlanEvents.slice(0, 3)) {
    add({
      id: `vlan-${event.id}`,
      severity: 'warn',
      title: event.title,
      detail: event.detail && event.detail !== event.title ? event.detail : undefined,
      to: `/sites/${encodeURIComponent(siteId)}/events`,
      search: { kind: 'inactive_vlan' },
    })
  }

  if (pendingCount > 0) {
    add({
      id: 'pending',
      severity: 'info',
      title: pendingCount === 1 ? '1 AP is waiting to be adopted' : `${pendingCount} APs are waiting to be adopted`,
      to: '/onboard',
      search: { site: siteId },
    })
  }

  if (system) {
    const days = daysLeft(system.wss?.cert?.not_after, now)
    if (days !== null && days < 14) {
      add({
        id: 'cert',
        severity: days < 7 ? 'crit' : 'warn',
        title: days < 0 ? 'The AP endpoint certificate has expired' : `The AP endpoint certificate expires in ${days} d`,
        to: '/system',
      })
    }
    if (system.migration?.state === 'legacy') {
      add({
        id: 'legacy',
        severity: 'warn',
        title: 'Typed settings are paused: dismist is running your existing config unchanged',
        detail: system.migration.reason || undefined,
        to: '/system/migration',
      })
    }
  }

  if (recommendations > 0) {
    add({
      id: 'recs',
      severity: 'info',
      title: recommendations === 1 ? '1 recommendation available' : `${recommendations} recommendations available`,
      to: `/sites/${encodeURIComponent(siteId)}/settings/advanced`,
    })
  }

  return items.toSorted((a, b) => rank[a.severity] - rank[b.severity])
}
