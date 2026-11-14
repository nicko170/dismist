import { AlertTriangle, CircleCheck, Loader2 } from 'lucide-react'
import type { Impact, ImpactAP } from '@/api/types'
import { describeError } from '@/api/errors'
import { cn } from '@/lib/utils'
import { disruptionMeta, toneDot, worstDisruption } from './StatusChip'

// The impact line of a dry run (design §3.0 "Saving", §4.8 "Impact"):
// "Applies to 2 APs · 5 GHz radio restarts · AP session kept". An empty AP
// list means nothing is pushed: "No AP changes".

function plural(n: number, one: string, many = `${one}s`) {
  return `${n} ${n === 1 ? one : many}`
}

/** How the change reaches the APs. Losing the session does not drop clients (live finding, §9 Q5). */
export function pushLine(aps: readonly ImpactAP[]): string {
  const reconnect = aps.filter((ap) => ap.connected && ap.push === 'reconnect').length
  const offline = aps.filter((ap) => !ap.connected).length
  const parts: string[] = []
  if (reconnect === 0 && aps.some((ap) => ap.connected)) parts.push('AP session kept')
  if (reconnect > 0) parts.push(reconnect === aps.length ? 'AP reconnects to apply' : `${plural(reconnect, 'AP')} reconnect to apply`)
  if (offline > 0) parts.push(`${plural(offline, 'AP')} offline, applies on reconnect`)
  return parts.join(' · ')
}

/** The whole impact line as one sentence of parts. */
export function impactLine(impact: Impact): string {
  if (!impact.aps.length) return 'No AP changes'
  const worst = worstDisruption([impact.disruption, ...impact.aps.flatMap((ap) => ap.disruption)])
  return [`Applies to ${plural(impact.aps.length, 'AP')}`, disruptionMeta[worst].label, pushLine(impact.aps)].filter(Boolean).join(' · ')
}

export function ImpactNotice({
  impact,
  loading = false,
  error,
  invalid = false,
  className,
}: {
  impact?: Impact | null
  loading?: boolean
  error?: unknown
  /** The form has errors: no dry run until they are fixed. */
  invalid?: boolean
  className?: string
}) {
  const base = cn('flex min-w-0 items-start gap-1.5 text-sm', className)
  if (invalid) {
    return (
      <p className={base}>
        <AlertTriangle aria-hidden className="mt-0.5 size-3.5 shrink-0 text-warning" />
        Fix the highlighted fields to see the impact.
      </p>
    )
  }
  if (error) {
    return (
      <p className={base}>
        <AlertTriangle aria-hidden className="mt-0.5 size-3.5 shrink-0 text-warning" />
        <span className="min-w-0 break-words">Impact unknown: {describeError(error).message}</span>
      </p>
    )
  }
  if (loading && !impact) {
    return (
      <p className={base}>
        <Loader2 aria-hidden className="mt-0.5 size-3.5 shrink-0 animate-spin" />
        Checking the impact…
      </p>
    )
  }
  if (!impact) return null
  const worst = worstDisruption([impact.disruption, ...impact.aps.flatMap((ap) => ap.disruption)])
  const tone = impact.aps.length ? disruptionMeta[worst].tone : 'good'
  return (
    <p className={cn(base, loading && 'opacity-70')} title={impact.summary || undefined}>
      {impact.aps.length ? (
        <span aria-hidden className={cn('mt-1.5 size-2 shrink-0 rounded-full', toneDot(tone))} />
      ) : (
        <CircleCheck aria-hidden className="mt-0.5 size-3.5 shrink-0 text-success" />
      )}
      <span className="min-w-0 break-words">{impactLine(impact)}</span>
    </p>
  )
}
