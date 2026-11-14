import { formatAbsolute, formatRelative, parseTime, useNow } from '@/lib/time'

/** "12 s ago" with the absolute time in title; re-rendered by the shared 15 s clock. */
export function RelativeTime({ value, fallback = 'Never' }: { value: string | null | undefined; fallback?: string }) {
  const now = useNow()
  const date = parseTime(value)
  if (!date) return <span className="text-muted-foreground">{fallback}</span>
  return (
    <time dateTime={date.toISOString()} title={formatAbsolute(date)} className="tabular-nums">
      {formatRelative(date, now)}
    </time>
  )
}
