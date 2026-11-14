import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { CheckCircle2, Loader2, PauseCircle, RotateCcw, ShieldAlert } from 'lucide-react'
import { describeError } from '@/api/errors'
import { keys } from '@/api/keys'
import { finaliseMigration, migrationQuery, retryMigration, type MigrationReport } from '@/api/queries/sites'
import { KV } from '@/components/app/KV'
import { PageHeader } from '@/components/app/PageHeader'
import { RelativeTime } from '@/components/app/RelativeTime'
import { ErrorState, SkeletonRows } from '@/components/app/states'
import { Chip } from '@/components/app/StatusChip'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { JsonView } from './kit/JsonEditor'
import { ConfirmDialog } from './kit/primitives'

// /system/migration (design §3.14, §4.7): the schema-2 migration state, the
// lift report, Retry from legacy mode (dry run, then commit) and, in phase 4,
// Finalise. Workstream E owns the /system routes; this component is mounted by
// routes/system/migration.tsx.

export function MigrationPage() {
  const migration = useQuery(migrationQuery)
  return (
    <div className="grid gap-5">
      <PageHeader title="Migration" meta="Schema 2: typed settings lifted from the live config, proven equivalent before commit." />
      {migration.isError ? (
        <ErrorState error={migration.error} title="Could not load the migration state" onRetry={() => void migration.refetch()} />
      ) : !migration.data ? (
        <SkeletonRows rows={3} />
      ) : (
        <MigrationBody report={migration.data} />
      )}
    </div>
  )
}

function MigrationBody({ report }: { report: MigrationReport }) {
  const queryClient = useQueryClient()
  const [preview, setPreview] = useState<MigrationReport | null>(null)
  const [busy, setBusy] = useState<'retry' | 'commit' | 'finalise' | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [finalising, setFinalising] = useState(false)

  async function retry(dryRun: boolean) {
    setBusy(dryRun ? 'retry' : 'commit')
    setError(null)
    try {
      const result = await retryMigration({ dryRun })
      if (dryRun) setPreview(result)
      else {
        queryClient.setQueryData(migrationQuery.queryKey, result)
        void queryClient.invalidateQueries({ queryKey: keys.system })
        setPreview(null)
        toast[result.state === 'committed' ? 'success' : 'error'](result.state === 'committed' ? 'Migration committed' : 'Still in legacy mode', { description: result.reason || undefined })
      }
    } catch (e) {
      setError(describeError(e).message)
    } finally {
      setBusy(null)
    }
  }

  async function finalise() {
    setBusy('finalise')
    try {
      const result = await finaliseMigration()
      queryClient.setQueryData(migrationQuery.queryKey, result)
      void queryClient.invalidateQueries({ queryKey: keys.system })
      toast.success('Migration finalised')
      setFinalising(false)
    } catch (e) {
      setError(describeError(e).message)
    } finally {
      setBusy(null)
    }
  }

  const shown = preview ?? report
  return (
    <>
      {report.state === 'committed' ? (
        <Alert>
          <CheckCircle2 className="text-success" />
          <AlertTitle>Schema 2 committed{report.at ? ' ' : ''}{report.at ? <RelativeTime value={report.at} /> : null}.</AlertTitle>
          <AlertDescription>
            Typed settings are live. The legacy fields are kept byte-identical, so {report.compat <= 1 ? 'rolling back the image needs no database restore.' : 'rolling back now needs a database restore first (compat 2).'}
          </AlertDescription>
        </Alert>
      ) : (
        <Alert variant="warning">
          <PauseCircle />
          <AlertTitle>{report.state === 'legacy' ? 'Legacy mode: dismist is running your existing config unchanged.' : 'The migration has not run yet.'}</AlertTitle>
          <AlertDescription className="grid gap-2">
            {report.reason ? <p>{report.reason}</p> : null}
            {report.paths.length ? (
              <ul className="list-disc pl-4 font-mono text-xs">
                {report.paths.map((p) => (
                  <li key={p}>{p}</li>
                ))}
              </ul>
            ) : null}
            <div className="flex flex-wrap gap-2">
              <Button variant="outline" size="sm" disabled={busy !== null} onClick={() => void retry(true)}>
                {busy === 'retry' ? <Loader2 className="motion-safe:animate-spin" /> : <RotateCcw />} Retry (dry run)
              </Button>
              {preview?.state === 'committed' ? (
                <Button size="sm" disabled={busy !== null} onClick={() => void retry(false)}>
                  {busy === 'commit' ? <Loader2 className="motion-safe:animate-spin" /> : null} Commit migration
                </Button>
              ) : null}
            </div>
          </AlertDescription>
        </Alert>
      )}
      {preview ? (
        <Alert variant={preview.state === 'committed' ? 'default' : 'destructive'}>
          <ShieldAlert />
          <AlertTitle>Dry run: {preview.state === 'committed' ? 'every AP verifies; nothing changes on any AP.' : `would stay in legacy mode. ${preview.reason}`}</AlertTitle>
        </Alert>
      ) : null}
      {error ? (
        <p role="alert" className="text-sm font-medium text-destructive">
          {error}
        </p>
      ) : null}

      <KV
        items={[
          { label: 'State', value: shown.state },
          { label: 'Rollback compatibility', value: shown.compat <= 1 ? 'compat 1: an older release still runs this database' : 'compat 2: restore before rolling back' },
          { label: 'APs verified', value: shown.report ? `${shown.report.aps.filter((a) => a.verified).length} of ${shown.report.aps.length}` : '–' },
        ]}
      />

      {shown.report ? (
        <>
          <section aria-labelledby="mig-aps" className="grid gap-2">
            <h2 id="mig-aps" className="text-base font-semibold">
              Verification per AP
            </h2>
            <ul className="grid gap-2">
              {shown.report.aps.map((ap) => (
                <li key={ap.id} className="flex flex-wrap items-center gap-2 rounded-lg border bg-card p-3 text-sm">
                  <span className="font-medium">{ap.name || ap.id}</span>
                  <Chip tone={ap.verified ? 'good' : 'crit'}>{ap.verified ? 'Effective config identical' : 'Differs'}</Chip>
                  <Chip tone={ap.local_files === 'present' ? 'crit' : ap.local_files === 'unknown' ? 'warn' : 'good'}>Local files: {ap.local_files}</Chip>
                  {ap.note ? <span className="text-muted-foreground">{ap.note}</span> : null}
                </li>
              ))}
            </ul>
          </section>
          <section aria-labelledby="mig-bssid" className="grid gap-2">
            <h2 id="mig-bssid" className="text-base font-semibold">
              BSSID indexes
            </h2>
            <ul className="grid gap-1 text-sm">
              {shown.report.bssid_indexes.map((b) => (
                <li key={b.network_id} className="flex flex-wrap items-center gap-2">
                  {b.ssid}: index {b.index} <Chip tone={b.source === 'measured' ? 'good' : 'warn'}>{b.source}</Chip>
                </li>
              ))}
            </ul>
          </section>
          <section aria-labelledby="mig-lifted" className="grid gap-2">
            <h2 id="mig-lifted" className="text-base font-semibold">
              Lifted into typed settings
            </h2>
            {shown.report.lifted.map((l) => (
              <details key={`${l.scope}:${l.object_id}`} className="rounded-lg border bg-card p-3">
                <summary className="min-h-11 cursor-pointer content-center text-sm font-medium">
                  {l.scope} {l.name}
                </summary>
                <JsonView value={l.values} ariaLabel={`Lifted values for ${l.scope} ${l.name}`} />
              </details>
            ))}
          </section>
          <section aria-labelledby="mig-residual" className="grid gap-2">
            <h2 id="mig-residual" className="text-base font-semibold">
              Residual expert patches
            </h2>
            {shown.report.residuals.length ? (
              shown.report.residuals.map((r) => (
                <details key={`${r.scope}:${r.object_id}`} className="rounded-lg border bg-card p-3">
                  <summary className="min-h-11 cursor-pointer content-center text-sm font-medium">
                    {r.scope} {r.name}
                  </summary>
                  <JsonView value={r.patch} ariaLabel={`Residual patch for ${r.scope} ${r.name}`} />
                </details>
              ))
            ) : (
              <p className="text-sm text-muted-foreground">None: every live setting fits a typed field.</p>
            )}
          </section>
        </>
      ) : null}

      {report.state === 'committed' ? (
        <section aria-labelledby="mig-finalise" className="grid gap-2 rounded-xl border border-destructive/40 bg-card p-4 md:p-5">
          <h2 id="mig-finalise" className="text-base font-semibold">
            Finalise migration
          </h2>
          <p className="text-sm text-muted-foreground">
            Clears the legacy fields and sets compat 2. After this, rolling back to a release without schema 2 needs a database restore.{' '}
            {report.finalise.allowed ? '' : report.finalise.reason}
          </p>
          <Button variant="destructive" className="justify-self-start" disabled={!report.finalise.allowed || busy !== null} onClick={() => setFinalising(true)}>
            Finalise…
          </Button>
          <ConfirmDialog
            open={finalising}
            onOpenChange={setFinalising}
            title="Finalise the schema-2 migration?"
            description="The legacy fields are removed. Download a backup first."
            action="Finalise"
            destructive
            confirmText="finalise"
            pending={busy === 'finalise'}
            onConfirm={() => void finalise()}
          />
        </section>
      ) : null}
    </>
  )
}
