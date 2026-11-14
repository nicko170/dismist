import { useEffect, useRef } from 'react'
import uPlot from 'uplot'
import 'uplot/dist/uPlot.min.css'
import type { TimeSeriesProps } from './TimeSeries'

// The uPlot half of TimeSeries, split out so the library loads lazily.
// Colours come from the theme tokens at draw time and follow theme changes.

function token(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || 'currentColor'
}

function options(props: TimeSeriesProps, width: number): uPlot.Options {
  const grid = { stroke: token('--border'), width: 1 }
  const axis = { stroke: token('--muted-foreground'), grid, ticks: grid, font: '12px Geist Variable, system-ui, sans-serif' }
  return {
    width,
    height: props.height ?? 180,
    legend: { show: false },
    cursor: { drag: { x: false, y: false } },
    scales: { x: { time: true }, y: props.yRange ? { range: props.yRange } : {} },
    axes: [axis, { ...axis, size: 48, values: (_u, ticks) => ticks.map((t) => `${t}${props.unit ? ` ${props.unit}` : ''}`) }],
    series: [
      {},
      ...props.series.map((s) => ({
        label: s.label,
        stroke: token(`--chart-${s.color ?? 1}`),
        width: 1.5,
        points: { show: false },
      })),
    ],
  }
}

export default function TimeSeriesChart(props: TimeSeriesProps) {
  const host = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const element = host.current
    if (!element) return
    const data: uPlot.AlignedData = [Float64Array.from(props.times), ...props.series.map((s) => s.values.map((v) => v ?? null))]
    let plot = new uPlot(options(props, element.clientWidth || 300), data, element)
    const resize = new ResizeObserver(() => plot.setSize({ width: element.clientWidth, height: props.height ?? 180 }))
    resize.observe(element)
    // Redraw with the new tokens when the theme flips.
    const theme = new MutationObserver(() => {
      plot.destroy()
      plot = new uPlot(options(props, element.clientWidth || 300), data, element)
    })
    theme.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
    return () => {
      resize.disconnect()
      theme.disconnect()
      plot.destroy()
    }
  }, [props])
  return <div ref={host} className="size-full overflow-hidden" />
}
