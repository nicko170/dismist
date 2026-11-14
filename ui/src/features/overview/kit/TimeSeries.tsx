import { useEffect, useId, useMemo, useRef, useState } from 'react'
import { cn } from '@/lib/utils'

// A single-series time chart in SVG (design §3.3 "Clients, 24 h", §7.8):
// y-axis ticks, time ticks, a crosshair tooltip on hover or touch, a
// visually hidden text summary and a "Show data" table. One series, so the
// card title names it and there is no legend.

export type Point = { t: number; v: number | null }

const timeFmt = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit' })
const dayFmt = new Intl.DateTimeFormat(undefined, { weekday: 'short', hour: '2-digit' })
const fullFmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' })

export function niceTicks(max: number, count = 4): number[] {
  if (!(max > 0)) return [0, 1]
  const raw = max / count
  const mag = 10 ** Math.floor(Math.log10(raw))
  const step = [1, 2, 5, 10].map((m) => m * mag).find((s) => s >= raw) ?? raw
  const ticks: number[] = []
  for (let v = 0; v <= max + step * 0.001; v += step) ticks.push(Math.round(v * 1000) / 1000)
  if (ticks[ticks.length - 1] < max) ticks.push(ticks[ticks.length - 1] + step)
  return ticks
}

export function summarise(points: Point[], unit: string): string {
  const valid = points.filter((p): p is { t: number; v: number } => p.v !== null)
  if (!valid.length) return `No ${unit} data in this range.`
  const peak = valid.reduce((a, b) => (b.v > a.v ? b : a))
  const last = valid[valid.length - 1]
  return `Peak ${peak.v} ${unit} at ${timeFmt.format(peak.t)}; now ${last.v} ${unit}.`
}

export function TimeSeries({
  points,
  unit,
  label,
  height = 180,
  className,
}: {
  points: Point[]
  unit: string
  label: string
  height?: number
  className?: string
}) {
  const box = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(600)
  const [hover, setHover] = useState<number | null>(null)
  const summaryId = useId()

  useEffect(() => {
    const el = box.current
    if (!el || typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(([entry]) => setWidth(Math.max(240, Math.round(entry.contentRect.width))))
    observer.observe(el)
    return () => observer.disconnect()
  }, [])

  const geometry = useMemo(() => {
    const pad = { top: 10, right: 12, bottom: 24, left: 34 }
    const valid = points.filter((p) => p.v !== null) as { t: number; v: number }[]
    const t0 = points[0]?.t ?? 0
    const t1 = points[points.length - 1]?.t ?? 1
    const span = Math.max(1, t1 - t0)
    const ticks = niceTicks(Math.max(1, ...valid.map((p) => p.v)))
    const yMax = ticks[ticks.length - 1]
    const innerW = width - pad.left - pad.right
    const innerH = height - pad.top - pad.bottom
    const x = (t: number) => pad.left + ((t - t0) / span) * innerW
    const y = (v: number) => pad.top + innerH - (v / yMax) * innerH
    // Break the line at gaps (null values).
    const segments: string[] = []
    let current = ''
    for (const p of points) {
      if (p.v === null) {
        if (current) segments.push(current)
        current = ''
        continue
      }
      current += `${current ? 'L' : 'M'}${x(p.t).toFixed(1)},${y(p.v).toFixed(1)}`
    }
    if (current) segments.push(current)
    const multiDay = span > 36 * 3600_000
    const xTicks = Array.from({ length: 5 }, (_, i) => t0 + (span * i) / 4)
    return { pad, ticks, x, y, segments, xTicks, multiDay, innerH, innerW, t0, span }
  }, [points, width, height])

  const summary = summarise(points, unit)
  const hovered = hover !== null ? points[hover] : null

  const onMove = (clientX: number) => {
    const el = box.current
    if (!el || !points.length) return
    const rect = el.getBoundingClientRect()
    const t = geometry.t0 + ((clientX - rect.left - geometry.pad.left) / geometry.innerW) * geometry.span
    let best = 0
    for (let i = 1; i < points.length; i++) if (Math.abs(points[i].t - t) < Math.abs(points[best].t - t)) best = i
    setHover(best)
  }

  return (
    <figure className={cn('grid min-w-0 gap-2', className)}>
      <div
        ref={box}
        className="relative min-w-0 touch-pan-y"
        onPointerMove={(e) => onMove(e.clientX)}
        onPointerDown={(e) => onMove(e.clientX)}
        onPointerLeave={() => setHover(null)}
      >
        <svg width="100%" height={height} viewBox={`0 0 ${width} ${height}`} role="img" aria-label={label} aria-describedby={summaryId}>
          {geometry.ticks.map((tick) => (
            <g key={tick}>
              <line
                x1={geometry.pad.left}
                x2={width - geometry.pad.right}
                y1={geometry.y(tick)}
                y2={geometry.y(tick)}
                className="stroke-border"
                strokeWidth={1}
              />
              <text x={geometry.pad.left - 6} y={geometry.y(tick)} dy="0.32em" textAnchor="end" className="fill-muted-foreground text-[11px] tabular-nums">
                {tick}
              </text>
            </g>
          ))}
          {geometry.xTicks.map((t, i) => (
            <text
              key={t}
              x={geometry.x(t)}
              y={height - 6}
              textAnchor={i === 0 ? 'start' : i === 4 ? 'end' : 'middle'}
              className="fill-muted-foreground text-[11px] tabular-nums"
            >
              {(geometry.multiDay ? dayFmt : timeFmt).format(t)}
            </text>
          ))}
          {geometry.segments.map((d, i) => (
            <path key={i} d={d} fill="none" className="stroke-chart-2" strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />
          ))}
          {hovered && hovered.v !== null ? (
            <g>
              <line
                x1={geometry.x(hovered.t)}
                x2={geometry.x(hovered.t)}
                y1={geometry.pad.top}
                y2={geometry.pad.top + geometry.innerH}
                className="stroke-muted-foreground"
                strokeDasharray="3 3"
              />
              <circle cx={geometry.x(hovered.t)} cy={geometry.y(hovered.v)} r={4.5} className="fill-chart-2 stroke-card" strokeWidth={2} />
            </g>
          ) : null}
        </svg>
        {hovered ? (
          <div
            aria-hidden
            className="pointer-events-none absolute top-0 rounded-md border bg-popover px-2 py-1 text-xs shadow-sm"
            style={{
              left: Math.min(Math.max(0, geometry.x(hovered.t) - 60), width - 130),
            }}
          >
            <div className="text-muted-foreground">{fullFmt.format(hovered.t)}</div>
            <div className="font-medium tabular-nums">{hovered.v === null ? 'No data' : `${hovered.v} ${unit}`}</div>
          </div>
        ) : null}
      </div>
      <figcaption id={summaryId} className="sr-only">
        {summary}
      </figcaption>
      <details className="text-xs">
        <summary className="inline-flex min-h-11 cursor-pointer items-center text-muted-foreground hover:text-foreground fine:md:min-h-0">
          Show data
        </summary>
        <div className="mt-2 max-h-64 overflow-y-auto rounded-md border">
          <table className="w-full text-left">
            <caption className="sr-only">{label}</caption>
            <thead>
              <tr className="border-b bg-muted/50">
                <th scope="col" className="px-2 py-1 font-medium">
                  Time
                </th>
                <th scope="col" className="px-2 py-1 text-right font-medium">
                  {unit}
                </th>
              </tr>
            </thead>
            <tbody>
              {points.map((p) => (
                <tr key={p.t} className="border-b last:border-0">
                  <td className="px-2 py-1">{fullFmt.format(p.t)}</td>
                  <td className="px-2 py-1 text-right tabular-nums">{p.v ?? '–'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>
    </figure>
  )
}

/** A tiny inline trend line (no axes); always paired with a text value. */
export function Sparkline({ values, label, className }: { values: (number | null)[]; label: string; className?: string }) {
  const w = 96
  const h = 24
  const valid = values.filter((v): v is number => v !== null)
  if (valid.length < 2) return null
  const max = Math.max(1, ...valid)
  const step = w / Math.max(1, values.length - 1)
  let d = ''
  values.forEach((v, i) => {
    if (v === null) return
    d += `${d ? 'L' : 'M'}${(i * step).toFixed(1)},${(h - 2 - (v / max) * (h - 4)).toFixed(1)}`
  })
  return (
    <svg width={w} height={h} viewBox={`0 0 ${w} ${h}`} role="img" aria-label={label} className={cn('shrink-0', className)}>
      <path d={d} fill="none" className="stroke-chart-2" strokeWidth={1.5} strokeLinejoin="round" />
    </svg>
  )
}
