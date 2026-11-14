import { useEffect, useMemo, useState } from 'react'
import { Link, useNavigate } from '@tanstack/react-router'
import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { ArrowLeft, History, KeyRound, Loader2, RotateCcw, TriangleAlert } from 'lucide-react'
import { describeError, isApiError, type ApiError } from '@/api/errors'
import { keys } from '@/api/keys'
import { changeQuery, changesQuery, restoreChange, type ChangeDetail, type ChangeSummary, type RestoreInput, type RestorePreview } from '@/api/queries/changes'
import type { MutationResponse } from '@/gen/api.gen'
import { PageHeader } from '@/components/app/PageHeader'
import { RelativeTime } from '@/components/app/RelativeTime'
import { EmptyState, ErrorState, SkeletonRows } from '@/components/app/states'
import { Chip } from '@/components/app/StatusChip'
import { Alert, AlertTitle } from '@/components/ui/alert'
import { Button, buttonVariants } from '@/components/ui/button'
import { usePageTitle } from '@/features/shell/title'
import { ResultChip } from '@/features/apconfig/APConfig'
import { DiffTable, ImpactDiff, impactLine, WarningList } from '@/features/settings/kit/ConfigForm'
import { PasswordInput } from '@/features/settings/kit/fields'
import { useTypedEditing } from '@/features/settings/kit/site'

// Config history (design §3.13, §4.11): every committed change with its
// actor, summary and per-AP result; the diff of a revision; and Restore,
// always previewed with a dry run that lists what it will do and every secret
// it cannot restore by itself.

function actorLabel(change: ChangeSummary) {
  if (change.actor.kind === 'system') return change.actor.name || 'dismist'
  return change.actor.name || (change.actor.kind === 'token' ? 'API token' : 'Signed-in session')
}

export function ChangesList({ siteId }: { siteId: string }) {
  const changes = useInfiniteQuery(changesQuery(siteId))
  const list = changes.data?.pages.flatMap((p) => p.changes) ?? []
  const base = `/sites/${encodeURIComponent(siteId)}/changes`
  return (
    <div className="grid gap-5">
      <PageHeader title="Changes" meta="Every saved config change, newest first. Open one to see its diff or restore it." />
      {changes.isError ? (
        <ErrorState error={changes.error} title="Could not load the history" onRetry={() => void changes.refetch()} />
      ) : !changes.data ? (
        <SkeletonRows rows={4} />
      ) : !list.length ? (
        <EmptyState title="No config changes yet." icon={<History />}>
          Saving a WLAN, the radio settings or site settings records a revision here.
        </EmptyState>
      ) : (
        <>
          <ul className="grid gap-2" aria-label="Config revisions">
            {list.map((change) => (
              <li key={change.revision} className="relative grid gap-1.5 rounded-xl border bg-card p-4 text-sm hover:bg-accent/40 md:grid-cols-[6rem_minmax(0,1fr)_minmax(0,16rem)] md:items-start md:gap-4">
                <Link to={`${base}/${change.revision}` as never} className="font-semibold tabular-nums after:absolute after:inset-0 after:content-['']">
                  Rev {change.revision}
                </Link>
                <div className="grid min-w-0 gap-0.5">
                  <span className="break-words">{change.summary}</span>
                  <span className="text-xs text-muted-foreground">
                    <RelativeTime value={change.at} /> · {actorLabel(change)}
                    {change.kind === 'action' ? ' · action' : ''}
                  </span>
                </div>
                <div className="flex flex-wrap gap-1.5 md:justify-end">
                  {change.affected_aps.length ? (
                    change.affected_aps.map((ap) => (
                      <span key={ap.id} className="inline-flex items-center gap-1 text-xs">
                        {ap.name || ap.id}: <ResultChip state={ap.state} />
                      </span>
                    ))
                  ) : (
                    <span className="text-xs text-muted-foreground">No AP changes</span>
                  )}
                </div>
              </li>
            ))}
          </ul>
          {changes.hasNextPage ? (
            <Button variant="outline" className="justify-self-start" disabled={changes.isFetchingNextPage} onClick={() => void changes.fetchNextPage()}>
              {changes.isFetchingNextPage ? <Loader2 className="motion-safe:animate-spin" /> : null}
              Older changes
            </Button>
          ) : null}
        </>
      )}
    </div>
  )
}

export function ChangeDetailPage({ siteId, revision }: { siteId: string; revision: number }) {
  const change = useQuery(changeQuery(siteId, revision))
  usePageTitle(`Change ${revision}`)
  const base = `/sites/${encodeURIComponent(siteId)}/changes`
  return (
    <div className="grid gap-5">
      <Link to={base as never} className={buttonVariants({ variant: 'ghost', size: 'sm', className: 'justify-self-start' })}>
        <ArrowLeft /> All changes
      </Link>
      <PageHeader title={`Change ${revision}`} meta={change.data?.summary} />
      {change.isError ? (
        <ErrorState error={change.error} title="Could not load this revision" onRetry={() => void change.refetch()} />
      ) : !change.data ? (
        <SkeletonRows rows={4} />
      ) : (
        <ChangeBody siteId={siteId} change={change.data} />
      )}
    </div>
  )
}

function ChangeBody({ siteId, change }: { siteId: string; change: ChangeDetail }) {
  const [restoring, setRestoring] = useState(false)
  const { typed } = useTypedEditing()
  return (
    <>
      <section aria-labelledby="change-summary" className="grid gap-2 rounded-xl border bg-card p-4 text-sm md:p-5">
        <h2 id="change-summary" className="sr-only">
          Summary
        </h2>
        <p className="break-words">{change.summary}</p>
        <p className="text-muted-foreground">
          <RelativeTime value={change.at} /> · {actorLabel(change)} · <span className="font-mono text-xs">{change.endpoint}</span>
        </p>
        {change.affected_aps.length ? (
          <ul className="grid gap-1">
            {change.affected_aps.map((ap) => (
              <li key={ap.id} className="flex flex-wrap items-center gap-2">
                {ap.name || ap.id}: <ResultChip state={ap.state} />
                {ap.error ? <span className="text-destructive">{ap.error}</span> : null}
              </li>
            ))}
          </ul>
        ) : null}
        {change.restorable && change.kind !== 'action' ? (
          <Button className="justify-self-start" onClick={() => setRestoring(true)} disabled={!typed}>
            <RotateCcw /> Restore this revision
          </Button>
        ) : null}
      </section>
      {restoring ? <RestorePanel siteId={siteId} revision={change.revision} onClose={() => setRestoring(false)} /> : null}
      {change.objects.map((object) => (
        <section key={`${object.kind}:${object.id}`} aria-label={`${object.kind} ${object.name}`} className="grid gap-2">
          <h2 className="text-base font-semibold">
            {object.kind === 'network' ? 'WLAN' : object.kind === 'ap' ? 'AP' : object.kind === 'rf' ? 'Radio settings' : 'Site'} {object.name}
          </h2>
          <DiffTable diff={object.diff} />
        </section>
      ))}
      {change.aps.length ? (
        <section aria-labelledby="ap-diffs" className="grid gap-3">
          <h2 id="ap-diffs" className="text-base font-semibold">
            Effect on each AP’s config
          </h2>
          {change.aps.map((ap) => (
            <div key={ap.id} className="grid gap-2">
              <h3 className="text-sm font-semibold">{ap.name || ap.id}</h3>
              <DiffTable diff={ap.diff} />
            </div>
          ))}
        </section>
      ) : null}
    </>
  )
}

type SecretChoice = { value: string; keep: boolean }

/** Turns the 422 secret_required fields into one input per secret. */
export function secretFields(error: ApiError): { path: string; message: string }[] {
  return Object.entries(error.fields).map(([path, message]) => ({ path, message }))
}

export function RestorePanel({ siteId, revision, onClose }: { siteId: string; revision: number; onClose: () => void }) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [keepNew, setKeepNew] = useState(false)
  const [secrets, setSecrets] = useState<Record<string, SecretChoice>>({})
  const [needed, setNeeded] = useState<{ path: string; message: string }[]>([])
  const [preview, setPreview] = useState<MutationResponse<RestorePreview> | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [busy, setBusy] = useState(false)

  const body = useMemo<RestoreInput>(() => {
    const out: RestoreInput['secrets'] = {}
    for (const { path } of needed) {
      const choice = secrets[path]
      if (!choice) continue
      if (choice.keep) out[path] = { keep: true }
      else if (choice.value) out[path] = { value: choice.value }
    }
    return { keep_new_wlans: keepNew, ...(Object.keys(out).length ? { secrets: out } : {}) }
  }, [keepNew, needed, secrets])

  async function check(input: RestoreInput) {
    setLoading(true)
    setError(null)
    try {
      const response = await restoreChange(siteId, revision, input, { dryRun: true })
      setPreview(response)
    } catch (e) {
      setPreview(null)
      if (isApiError(e) && e.code === 'secret_required') {
        setNeeded((prev) => {
          const next = secretFields(e)
          const known = new Set(prev.map((p) => p.path))
          return [...prev, ...next.filter((n) => !known.has(n.path))]
        })
        setError(e.message || 'Some secrets differ from today’s. Enter them, or keep the current ones.')
      } else setError(describeError(e).message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void check({ keep_new_wlans: keepNew })
    // Re-run when the keep-new choice changes; secrets are checked on demand.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [keepNew])

  const missing = needed.filter(({ path }) => !secrets[path]?.keep && !secrets[path]?.value)

  async function apply() {
    setBusy(true)
    try {
      const response = await restoreChange(siteId, revision, body)
      toast.success(`Restored revision ${revision}`, { description: `New revision ${response.revision} · ${impactLine(response.impact)}` })
      void queryClient.invalidateQueries({ queryKey: keys.site(siteId) })
      void queryClient.invalidateQueries({ queryKey: ['network'] })
      void queryClient.invalidateQueries({ queryKey: ['ap'] })
      void navigate({ to: `/sites/${encodeURIComponent(siteId)}/changes` as never })
    } catch (e) {
      setError(describeError(e).message)
    } finally {
      setBusy(false)
    }
  }

  const p = preview?.object
  return (
    <section aria-labelledby="restore-heading" className="grid gap-4 rounded-xl border-2 border-brand/40 bg-card p-4 md:p-5">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="grid gap-1">
          <h2 id="restore-heading" className="text-base font-semibold">
            Restore revision {revision}
          </h2>
          <p className="text-sm text-muted-foreground">A dry run first: nothing changes until you confirm. The restore is saved as a new revision.</p>
        </div>
        <Button variant="ghost" size="sm" onClick={onClose}>
          Cancel
        </Button>
      </div>

      {needed.length ? (
        <fieldset className="grid gap-3 rounded-lg border p-3">
          <legend className="flex items-center gap-2 px-1 text-sm font-medium">
            <KeyRound aria-hidden className="size-4" /> Secrets this restore needs
          </legend>
          {needed.map(({ path, message }) => {
            const choice = secrets[path] ?? { value: '', keep: false }
            const set = (next: Partial<SecretChoice>) => setSecrets((s) => ({ ...s, [path]: { ...choice, ...next } }))
            const id = `secret-${path.replace(/[^a-z0-9]/gi, '-')}`
            return (
              <div key={path} className="grid gap-2">
                <label htmlFor={id} className="text-sm">
                  {message}
                </label>
                {!choice.keep ? <PasswordInput id={id} value={choice.value} onChange={(value) => set({ value })} generate={false} /> : null}
                <label className="flex min-h-11 items-center gap-3 text-sm">
                  <input type="checkbox" className="size-5 accent-brand" checked={choice.keep} onChange={(e) => set({ keep: e.target.checked })} />
                  Keep the current value
                </label>
              </div>
            )
          })}
          <Button variant="outline" className="justify-self-start" disabled={loading || missing.length > 0} onClick={() => void check(body)}>
            {loading ? <Loader2 className="motion-safe:animate-spin" /> : null}
            Check again
          </Button>
        </fieldset>
      ) : null}

      {error ? (
        <Alert variant={needed.length ? 'warning' : 'destructive'}>
          <TriangleAlert />
          <AlertTitle>{error}</AlertTitle>
        </Alert>
      ) : null}
      {loading && !preview ? (
        <p className="inline-flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 aria-hidden className="size-4 motion-safe:animate-spin" /> Checking what the restore would do…
        </p>
      ) : null}

      {p ? (
        <div className="grid gap-3 text-sm">
          <p className="font-medium">{impactLine(preview!.impact)}</p>
          {p.changed.length ? (
            <div>
              <span className="font-medium">Changed back:</span> {p.changed.map((c) => c.name).join(', ')}
            </div>
          ) : null}
          {p.recreated.length ? (
            <div className="grid gap-1">
              <span className="font-medium">WLANs re-created:</span>
              {p.recreated.map((w) => (
                <span key={w.id}>
                  {w.ssid}
                  {w.bssid_warning ? <span className="text-warning"> · {w.bssid_warning}</span> : null}
                </span>
              ))}
            </div>
          ) : null}
          {p.removed.length || keepNew ? (
            <div className="grid gap-1">
              {p.removed.length ? (
                <>
                  <span className="font-medium">WLANs created since then are removed:</span>
                  {p.removed.map((w) => (
                    <span key={w.id}>
                      {w.ssid} <Chip tone="warn">{w.clients} client{w.clients === 1 ? '' : 's'}</Chip>
                    </span>
                  ))}
                </>
              ) : null}
              <label className="flex min-h-11 items-center gap-3">
                <input type="checkbox" className="size-5 accent-brand" checked={keepNew} onChange={(e) => setKeepNew(e.target.checked)} />
                Keep WLANs created after this revision
              </label>
            </div>
          ) : null}
          {p.not_restored.length ? (
            <div className="grid gap-1">
              <span className="font-medium">Not restored:</span>
              {p.not_restored.map((n) => (
                <span key={`${n.kind}:${n.id}`} className="text-muted-foreground">
                  {n.name}: {n.reason}
                </span>
              ))}
            </div>
          ) : null}
          <WarningList warnings={preview!.warnings} />
          <details className="rounded-lg border p-3">
            <summary className="min-h-11 cursor-pointer content-center font-medium">What changes on each AP</summary>
            <div className="pt-3">
              <ImpactDiff impact={preview!.impact} />
            </div>
          </details>
          <Button className="justify-self-start" disabled={busy || missing.length > 0} onClick={() => void apply()}>
            {busy ? <Loader2 className="motion-safe:animate-spin" /> : <RotateCcw />}
            Restore revision {revision}
          </Button>
        </div>
      ) : null}
    </section>
  )
}
