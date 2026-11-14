import { useId } from 'react'
import { cn } from '@/lib/utils'

// A trend line without axes for tiles and table cells (§7.3). It is drawn
// in SVG (no chart library) and summarised in text for screen readers.

export function sparkPath(values: readonly (number | null)[], width = 100, height = 24): { line: string; area: string } {
  const points = values.map((value, index) => ({ index, value })).filter((p): p is { index: number; value: number } => p.value != null && Number.isFinite(p.value))
  if (points.length < 2) return { line: '', area: '' }
  const min = Math.min(...points.map((p) => p.value))
  const max = Math.max(...points.map((p) => p.value))
  const span = max - min || 1
  const step = width / Math.max(1, values.length - 1)
  const xy = points.map((p) => [p.index * step, height - 1 - ((p.value - min) / span) * (height - 2)] as const)
  const line = xy.map(([x, y], i) => `${i ? 'L' : 'M'}${x.toFixed(2)} ${y.toFixed(2)}`).join(' ')
  const area = `${line} L${xy.at(-1)![0].toFixed(2)} ${height} L${xy[0][0].toFixed(2)} ${height} Z`
  return { line, area }
}

export function sparkSummary(label: string, values: readonly (number | null)[], unit = ''): string {
  const known = values.filter((v): v is number => v != null && Number.isFinite(v))
  if (!known.length) return `${label}: no data`
  const fmt = (v: number) => `${Math.round(v * 10) / 10}${unit ? ` ${unit}` : ''}`
  return `${label}: latest ${fmt(known.at(-1)!)}, low ${fmt(Math.min(...known))}, high ${fmt(Math.max(...known))}, ${known.length} samples`
}

export function Sparkline({
  values,
  label,
  unit,
  className,
  stroke = 'stroke-brand',
  fill = 'fill-brand/10',
}: {
  values: readonly (number | null)[]
  label: string
  unit?: string
  className?: string
  stroke?: string
  fill?: string
}) {
  const id = useId()
  const { line, area } = sparkPath(values)
  return (
    <span className={cn('relative block h-6 w-full', className)}>
      <svg aria-hidden viewBox="0 0 100 24" preserveAspectRatio="none" className="absolute inset-0 size-full overflow-visible">
        {area ? <path d={area} className={fill} /> : null}
        {line ? <path d={line} className={cn('fill-none', stroke)} strokeWidth={1.5} vectorEffect="non-scaling-stroke" strokeLinejoin="round" /> : null}
      </svg>
      <span id={id} className="sr-only">
        {sparkSummary(label, values, unit)}
      </span>
    </span>
  )
}
