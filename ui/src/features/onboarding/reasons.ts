import type { PendingReason } from '@/gen/api.gen'

export const pendingReasonMeta: Record<PendingReason, { label: string; tone: 'info' | 'crit'; help: string }> = {
  unregistered: { label: 'Not registered', tone: 'info', help: 'The AP authenticated its ID but has no record here. Adopt it to register it.' },
  source_mismatch: {
    label: 'Source mismatch',
    tone: 'crit',
    help: 'Registered, but connecting from an address other than its pinned source IP.',
  },
  auth_failed: { label: 'Auth failed', tone: 'crit', help: 'Registered, but its proof does not match the stored secret.' },
}
