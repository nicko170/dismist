// Search-param vocabularies used by route validateSearch. Kept in their own
// tiny module because validateSearch is part of the initial bundle (design
// §7.9); the feature models re-export them.

export const AP_FILTERS = ['online', 'offline', 'attention'] as const
export const AP_SORTS = ['name', 'status', 'clients', 'uptime'] as const
export const CLIENT_SORTS = ['signal', 'name', 'usage'] as const
export const SIGNALS = ['excellent', 'good', 'fair', 'poor'] as const
export const BANDS = ['2.4', '5'] as const
export const EVENT_SEVERITIES = ['info', 'warning', 'error'] as const
export const EVENT_RANGE_KEYS = ['1h', '24h', '7d', '30d'] as const
export const EVENT_KINDS = [
  'connection',
  'config',
  'reboot',
  'auth',
  'dhcp_failure',
  'dns',
  'disassociated',
  'authorized',
  'rsn_config',
  'radio',
  'port',
  'inactive_vlan',
  'clock',
  'push',
  'feature',
  'system',
  'backup',
  'pace',
] as const
/** Kinds the server may send that the filter does not offer, still valid in a URL. */
export const EVENT_KINDS_EXTRA = ['dns_server', 'dns_latency', 'gtk_rekey', 'radar'] as const
