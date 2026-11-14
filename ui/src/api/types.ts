// Contract types come only from the generator (design §7.5): tygo over
// internal/api writes src/gen/api.gen.ts. This module re-exports them so
// screens keep importing from '@/api/types', and adds the few client-side
// shapes the generator does not emit (and must never emit a second copy of).

export type {
  APCapabilities,
  APConfigState,
  APConfigView,
  APConnection,
  APDetail,
  APHealth,
  APIdentity,
  APInput,
  APIP,
  APLive,
  APOverrides,
  APStatus,
  APUplink,
  APView,
  Actor,
  ActorKind,
  AuthSession,
  AuthSessionView,
  AuthVia,
  Band,
  BackupView,
  CAPem,
  ClientDetail,
  ClientQoS,
  ClientRetries,
  ClientView,
  ConfigEvent,
  ConfigView,
  DFSState,
  DiffEntry,
  Disruption,
  ErrorBody,
  ErrorCode,
  EventPage,
  EventSummary,
  EventView,
  H0View,
  Impact,
  ImpactAP,
  LLDPView,
  LocalFileView,
  MutationResponse,
  OnboardingView,
  PendingAP,
  PendingEvent,
  PendingReason,
  PlannedRadio,
  PortView,
  PowerView,
  PushEvent,
  PushMode,
  PushState,
  RadioOverride,
  RadioRole,
  RadioSummary,
  RadioView,
  SessionView,
  Severity,
  Signal,
  SiteCounts,
  SiteInput,
  SiteSummary,
  SiteView,
  StreamEvent,
  StreamMessage,
  StreamSnapshot,
  SystemView,
  TokenCreated,
  TokenInput,
  TokenList,
  TokenScope,
  TokenView,
  Verification,
  WLANLive,
  Warning,
} from '@/gen/api.gen'

/**
 * The pre-v1 stream's only event (`event: update`), sent by servers before
 * SSE v2 (design §4.10). The client folds it into the AP list so a v0.3
 * controller still shows live connection state and client counts.
 */
export type LegacyStreamUpdate = {
  site_id: string
  revision: number
  observed_at?: string
  aps: { id: string; connected: boolean; applied_revision: number; client_count: number }[]
}
