import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { keys, stale } from '@/api/keys'
import type { MutationResponse, RADIUSServerInput, RADIUSServerView, RADIUSView, RevealView, WLANView } from '@/gen/api.gen'
import { dryRunQuery, type ExpertPatchInput, type ExpertPatchView, type MutationOptions } from './sites'

// WLANs (design §3.8, §3.9, §4.3 Network, §4.9 WLANView): the generated
// contract (internal/api, tygo); the aliases keep the names the screens use.

export type {
  CoAServerView,
  CoAView,
  MPSKKeyView,
  MPSKView,
  RevealView,
  ScheduleView,
  ScheduleWindow,
  Security,
  WLANView,
} from '@/gen/api.gen'
export type PMF = NonNullable<WLANView['pmf']>
export type Isolation = WLANView['isolation']
export type RatesProfile = WLANView['rates']
export type QoSClass = WLANView['qos_class']
export type FastRoaming = WLANView['roaming']['fast']
export type RadiusServerView = RADIUSServerView
export type RadiusServerInput = RADIUSServerInput
export type RadiusView = RADIUSView

/**
 * POST body, and the PATCH merge patch: read-only fields removed, secrets
 * write-only (internal/api WLANInput lists the keys the server decodes
 * itself; every other editable WLANView key passes through).
 */
export type WLANInput = Partial<
  Omit<WLANView, 'id' | 'site_id' | 'has_psk' | 'bssid_index' | 'owe_bssid_index' | 'expert' | 'experimental_in_use' | 'live' | 'updated_rev' | 'radius' | 'mpsk'>
> & {
  psk?: string
  preset?: 'home' | 'guest' | 'iot'
  radius?:
    | (Partial<Omit<RadiusView, 'auth' | 'acct' | 'coa'>> & {
        auth?: RadiusServerInput[]
        acct?: RadiusServerInput[]
        coa?: { enabled: boolean; servers: { host: string; port: number; secret?: string }[]; event_timestamp_optional: boolean } | null
      })
    | null
  mpsk?: { enabled?: boolean; private?: boolean; keys?: { id?: string; name: string; passphrase?: string; vlan_id: number | null; mac: string; expires: string | null }[] } | null
}

const networksPath = (siteId: string) => `/api/sites/${encodeURIComponent(siteId)}/networks`
const networkPath = (siteId: string, id: string) => `${networksPath(siteId)}/${encodeURIComponent(id)}`

export function networksQuery(siteId: string) {
  return queryOptions({
    queryKey: keys.siteNetworks(siteId),
    queryFn: ({ signal }) => api.get<WLANView[]>(networksPath(siteId), { signal }),
    staleTime: stale.config,
  })
}

export function networkQuery(siteId: string, id: string) {
  return queryOptions({
    queryKey: keys.network(id),
    queryFn: ({ signal }) => api.get<WLANView>(networkPath(siteId, id), { signal }),
    staleTime: stale.config,
  })
}

export function createNetwork(siteId: string, body: WLANInput, options: MutationOptions = {}) {
  return api.post<MutationResponse<WLANView>>(networksPath(siteId), body, {
    query: dryRunQuery(options),
    signal: options.signal,
  })
}

export function patchNetwork(siteId: string, id: string, body: WLANInput, options: MutationOptions = {}) {
  return api.patch<MutationResponse<WLANView>>(networkPath(siteId, id), body, {
    query: dryRunQuery(options),
    ifMatch: options.ifMatch,
    signal: options.signal,
  })
}

export function deleteNetwork(siteId: string, id: string, options: MutationOptions = {}) {
  return api.delete<MutationResponse<null>>(networkPath(siteId, id), {
    query: dryRunQuery(options),
    ifMatch: options.ifMatch,
  })
}

export function networkExpertQuery(siteId: string, id: string) {
  return queryOptions({
    queryKey: [...keys.network(id), 'expert'] as const,
    queryFn: ({ signal }) => api.get<ExpertPatchView>(`${networkPath(siteId, id)}/expert`, { signal }),
    staleTime: stale.config,
  })
}

export function putNetworkExpert(siteId: string, id: string, body: ExpertPatchInput, options: MutationOptions = {}) {
  return api.put<MutationResponse<ExpertPatchView>>(`${networkPath(siteId, id)}/expert`, body, {
    query: dryRunQuery(options),
    ifMatch: options.ifMatch,
    signal: options.signal,
  })
}

/** Phase 4: admin scope, audit-logged on the server. */
export function revealNetwork(siteId: string, id: string) {
  return api.post<RevealView>(`${networkPath(siteId, id)}/reveal`)
}
