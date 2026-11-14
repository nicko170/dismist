import type { ReactNode } from 'react'
import type { APStatus, PendingReason, PushState, Severity } from '@/api/types'
import { cn } from '@/lib/utils'

// The one mapping from status enums to colour and words (design §3.4, §7.2).
// Every screen uses these tables, and Grafana's palette follows the same
// tones. Colour is never the only signal: every chip has text.

export type Tone = 'good' | 'info' | 'warn' | 'crit' | 'idle'

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

export const severityMeta: Record<Severity, { label: string; tone: Tone }> = {
  info: { label: 'Info', tone: 'info' },
  warning: { label: 'Warning', tone: 'warn' },
  error: { label: 'Error', tone: 'crit' },
}

/** Config push lifecycle (design §5.6), shown by PushProgress. */
export const pushStateMeta: Record<PushState, { label: string; tone: Tone }> = {
  notified: { label: 'Notified', tone: 'info' },
  fetched: { label: 'Fetched', tone: 'info' },
  applied: { label: 'Applied', tone: 'good' },
  rejected: { label: 'Rejected', tone: 'crit' },
  fallback_reconnect: { label: 'Reconnected to apply', tone: 'warn' },
  timeout: { label: 'Timed out', tone: 'crit' },
}

export const pendingReasonMeta: Record<PendingReason, { label: string; tone: Tone }> = {
  unregistered: { label: 'Not registered', tone: 'info' },
  source_mismatch: { label: 'Source address differs', tone: 'warn' },
  auth_failed: { label: 'Secret does not match', tone: 'crit' },
}

const toneClass: Record<Tone, { chip: string; dot: string }> = {
  good: { chip: 'border-status-good/35 bg-status-good/10', dot: 'bg-status-good' },
  info: { chip: 'border-status-info/35 bg-status-info/10', dot: 'bg-status-info' },
  warn: { chip: 'border-status-warn/45 bg-status-warn/12', dot: 'bg-status-warn' },
  crit: { chip: 'border-status-crit/40 bg-status-crit/10', dot: 'bg-status-crit' },
  idle: { chip: 'border-border bg-muted', dot: 'bg-status-idle' },
}

/** The dot colour of a tone, for compact markers that sit next to text. */
export function toneDot(tone: Tone): string {
  return toneClass[tone].dot
}

export function Chip({ tone, children, className, title }: { tone: Tone; children: ReactNode; className?: string; title?: string }) {
  return (
    <span
      title={title}
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

function metaChip<K extends string>(table: Record<K, { label: string; tone: Tone }>, value: K) {
  return table[value] ?? { label: String(value).replaceAll('_', ' '), tone: 'idle' as const }
}

/** `suffix` adds context such as the offline duration ("Offline · 12 m"). */
export function StatusChip({ status, suffix, className }: { status: APStatus; suffix?: string; className?: string }) {
  const meta = metaChip(apStatusMeta, status)
  return (
    <Chip tone={meta.tone} className={className}>
      {suffix ? `${meta.label} · ${suffix}` : meta.label}
    </Chip>
  )
}

export function SeverityChip({ severity, className }: { severity: Severity; className?: string }) {
  const meta = metaChip(severityMeta, severity)
  return (
    <Chip tone={meta.tone} className={className}>
      {meta.label}
    </Chip>
  )
}

export function PushStateChip({ state, revision, className }: { state: PushState | 'pending'; revision?: number; className?: string }) {
  const meta = state === 'pending' ? { label: 'Pending', tone: 'warn' as const } : metaChip(pushStateMeta, state)
  return (
    <Chip tone={meta.tone} className={className}>
      {revision !== undefined ? `${meta.label} rev ${revision}` : meta.label}
    </Chip>
  )
}

export function PendingReasonChip({ reason, className }: { reason: PendingReason; className?: string }) {
  const meta = metaChip(pendingReasonMeta, reason)
  return (
    <Chip tone={meta.tone} className={className}>
      {meta.label}
    </Chip>
  )
}

/** A yes/no state that is not good or bad by itself (Connected / Disconnected, On / Off). */
export function StateChip({ on, onLabel = 'On', offLabel = 'Off', className }: { on: boolean; onLabel?: string; offLabel?: string; className?: string }) {
  return (
    <Chip tone={on ? 'good' : 'idle'} className={className}>
      {on ? onLabel : offLabel}
    </Chip>
  )
}
