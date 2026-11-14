import type {
  WLANInput as GeneratedWLANInput,
  BroadcastView,
  ChangeDetail,
  MPSKKeyView,
  MutationResponse,
  PlanProposal,
  RADIUSServerInput,
  RADIUSServerView,
  RADIUSView,
  RateLimitView,
  RoamingView,
  SamplePage,
  SitePatch,
  SiteView,
  WLANView,
} from '@/gen/api.gen'

// Names the screens used before workstream C generated the phase 2 to 4
// contract, kept as aliases of the generated types (design §7.5: contract
// types come only from the generator). The generated types themselves are
// re-exported from types.ts.

export type {
  APAction,
  ActionInput,
  ActionResult,
  BandProfileView,
  ChangePage,
  ChangeSummary,
  ClientMeta,
  ExpertPatchView,
  ExpertScope,
  MigrationStatus,
  MigrationRun,
  Neighbour,
  PlanProposal,
  PlanRow,
  PowerPolicy,
  RFProfileInput,
  RFProfileView,
  Recommendation,
  RestoreInput,
  RestorePlan,
  RevealView,
  SSHKeyView,
  Sample,
  SampleRadio,
  ScheduleView,
  Security,
  SyslogTarget,
  WLANView,
} from '@/gen/api.gen'

export type PMF = NonNullable<WLANView['pmf']>
export type Isolation = WLANView['isolation']
export type RateProfile = WLANView['rates']
export type QoSClass = WLANView['qos_class']
export type RadiusServerView = RADIUSServerView
export type RadiusServerInput = RADIUSServerInput
export type RadiusView = RADIUSView
export type WLANRoaming = RoamingView
export type WLANBroadcast = BroadcastView
export type RateLimit = RateLimitView
export type MultiPSKKeyView = MPSKKeyView
export type SiteSettingsView = SiteView
export type SiteSettingsInput = SitePatch
export type PlanResponse = MutationResponse<PlanProposal>
export type ChangeRecord = ChangeDetail
export type SamplesView = SamplePage

/**
 * POST /api/sites/$id/networks and PATCH …/networks/$nid: a JSON merge
 * patch over WLANView's editable fields plus the write-only secrets. The
 * generated WLANInput lists the keys the server decodes itself; every
 * other editable WLANView key passes through (internal/api/wlans.go).
 */
export type WLANInput = GeneratedWLANInput &
  Partial<Omit<WLANView, keyof GeneratedWLANInput | 'id' | 'site_id' | 'has_psk' | 'expert' | 'experimental_in_use' | 'live' | 'updated_rev' | 'bssid_index' | 'owe_bssid_index'>>
