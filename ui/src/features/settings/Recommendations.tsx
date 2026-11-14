import { useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { Lightbulb, Loader2 } from 'lucide-react'
import { describeError } from '@/api/errors'
import { keys } from '@/api/keys'
import { applyRecommendation, recommendationsKey, type Recommendation } from '@/api/queries/recommendations'
import type { MutationResponse } from '@/gen/api.gen'
import { EmptyState } from '@/components/app/states'
import { Chip } from '@/components/app/StatusChip'
import { Button } from '@/components/ui/button'
import { ImpactDiff, impactLine, WarningList } from './kit/ConfigForm'
import { disruptionLabel } from './kit/options'
import { Dialog } from './kit/primitives'

// Recommendations (design §4.6, §3.10, §3.11 Advanced): each is a named diff
// applied through a normal, previewed mutation. Preview = apply?dry_run=1.

export function RecommendationList({ siteId, items, readOnly, emptyText = 'No recommendations. This site already follows the research defaults.' }: { siteId: string; items: Recommendation[]; readOnly?: boolean; emptyText?: string }) {
  const [active, setActive] = useState<Recommendation | null>(null)
  if (!items.length) return <EmptyState title={emptyText} icon={<Lightbulb />} />
  return (
    <>
      <ul className="grid gap-3">
        {items.map((rec) => (
          <li key={rec.id} className="grid gap-2 rounded-lg border bg-card p-4 md:flex md:items-start md:justify-between md:gap-4">
            <div className="grid min-w-0 gap-1">
              <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
                <Lightbulb aria-hidden className="size-4 text-warning" />
                {rec.title}
              </p>
              {rec.detail ? <p className="text-sm text-muted-foreground">{rec.detail}</p> : null}
              {rec.disruption && rec.disruption !== 'none' ? (
                <span>
                  <Chip tone="warn">{disruptionLabel[rec.disruption] ?? rec.disruption}</Chip>
                </span>
              ) : null}
            </div>
            {rec.informational ? (
              <span className="text-xs text-muted-foreground">Information only</span>
            ) : (
              <Button variant="outline" size="sm" disabled={readOnly} onClick={() => setActive(rec)}>
                Preview
              </Button>
            )}
          </li>
        ))}
      </ul>
      {active ? <ApplyDialog siteId={siteId} rec={active} onClose={() => setActive(null)} /> : null}
    </>
  )
}

function ApplyDialog({ siteId, rec, onClose }: { siteId: string; rec: Recommendation; onClose: () => void }) {
  const queryClient = useQueryClient()
  const [preview, setPreview] = useState<MutationResponse<unknown> | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    let cancelled = false
    applyRecommendation(siteId, rec.id, { dryRun: true })
      .then((p) => !cancelled && setPreview(p))
      .catch((e: unknown) => !cancelled && setError(describeError(e).message))
    return () => {
      cancelled = true
    }
  }, [siteId, rec.id])

  async function apply() {
    setBusy(true)
    try {
      const response = await applyRecommendation(siteId, rec.id)
      toast.success('Recommendation applied', { description: `Revision ${response.revision} · ${impactLine(response.impact)}` })
      void queryClient.invalidateQueries({ queryKey: keys.site(siteId) })
      void queryClient.invalidateQueries({ queryKey: recommendationsKey(siteId) })
      onClose()
    } catch (e) {
      setError(describeError(e).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog
      open
      onOpenChange={(open) => !open && onClose()}
      title={rec.title}
      description={preview ? impactLine(preview.impact) : 'Checking what this changes…'}
      wide
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={() => void apply()} disabled={!preview || busy}>
            {busy ? <Loader2 className="motion-safe:animate-spin" /> : null}
            Apply
          </Button>
        </>
      }
    >
      <div className="grid gap-4">
        {rec.detail ? <p className="text-sm">{rec.detail}</p> : null}
        {error ? (
          <p role="alert" className="text-sm font-medium text-destructive">
            {error}
          </p>
        ) : null}
        {!preview && !error ? (
          <p className="inline-flex items-center gap-2 text-sm text-muted-foreground">
            <Loader2 aria-hidden className="size-4 motion-safe:animate-spin" /> Running a dry run…
          </p>
        ) : null}
        {preview ? (
          <>
            <WarningList warnings={preview.warnings} />
            <ImpactDiff impact={preview.impact} />
          </>
        ) : null}
      </div>
    </Dialog>
  )
}
