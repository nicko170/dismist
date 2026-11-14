import { lazy, Suspense, useState } from 'react'
import { Table2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { formatAbsolute } from '@/lib/time'
import { cn } from '@/lib/utils'
import { sparkSummary } from './Sparkline'

// A chart with axes (design §7.3, §7.8): uPlot, loaded only when a chart is
// on screen. Every chart has a hidden text summary and a "Show data" table,
// so the numbers are never only in pixels.

export type Series = {
  label: string
  values: readonly (number | null)[]
  /** A chart token: 1 to 5. */
  color?: 1 | 2 | 3 | 4 | 5
}

export type TimeSeriesProps = {
  /** Accessible name and heading of the chart. */
  label: string
  /** Sample times, seconds since the epoch, one per value. */
  times: readonly number[]
  series: readonly Series[]
  unit?: string
  height?: number
  /** Fixed y range, e.g. [0, 100] for utilisation. */
  yRange?: [number, number]
  className?: string
}

const Chart = lazy(() => import('./TimeSeriesChart'))

export function TimeSeries(props: TimeSeriesProps) {
  const { label, times, series, unit, height = 180, className } = props
  const [table, setTable] = useState(false)
  return (
    <figure className={cn('grid min-w-0 gap-2', className)}>
      <figcaption className="sr-only">
        {label}. {series.map((s) => sparkSummary(s.label, s.values, unit)).join('. ')}
      </figcaption>
      <div aria-hidden className="min-w-0" style={{ height }}>
        <Suspense fallback={<Skeleton className="size-full" />}>
          <Chart {...props} height={height} />
        </Suspense>
      </div>
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1">
        <ul className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
          {series.map((s) => (
            <li key={s.label} className="inline-flex items-center gap-1.5">
              <span aria-hidden className={cn('h-0.5 w-3 rounded-full', chartBg[s.color ?? 1])} />
              {s.label}
            </li>
          ))}
        </ul>
        <Button variant="ghost" size="sm" aria-expanded={table} onClick={() => setTable((v) => !v)} className="text-muted-foreground">
          <Table2 /> {table ? 'Hide data' : 'Show data'}
        </Button>
      </div>
      {table ? (
        <div className="max-h-72 overflow-auto rounded-md border">
          <table className="w-full text-sm tabular-nums">
            <caption className="sr-only">{label}</caption>
            <thead className="sticky top-0 bg-muted text-left text-xs text-muted-foreground">
              <tr>
                <th scope="col" className="px-3 py-2 font-medium">
                  Time
                </th>
                {series.map((s) => (
                  <th key={s.label} scope="col" className="px-3 py-2 text-right font-medium">
                    {s.label}
                    {unit ? ` (${unit})` : ''}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody className="divide-y">
              {times.map((time, index) => (
                <tr key={time}>
                  <td className="px-3 py-1.5 whitespace-nowrap">{formatAbsolute(new Date(time * 1000))}</td>
                  {series.map((s) => (
                    <td key={s.label} className="px-3 py-1.5 text-right">
                      {s.values[index] ?? '–'}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
    </figure>
  )
}

export const chartBg = {
  1: 'bg-chart-1',
  2: 'bg-chart-2',
  3: 'bg-chart-3',
  4: 'bg-chart-4',
  5: 'bg-chart-5',
} as const
