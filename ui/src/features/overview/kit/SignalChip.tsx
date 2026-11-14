import type { Signal } from '@/gen/api.gen'
import { Chip } from '@/components/app/StatusChip'

// Signal quality from RSSI (design §3.12): Excellent ≥ −60, Good −60 to −67,
// Fair −67 to −75, Poor < −75. Colour is never the only signal.

export function signalFromRssi(rssi: number | null | undefined): Signal | null {
  if (rssi == null || !Number.isFinite(rssi) || rssi === 0) return null
  if (rssi >= -60) return 'excellent'
  if (rssi >= -67) return 'good'
  if (rssi >= -75) return 'fair'
  return 'poor'
}

const meta: Record<Signal, { label: string; tone: 'good' | 'info' | 'warn' | 'crit' }> = {
  excellent: { label: 'Excellent', tone: 'good' },
  good: { label: 'Good', tone: 'good' },
  fair: { label: 'Fair', tone: 'warn' },
  poor: { label: 'Poor', tone: 'crit' },
}

export function SignalChip({ signal, rssi, className }: { signal?: Signal | null; rssi?: number | null; className?: string }) {
  const bucket = signal ?? signalFromRssi(rssi)
  if (!bucket) return null
  const m = meta[bucket] ?? meta.fair
  return (
    <Chip tone={m.tone} className={className}>
      {m.label}
      {rssi != null && rssi !== 0 ? ` · ${rssi} dBm` : ''}
    </Chip>
  )
}
