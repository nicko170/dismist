import { cn } from '@/lib/utils'

// Stacked channel utilisation with text (design §3.5 Radios tab): Tx, Rx in
// BSS, Rx other BSS, non-Wi-Fi, other (unknown Wi-Fi and undecodable) and
// idle. Every segment is also listed in words, so colour is never the only
// signal; segments are separated by a 2 px surface gap. Values are what the
// API sends: fractions from 0 to 1 (RadioView.util and util_parts, keyed
// tx, rx_in_bss, rx_other_bss, unknown_wifi, non_wifi, undecodable_wifi).

const parts = [
  { key: 'tx', label: 'Tx', className: 'bg-chart-1' },
  { key: 'rx_in_bss', label: 'Rx in BSS', className: 'bg-chart-2' },
  { key: 'rx_other_bss', label: 'Rx other BSS', className: 'bg-chart-4' },
  { key: 'non_wifi', label: 'Non-Wi-Fi', className: 'bg-chart-3' },
  { key: 'other', label: 'Other', className: 'bg-chart-5' },
] as const

export type UtilParts = Record<string, number | undefined>

/** Fractions (0 to 1) to percentages for drawing and text. */
export function normaliseUtil(util: number, raw: UtilParts | null | undefined) {
  const value = (key: string) => {
    const v = raw?.[key]
    return typeof v === 'number' && Number.isFinite(v) && v > 0 ? v * 100 : 0
  }
  const segments = parts.map((part) => ({
    ...part,
    value: part.key === 'other' ? value('unknown_wifi') + value('undecodable_wifi') : value(part.key),
  }))
  const known = segments.reduce((sum, s) => sum + s.value, 0)
  const busy = known > 0 ? known : Math.max(0, Math.min(100, (util || 0) * 100))
  return { segments: known > 0 ? segments : [], busy: Math.min(100, busy), idle: Math.max(0, 100 - busy) }
}

const pct = (v: number) => `${v < 10 && v > 0 ? v.toFixed(1) : Math.round(v)} %`

export function UtilBar({ util, parts: raw, className }: { util: number; parts?: UtilParts | null; className?: string }) {
  const { segments, busy, idle } = normaliseUtil(util, raw)
  return (
    <div className={cn('grid gap-2', className)}>
      <div
        role="img"
        aria-label={`Channel utilisation ${pct(busy)}`}
        className="flex h-2.5 w-full gap-[2px] overflow-hidden rounded-full bg-muted"
      >
        {segments.length
          ? segments
              .filter((s) => s.value > 0)
              .map((s) => <span key={s.key} className={cn('h-full first:rounded-l-full', s.className)} style={{ width: `${s.value}%` }} />)
          : busy > 0 && <span className="h-full rounded-l-full bg-chart-1" style={{ width: `${busy}%` }} />}
      </div>
      <ul className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
        {segments
          .filter((s) => s.value > 0)
          .map((s) => (
            <li key={s.key} className="inline-flex items-center gap-1.5">
              <span aria-hidden className={cn('size-2 rounded-sm', s.className)} />
              {s.label} <span className="tabular-nums text-foreground">{pct(s.value)}</span>
            </li>
          ))}
        <li className="inline-flex items-center gap-1.5">
          <span aria-hidden className="size-2 rounded-sm border bg-muted" />
          Idle <span className="tabular-nums text-foreground">{pct(idle)}</span>
        </li>
      </ul>
    </div>
  )
}
