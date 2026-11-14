import type { Band, Signal } from '@/api/types'
import { formatDbm, formatPercent } from '@/lib/format'
import { cn } from '@/lib/utils'
import type { Tone } from './StatusChip'

// Radio vocabulary shared by the AP, radio, client and RF screens (§7.3):
// signal quality, channel badges and utilisation bars. Each carries its
// meaning in text; colour only reinforces it.

const signalMeta: Record<Signal, { label: string; tone: Tone; bars: number }> = {
  excellent: { label: 'Excellent', tone: 'good', bars: 4 },
  good: { label: 'Good', tone: 'good', bars: 3 },
  fair: { label: 'Fair', tone: 'warn', bars: 2 },
  poor: { label: 'Poor', tone: 'crit', bars: 1 },
}

const barTone: Record<Tone, string> = {
  good: 'bg-status-good',
  info: 'bg-status-info',
  warn: 'bg-status-warn',
  crit: 'bg-status-crit',
  idle: 'bg-status-idle',
}

/** Signal bucket plus RSSI: "Good −54 dBm" with four bars. */
export function SignalChip({ signal, rssi, className }: { signal: Signal; rssi?: number | null; className?: string }) {
  const meta = signalMeta[signal] ?? { label: signal, tone: 'idle' as const, bars: 0 }
  return (
    <span className={cn('inline-flex items-center gap-2 text-sm whitespace-nowrap', className)}>
      <span aria-hidden className="inline-flex h-3.5 items-end gap-[2px]">
        {[1, 2, 3, 4].map((bar) => (
          <span
            key={bar}
            className={cn('w-[3px] rounded-[1px]', bar <= meta.bars ? barTone[meta.tone] : 'bg-muted-foreground/25')}
            style={{ height: `${bar * 25}%` }}
          />
        ))}
      </span>
      <span>
        {meta.label}
        {rssi != null ? <span className="ml-1.5 text-muted-foreground tabular-nums">{formatDbm(rssi)}</span> : null}
      </span>
    </span>
  )
}

const bandClass: Record<Band, { edge: string; fill: string }> = {
  '2.4': { edge: 'border-l-band-24', fill: 'bg-band-24/15' },
  '5': { edge: 'border-l-band-5', fill: 'bg-band-5/15' },
}

/**
 * "5 │ 149/80": band, primary channel and width. A scan radio (no band) reads
 * "Scan"; `dfs` adds a DFS marker.
 */
export function ChannelBadge({
  band,
  channel,
  width,
  dfs = false,
  className,
}: {
  band: Band | null
  channel: number
  width?: number
  dfs?: boolean
  className?: string
}) {
  if (!band) {
    return (
      <span className={cn('inline-flex h-6 items-center rounded-md border px-2 text-xs font-medium text-muted-foreground', className)}>Scan</span>
    )
  }
  const spoken = `${band} GHz channel ${channel}${width ? `, ${width} MHz` : ''}${dfs ? ', DFS' : ''}`
  return (
    <span
      className={cn(
        'inline-flex h-6 items-stretch overflow-hidden rounded-md border border-l-[3px] bg-card text-xs whitespace-nowrap tabular-nums',
        bandClass[band].edge,
        className,
      )}
    >
      <span className="sr-only">{spoken}</span>
      <span aria-hidden className={cn('flex items-center px-1.5 font-semibold', bandClass[band].fill)}>
        {band}
      </span>
      <span aria-hidden className="flex items-center px-1.5 font-medium">
        {channel || '–'}
        {width ? <span className="text-muted-foreground">/{width}</span> : null}
        {dfs ? <span className="ml-1 rounded-sm bg-status-warn/20 px-1 text-[0.6875rem] font-semibold">DFS</span> : null}
      </span>
    </span>
  )
}

/** Segments of radio utilisation (design §5.2 field 31.30), in drawing order. */
export const utilParts: { key: string; label: string; className: string }[] = [
  { key: 'tx', label: 'Transmit', className: 'bg-chart-2' },
  { key: 'rx_bss', label: 'Receive (own clients)', className: 'bg-chart-4' },
  { key: 'rx_other', label: 'Receive (other networks)', className: 'bg-chart-3' },
  { key: 'unknown_wifi', label: 'Other Wi-Fi', className: 'bg-chart-5' },
  { key: 'non_wifi', label: 'Interference', className: 'bg-status-crit' },
  { key: 'undecodable', label: 'Undecodable', className: 'bg-muted-foreground/60' },
]

function utilTone(util: number): string {
  if (util >= 70) return 'bg-status-crit'
  if (util >= 40) return 'bg-status-warn'
  return 'bg-foreground/55'
}

/**
 * Channel utilisation (0–100 %) as a bar with the number beside it. With
 * `parts` it stacks the segments and lists them for screen readers.
 */
export function UtilBar({
  util,
  parts,
  label = 'Utilisation',
  className,
}: {
  util: number
  parts?: Record<string, number>
  label?: string
  className?: string
}) {
  const clamped = Math.max(0, Math.min(100, util || 0))
  const segments = parts ? utilParts.filter((part) => (parts[part.key] ?? 0) > 0) : []
  const summary = segments.length
    ? `${label} ${formatPercent(clamped)}: ${segments.map((part) => `${part.label.toLowerCase()} ${formatPercent(parts![part.key])}`).join(', ')}`
    : `${label} ${formatPercent(clamped)}`
  return (
    <span className={cn('flex min-w-0 items-center gap-2', className)}>
      <span aria-hidden className="flex h-2 min-w-12 flex-1 overflow-hidden rounded-full bg-muted">
        {segments.length ? (
          segments.map((part) => (
            <span key={part.key} className={cn('h-full', part.className)} style={{ width: `${Math.min(100, parts![part.key])}%` }} />
          ))
        ) : (
          <span className={cn('h-full rounded-full', utilTone(clamped))} style={{ width: `${clamped}%` }} />
        )}
      </span>
      <span aria-hidden className="w-10 shrink-0 text-right text-sm tabular-nums">
        {formatPercent(clamped)}
      </span>
      <span className="sr-only">{summary}</span>
    </span>
  )
}

/** The legend for stacked UtilBars (once per card, not per bar). */
export function UtilLegend({ className }: { className?: string }) {
  return (
    <ul className={cn('flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground', className)} aria-label="Utilisation legend">
      {utilParts.map((part) => (
        <li key={part.key} className="inline-flex items-center gap-1.5">
          <span aria-hidden className={cn('size-2 rounded-sm', part.className)} />
          {part.label}
        </li>
      ))}
    </ul>
  )
}

/** "2.4 GHz" / "5 GHz" with the band's colour marker. */
export function BandLabel({ band, className }: { band: Band; className?: string }) {
  return (
    <span className={cn('inline-flex items-center gap-1.5 whitespace-nowrap', className)}>
      <span aria-hidden className={cn('size-2 rounded-full', band === '5' ? 'bg-band-5' : 'bg-band-24')} />
      {band} GHz
    </span>
  )
}
