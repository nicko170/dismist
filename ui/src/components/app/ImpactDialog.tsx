import { useState, type ReactNode } from 'react'
import { ChevronDown, Loader2, TriangleAlert } from 'lucide-react'
import type { Impact, Warning } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { Dialog, DialogContent } from '@/components/ui/dialog'
import { cn } from '@/lib/utils'
import { DiffView } from './DiffView'
import { impactLine, pushLine } from './ImpactNotice'
import { DisruptionChip, StateChip, disruptionMeta, worstDisruption } from './StatusChip'

// The impact preview of a dry run (design §4.8, P6): which APs change, how
// disruptive it is for each, the redacted per-AP diff, and the warnings,
// before the operator confirms. Used for "View diff" from the save bar, for
// restores, recommendations, RF plans and expert patches.

export function ImpactDialog({
  open,
  onOpenChange,
  title,
  description,
  impact,
  warnings = [],
  loading = false,
  onConfirm,
  confirmLabel = 'Save',
  pending = false,
  children,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: ReactNode
  description?: ReactNode
  impact?: Impact | null
  warnings?: readonly Warning[]
  loading?: boolean
  /** Omit for a read-only preview (only Close). */
  onConfirm?: () => void
  confirmLabel?: string
  pending?: boolean
  /** Extra choices (for example "Keep WLANs created after this revision"). */
  children?: ReactNode
}) {
  const worst = impact ? worstDisruption([impact.disruption, ...impact.aps.flatMap((ap) => ap.disruption)]) : 'none'
  const risky = disruptionMeta[worst].rank >= 3
  return (
    <Dialog open={open} onOpenChange={(next) => !pending && onOpenChange(next)}>
      <DialogContent
        title={title}
        description={description}
        footer={
          <>
            <Button variant="outline" onClick={() => onOpenChange(false)} disabled={pending}>
              {onConfirm ? 'Cancel' : 'Close'}
            </Button>
            {onConfirm ? (
              <Button variant={risky ? 'destructive' : 'brand'} onClick={onConfirm} disabled={pending || loading || !impact}>
                {pending ? <Loader2 aria-hidden className="animate-spin" /> : null}
                {confirmLabel}
              </Button>
            ) : null}
          </>
        }
      >
        {loading && !impact ? (
          <p className="flex items-center gap-2 text-sm text-muted-foreground">
            <Loader2 aria-hidden className="size-4 animate-spin" /> Checking the impact…
          </p>
        ) : impact ? (
          <div className="grid gap-4">
            <div className="grid gap-1.5" aria-live="polite">
              <p className="text-sm font-medium">{impactLine(impact)}</p>
              {impact.summary ? <p className="text-sm text-muted-foreground">{impact.summary}</p> : null}
              {impact.aps.length ? <p className="text-sm text-muted-foreground">{disruptionMeta[worst].detail}</p> : null}
            </div>
            {warnings.length ? (
              <ul className="grid gap-2" aria-label="Warnings">
                {warnings.map((warning) => (
                  <li key={warning.code + warning.message} className="flex gap-2 rounded-md border border-status-warn/45 bg-status-warn/8 px-3 py-2 text-sm">
                    <TriangleAlert aria-hidden className="mt-0.5 size-4 shrink-0 text-warning" />
                    <span className="min-w-0 break-words">{warning.message}</span>
                  </li>
                ))}
              </ul>
            ) : null}
            {children}
            {impact.aps.length ? (
              <ul className="grid gap-2" aria-label="Access points that change">
                {impact.aps.map((ap) => (
                  <ImpactAPRow key={ap.id} ap={ap} defaultOpen={impact.aps.length === 1} />
                ))}
              </ul>
            ) : null}
          </div>
        ) : null}
      </DialogContent>
    </Dialog>
  )
}

function ImpactAPRow({ ap, defaultOpen }: { ap: Impact['aps'][number]; defaultOpen: boolean }) {
  const [open, setOpen] = useState(defaultOpen)
  const diff = ap.diff ?? []
  return (
    <li className="rounded-md border">
      <Collapsible open={open} onOpenChange={setOpen}>
        <CollapsibleTrigger
          disabled={!diff.length}
          className="group flex min-h-12 w-full cursor-pointer items-start gap-3 px-3 py-2.5 text-left outline-none focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring disabled:cursor-default"
        >
          <span className="grid min-w-0 flex-1 gap-1.5">
            <span className="font-medium break-words">{ap.name || ap.id}</span>
            <span className="flex flex-wrap gap-1.5">
              <StateChip on={ap.connected} onLabel="Connected" offLabel="Offline" />
              {(ap.disruption.length ? ap.disruption : (['none'] as const)).map((d) => (
                <DisruptionChip key={d} disruption={d} />
              ))}
            </span>
            <span className="text-xs text-muted-foreground">{pushLine([ap])}</span>
          </span>
          {diff.length ? (
            <span className="flex shrink-0 items-center gap-1 pt-0.5 text-xs text-muted-foreground">
              {diff.length === 1 ? '1 path' : `${diff.length} paths`}
              <ChevronDown aria-hidden className={cn('size-4 transition-transform duration-150', open && 'rotate-180')} />
            </span>
          ) : null}
        </CollapsibleTrigger>
        <CollapsibleContent className="px-3 pb-3">
          <DiffView entries={diff} label={`Changes on ${ap.name || ap.id}`} />
        </CollapsibleContent>
      </Collapsible>
    </li>
  )
}
