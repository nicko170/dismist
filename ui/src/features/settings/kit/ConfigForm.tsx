import { useEffect, useRef, useState, type ReactNode } from 'react'
import { FormProvider, type FieldValues } from 'react-hook-form'
import { skipToken, useQuery } from '@tanstack/react-query'
import { CircleAlert, FileDiff, History, Info, Loader2, TriangleAlert } from 'lucide-react'
import { keys } from '@/api/keys'
import type { DiffEntry, Impact, MutationResponse, PushEvent, Warning } from '@/gen/api.gen'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Chip } from '@/components/app/StatusChip'
import { cn } from '@/lib/utils'
import { EXPERIMENTAL_HELP, ExperimentalBadge } from './badges'
import { ConfigFormProvider, type ConfigFormContextValue } from './context'
import { disruptionLabel } from './options'
import { ConfirmDialog, Dialog } from './primitives'
import type { ConfigFormController, ImpactState } from './useConfigForm'

// The frame of every config form: provider, form-level errors, the sticky
// save bar with the impact line (§3.0 "Saving"), and the stale,
// experimental-acknowledgement, confirmation and draft dialogs.

export function ConfigForm<V extends FieldValues, T>({
  controller,
  context,
  children,
  saveLabel = 'Save',
  className,
  alwaysShowBar = false,
  extraActions,
}: {
  controller: ConfigFormController<V, T>
  context: ConfigFormContextValue
  children: ReactNode
  saveLabel?: string
  className?: string
  /** Create forms show the bar from the start. */
  alwaysShowBar?: boolean
  extraActions?: ReactNode
}) {
  const c = controller
  const errorRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (c.formError.length) errorRef.current?.focus()
  }, [c.formError])
  return (
    <ConfigFormProvider value={{ ...context, readOnly: context.readOnly || c.readOnly }}>
      <FormProvider {...c.form}>
        <form onSubmit={c.save} noValidate className={cn('grid gap-4', className)} aria-busy={c.saving || undefined}>
          {c.draft ? (
            <Alert>
              <History />
              <AlertTitle>Unsaved changes from before you signed in</AlertTitle>
              <AlertDescription className="flex flex-wrap items-center gap-2 pt-1">
                <Button size="sm" onClick={() => c.resolveDraft(true)}>
                  Restore them
                </Button>
                <Button size="sm" variant="outline" onClick={() => c.resolveDraft(false)}>
                  Discard
                </Button>
              </AlertDescription>
            </Alert>
          ) : null}
          {c.formError.length ? (
            <div ref={errorRef} tabIndex={-1} className="outline-none">
              <Alert variant="destructive">
                <CircleAlert />
                <AlertTitle>{c.formError[0]}</AlertTitle>
                {c.formError.length > 1 ? (
                  <AlertDescription>
                    <ul className="list-disc pl-4">
                      {c.formError.slice(1).map((m) => (
                        <li key={m}>{m}</li>
                      ))}
                    </ul>
                  </AlertDescription>
                ) : null}
              </Alert>
            </div>
          ) : null}
          {children}
          <SaveBar controller={c} saveLabel={saveLabel} show={alwaysShowBar} extraActions={extraActions} />
        </form>
        <StaleDialog controller={c} />
        <ConfirmDialog
          open={Boolean(c.ack)}
          onOpenChange={(open) => !open && c.resolveAck(false)}
          title="Use experimental settings?"
          description={EXPERIMENTAL_HELP}
          action="I understand, save"
          onConfirm={() => c.resolveAck(true)}
        >
          <ul className="grid gap-1.5 text-sm">
            {c.ack?.labels.map((label) => (
              <li key={label} className="flex items-center gap-2">
                <ExperimentalBadge label="" className="px-1.5" />
                {label}
              </li>
            ))}
          </ul>
          <p className="text-sm text-muted-foreground">
            After the AP applies it, dismist checks the result and offers a one-click revert if the AP did something else.
          </p>
        </ConfirmDialog>
        <ConfirmDialog
          open={Boolean(c.confirming)}
          onOpenChange={(open) => !open && c.resolveConfirm(false)}
          title={c.confirming?.title ?? ''}
          description={c.confirming?.description}
          action={c.confirming?.action ?? 'Save'}
          destructive={c.confirming?.destructive}
          pending={c.saving}
          onConfirm={() => c.resolveConfirm(true)}
        />
      </FormProvider>
    </ConfigFormProvider>
  )
}

function StaleDialog<V extends FieldValues, T>({ controller: c }: { controller: ConfigFormController<V, T> }) {
  return (
    <ConfirmDialog
      open={Boolean(c.stale)}
      onOpenChange={(open) => !open && c.resolveStale('cancel')}
      title="This changed since you opened it"
      description="Someone else saved this object after you started editing. Reload to see their version (your edits are lost), or overwrite it with yours."
      action="Overwrite"
      destructive
      onConfirm={() => c.resolveStale('overwrite')}
    >
      <Button variant="outline" onClick={() => c.resolveStale('reload')}>
        Reload
      </Button>
    </ConfirmDialog>
  )
}

// ---- impact ----

export function impactLine(impact: Impact | undefined): string {
  if (!impact) return ''
  const n = impact.aps?.length ?? 0
  if (!n) return impact.summary && !/no ap/i.test(impact.summary) ? `${impact.summary}` : 'No AP changes'
  if (impact.summary) return impact.summary
  const disruption = disruptionLabel[impact.disruption] ?? impact.disruption
  const kept = impact.aps.every((ap) => ap.push === 'notify')
  return [`Applies to ${n} AP${n === 1 ? '' : 's'}`, disruption, kept ? 'AP session kept' : 'AP reconnects'].join(' · ')
}

export function ImpactNotice<T>({ state }: { state: ImpactState<T> }) {
  switch (state.status) {
    case 'idle':
      return null
    case 'invalid':
      return <span className="text-muted-foreground">Fix the highlighted fields to see the impact</span>
    case 'loading':
      return (
        <span className="inline-flex items-center gap-1.5 text-muted-foreground">
          <Loader2 aria-hidden className="size-3.5 motion-safe:animate-spin" />
          Checking impact…
        </span>
      )
    case 'error':
      return (
        <span className="inline-flex items-start gap-1.5 text-destructive">
          <CircleAlert aria-hidden className="mt-0.5 size-3.5 shrink-0" />
          <span>Can’t save: {state.error.message}</span>
        </span>
      )
    case 'ready':
      return <span>{impactLine(state.data.impact)}</span>
  }
}

export function WarningList({ warnings }: { warnings: Warning[] | undefined }) {
  if (!warnings?.length) return null
  return (
    <ul className="grid gap-1 text-sm">
      {warnings.map((w) => (
        <li key={`${w.code}:${w.message}`} className="flex items-start gap-1.5">
          <TriangleAlert aria-hidden className="mt-0.5 size-3.5 shrink-0 text-warning" />
          {w.message}
        </li>
      ))}
    </ul>
  )
}

function SaveBar<V extends FieldValues, T>({
  controller: c,
  saveLabel,
  show,
  extraActions,
}: {
  controller: ConfigFormController<V, T>
  saveLabel: string
  show: boolean
  extraActions?: ReactNode
}) {
  const [diffOpen, setDiffOpen] = useState(false)
  const ready = c.impact.status === 'ready' ? c.impact.data : null
  const visible = show || c.dirty || c.saving
  if (!visible) {
    return c.lastSaved && c.siteId ? <PushProgress siteId={c.siteId} response={c.lastSaved} /> : null
  }
  const n = c.changed.length
  return (
    <>
      <div
        role="region"
        aria-label="Save changes"
        className="sticky bottom-[calc(var(--shell-tabs)+env(safe-area-inset-bottom)+0.75rem)] z-30 grid gap-2 rounded-xl border bg-popover/95 p-3 shadow-lg shadow-foreground/5 backdrop-blur md:bottom-4 md:flex md:items-center md:gap-4 md:px-4"
      >
        <div className="grid min-w-0 flex-1 gap-0.5 text-sm" aria-live="polite">
          <span className="font-medium">
            {c.readOnly ? 'Read-only' : n ? `${n} change${n === 1 ? '' : 's'}` : 'No changes yet'}
          </span>
          <span className="min-w-0 text-xs break-words md:text-sm">
            <ImpactNotice state={c.impact} />
          </span>
          {ready?.warnings?.length ? <WarningList warnings={ready.warnings} /> : null}
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {extraActions}
          {ready?.impact?.aps?.some((ap) => ap.diff?.length) ? (
            <Button variant="ghost" onClick={() => setDiffOpen(true)}>
              <FileDiff /> View diff
            </Button>
          ) : null}
          <Button variant="outline" onClick={c.discard} disabled={!c.dirty || c.saving}>
            Discard
          </Button>
          <Button type="submit" disabled={c.saving || c.readOnly || (!c.dirty && !show)}>
            {c.saving ? <Loader2 className="motion-safe:animate-spin" /> : null}
            {c.saving ? 'Saving…' : saveLabel}
          </Button>
        </div>
      </div>
      <Dialog open={diffOpen} onOpenChange={setDiffOpen} title="Changes to the AP config" description={impactLine(ready?.impact)} wide>
        <ImpactDiff impact={ready?.impact} />
      </Dialog>
    </>
  )
}

function formatValue(value: unknown): string {
  if (value === undefined) return '(absent)'
  if (typeof value === 'string') return value
  return JSON.stringify(value)
}

/** The redacted per-path diff of a dry run (`impact.aps[].diff`). */
export function DiffTable({ diff, caption }: { diff: DiffEntry[]; caption?: string }) {
  if (!diff.length) return <p className="text-sm text-muted-foreground">No differences.</p>
  return (
    <div className="grid gap-2">
      {caption ? <p className="text-sm font-medium">{caption}</p> : null}
      <ul className="grid gap-2">
        {diff.map((entry) => (
          <li key={entry.path} className="grid gap-1 rounded-lg border p-2.5 text-sm md:grid-cols-[minmax(0,2fr)_minmax(0,1fr)_minmax(0,1fr)] md:gap-3">
            <span className="font-mono text-xs break-all">{entry.path}</span>
            <span className="min-w-0 font-mono text-xs break-all text-destructive line-through decoration-destructive/40">
              <span className="sr-only">from </span>
              {formatValue(entry.from)}
            </span>
            <span className="min-w-0 font-mono text-xs break-all text-success">
              <span className="sr-only">to </span>
              {formatValue(entry.to)}
            </span>
          </li>
        ))}
      </ul>
    </div>
  )
}

export function ImpactDiff({ impact }: { impact: Impact | undefined }) {
  if (!impact?.aps?.length) return <p className="text-sm text-muted-foreground">No AP changes.</p>
  return (
    <div className="grid gap-5">
      {impact.aps.map((ap) => (
        <div key={ap.id} className="grid gap-2">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="text-sm font-semibold">{ap.name || ap.id}</h3>
            <Chip tone={ap.connected ? 'good' : 'idle'}>{ap.connected ? 'Connected' : 'Offline: applies on reconnect'}</Chip>
            {ap.disruption?.map((d) => (
              <Chip key={d} tone={d === 'none' ? 'idle' : 'warn'}>
                {disruptionLabel[d] ?? d}
              </Chip>
            ))}
          </div>
          <DiffTable diff={ap.diff ?? []} />
        </div>
      ))}
    </div>
  )
}

const pushLabel: Record<PushEvent['state'], { label: string; tone: 'good' | 'info' | 'warn' | 'crit' | 'idle' }> = {
  notified: { label: 'Pending', tone: 'info' },
  fetched: { label: 'Fetched', tone: 'info' },
  applied: { label: 'Applied', tone: 'good' },
  rejected: { label: 'Rejected', tone: 'crit' },
  fallback_reconnect: { label: 'Reconnected', tone: 'warn' },
  timeout: { label: 'Timed out', tone: 'crit' },
}

/** Per-AP apply progress after a save, driven by SSE `push` events (§3.0). */
export function PushProgress<T>({ siteId, response }: { siteId: string; response: MutationResponse<T> }) {
  // Filled only by the site stream's `push` events; never fetched.
  const { data: pushes } = useQuery<Record<string, PushEvent>>({ queryKey: keys.sitePush(siteId), queryFn: skipToken })
  const aps = response.impact?.aps ?? []
  if (!aps.length) return null
  return (
    <div className="flex flex-wrap items-center gap-2 text-sm" aria-live="polite">
      <Info aria-hidden className="size-4 text-muted-foreground" />
      <span className="text-muted-foreground">Revision {response.revision}:</span>
      {aps.map((ap) => {
        const push = pushes?.[ap.id]
        const state = push && push.revision >= response.revision ? pushLabel[push.state] : null
        return (
          <Chip key={ap.id} tone={state?.tone ?? (ap.connected ? 'info' : 'idle')}>
            {ap.name || ap.id}: {state?.label ?? (ap.connected ? `Pending rev ${response.revision}` : 'Applies on reconnect')}
          </Chip>
        )
      })}
    </div>
  )
}
