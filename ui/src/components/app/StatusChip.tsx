import type { APStatus } from '@/api/types'
import { cn } from '@/lib/utils'

// The one mapping from AP status to colour and words (design §3.4, §7.2).
// Colour is never the only signal: every chip has text.

type Tone = 'good' | 'info' | 'warn' | 'crit' | 'idle'

export const apStatusMeta: Record<APStatus, { label: string; tone: Tone }> = {
  in_sync: { label: 'In sync', tone: 'good' },
  applying: { label: 'Applying', tone: 'info' },
  pending: { label: 'Pending', tone: 'warn' },
  rejected: { label: 'Config rejected', tone: 'crit' },
  offline: { label: 'Offline', tone: 'idle' },
  never_connected: { label: 'Never connected', tone: 'idle' },
  auth_failing: { label: 'Auth failing', tone: 'crit' },
  source_mismatch: { label: 'Source mismatch', tone: 'crit' },
  config_error: { label: 'Config error', tone: 'crit' },
}

const toneClass: Record<Tone, { chip: string; dot: string }> = {
  good: { chip: 'border-status-good/35 bg-status-good/10', dot: 'bg-status-good' },
  info: { chip: 'border-status-info/35 bg-status-info/10', dot: 'bg-status-info' },
  warn: { chip: 'border-status-warn/45 bg-status-warn/12', dot: 'bg-status-warn' },
  crit: { chip: 'border-status-crit/40 bg-status-crit/10', dot: 'bg-status-crit' },
  idle: { chip: 'border-border bg-muted', dot: 'bg-status-idle' },
}

export function Chip({
  tone,
  children,
  className,
}: {
  tone: Tone
  children: React.ReactNode
  className?: string
}) {
  return (
    <span
      className={cn(
        'inline-flex h-6 max-w-full items-center gap-1.5 rounded-full border px-2 text-xs font-medium whitespace-nowrap text-foreground',
        toneClass[tone].chip,
        className,
      )}
    >
      <span aria-hidden className={cn('size-1.5 shrink-0 rounded-full', toneClass[tone].dot)} />
      <span className="truncate">{children}</span>
    </span>
  )
}

/** `suffix` adds context such as the offline duration ("Offline · 12 m"). */
export function StatusChip({ status, suffix, className }: { status: APStatus; suffix?: string; className?: string }) {
  const meta = apStatusMeta[status] ?? { label: status, tone: 'idle' as const }
  return (
    <Chip tone={meta.tone} className={className}>
      {suffix ? `${meta.label} · ${suffix}` : meta.label}
    </Chip>
  )
}
