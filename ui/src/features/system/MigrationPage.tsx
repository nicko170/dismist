import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { migrationQuery, systemKeys, useMigrationFinalise, useMigrationRetry, type MigrationView } from '@/api/queries/system'
import { describeError } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { Chip } from '@/components/app/StatusChip'
import { Mono } from '@/components/app/identifiers'
import { RelativeTime } from '@/components/app/RelativeTime'
import { ErrorState, SkeletonRows } from '@/components/app/states'
import { Card, Facts } from '@/features/overview/kit/Card'
import { ConfirmDialog } from '@/features/overview/kit/ConfirmDialog'
import { ResponsiveList, type Column } from '@/features/overview/kit/ResponsiveList'
import type { MigrationAPReport } from '@/api/queries/system'

// Migration to schema 2 (design §3.14, §4.1 rules, §4.7): the state, the lift
// report (typed values per object, residual expert patches, per-AP
// verification and local-file status); in legacy mode the failing paths and
// Retry (dry run, then commit, no restart); after commit, Finalise (phase 4,
// only once M2 has run 30 days) which clears the legacy keys and raises
// compat to 2, so rolling back then needs the pre-migration copy.

const stateMeta: Record<MigrationView['state'], { label: string; tone: 'good' | 'warn' | 'idle'; text: string }> = {
  not_run: { label: 'Not run', tone: 'idle', text: 'This controller has not migrated its database to schema 2.' },
  committed: {
    label: 'Committed',
    tone: 'good',
    text: 'Every AP’s effective config was proven identical before the migration committed. The legacy fields are unchanged, so an older release still runs today’s config.',
  },
  legacy: {
    label: 'Legacy mode',
    tone: 'warn',
    text: 'The migration could not prove that every AP’s config stays identical, so nothing was written. dismist keeps running your existing config unchanged; typed settings are paused.',
  },
}

const value = (v: unknown): string => (typeof v === 'string' ? v : JSON.stringify(v))

function Lifted({ report }: { report: NonNullable<MigrationView['report']> }) {
  if (!report.objects.length) return <p className="text-sm text-muted-foreground">No objects were lifted.</p>
  return (
    <ul className="grid gap-4">
      {report.objects.map((object) => {
        const lifted = Object.entries(object.lifted ?? {})
        const residual = object.residual && typeof object.residual === 'object' && Object.keys(object.residual as object).length ? object.residual : null
        return (
          <li key={`${object.scope}-${object.id}`} className="grid min-w-0 gap-2 rounded-md border p-3">
            <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
              <Chip tone="idle">{object.scope.toUpperCase()}</Chip> {object.name || object.id}
            </p>
            {lifted.length ? (
              <dl className="grid gap-1 text-xs sm:grid-cols-2">
                {lifted.map(([k, v]) => (
                  <div key={k} className="flex min-w-0 gap-2">
                    <dt className="text-muted-foreground">{k}</dt>
                    <dd className="min-w-0 font-mono break-all">{value(v)}</dd>
                  </div>
                ))}
              </dl>
            ) : null}
            <p className="text-xs text-muted-foreground">
              Residual expert patch: {residual ? <Mono className="text-foreground">{JSON.stringify(residual)}</Mono> : 'none'}
            </p>
          </li>
        )
      })}
    </ul>
  )
}

function APReport({ aps }: { aps: MigrationAPReport[] }) {
  const columns: Column<MigrationAPReport>[] = [
    { id: 'ap', header: 'AP', render: (ap) => <span className="font-medium">{ap.name || ap.id}</span> },
    { id: 'verified', header: 'Effective config', render: (ap) => (ap.verified ? <Chip tone="good">Identical</Chip> : <Chip tone="crit">Differs</Chip>) },
    { id: 'files', header: 'Local files', render: (ap) => <Chip tone={ap.local_files === 'none' ? 'good' : ap.local_files === 'present' ? 'crit' : 'warn'}>{ap.local_files}</Chip> },
    { id: 'bssid', header: 'BSSID index', render: (ap) => ap.bssid_index || '–' },
    { id: 'diff', header: 'Mismatches', priority: 2, render: (ap) => (ap.mismatches.length ? <Mono className="text-xs">{ap.mismatches.join(', ')}</Mono> : 'None') },
  ]
  return (
    <ResponsiveList
      caption="Per-AP verification"
      rows={aps}
      rowKey={(ap) => ap.id}
      columns={columns}
      renderCard={(ap) => (
        <div className="grid gap-1">
          <span className="font-medium">{ap.name || ap.id}</span>
          <span className="flex flex-wrap gap-1.5">
            {ap.verified ? <Chip tone="good">Identical</Chip> : <Chip tone="crit">Differs</Chip>}
            <Chip tone={ap.local_files === 'none' ? 'good' : 'warn'}>local files: {ap.local_files}</Chip>
          </span>
          {ap.mismatches.length ? <Mono className="text-xs">{ap.mismatches.join(', ')}</Mono> : null}
        </div>
      )}
    />
  )
}

export function MigrationPage() {
  const qc = useQueryClient()
  const query = useQuery(migrationQuery)
  const retry = useMigrationRetry()
  const dryRun = useMigrationRetry()
  const finalise = useMigrationFinalise()
  const [confirmRetry, setConfirmRetry] = useState(false)
  const [confirmFinalise, setConfirmFinalise] = useState(false)

  if (query.isPending) return <SkeletonRows rows={3} />
  if (query.isError) return <ErrorState error={query.error} title="Could not load the migration state" onRetry={() => void query.refetch()} />
  const m = query.data
  const meta = stateMeta[m.state] ?? stateMeta.not_run
  const refresh = () => void qc.invalidateQueries({ queryKey: systemKeys.migration })

  return (
    <div className="grid min-w-0 gap-4">
      <Card title="State" action={<Chip tone={meta.tone}>{meta.label}</Chip>}>
        <div className="grid gap-3">
          <p className="text-sm">{meta.text}</p>
          <Facts
            items={[
              { label: 'When', value: <RelativeTime value={m.at} />, hidden: !m.at },
              { label: 'Reason', value: m.reason, hidden: !m.reason },
              { label: 'Schema · compat', value: `${m.schema} · ${m.compat}` },
              {
                label: 'Roll back to an older release',
                value: m.compat <= 1 ? 'Revert the image tag; the legacy fields reproduce the old config.' : 'Restore controller.db.pre-v2-* first, then revert the image tag.',
              },
              { label: 'Normaliser version', value: m.report?.normaliser_version, hidden: !m.report?.normaliser_version },
            ]}
          />
          {m.state === 'legacy' && m.paths.length ? (
            <div className="grid gap-1.5">
              <h3 className="text-sm font-semibold">Paths that would change</h3>
              <ul className="grid gap-1 rounded-md border bg-muted/40 p-3">
                {m.paths.map((path) => (
                  <li key={path}>
                    <Mono className="text-xs">{path}</Mono>
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
          {m.state === 'legacy' ? (
            <Button
              className="justify-self-start"
              onClick={() => {
                setConfirmRetry(true)
                dryRun.mutate({ dryRun: true })
              }}
            >
              Retry the migration
            </Button>
          ) : null}
        </div>
      </Card>

      {m.report ? (
        <>
          <Card title="Per-AP verification" bodyClassName="p-0 md:p-4">
            <div className="p-4 md:p-0">
              <APReport aps={m.report.aps} />
            </div>
          </Card>
          <Card title="Lifted values" description="Typed settings read from each object’s effective config, and what stays as an expert patch.">
            <Lifted report={m.report} />
          </Card>
        </>
      ) : m.state !== 'not_run' ? (
        <p className="text-sm text-muted-foreground">The migration report is not available from this controller.</p>
      ) : null}

      {m.state === 'committed' ? (
        <Card title="Finalise migration">
          <div className="grid gap-3 text-sm">
            <p>
              Finalising removes the legacy fields and sets compat to 2. After that, rolling dismist back to a release before schema 2 needs the pre-migration database copy.
            </p>
            {m.finalised ? (
              <Chip tone="good">Finalised</Chip>
            ) : m.finalise_available ? (
              <Button variant="destructive" className="justify-self-start" onClick={() => setConfirmFinalise(true)}>
                Finalise migration
              </Button>
            ) : (
              <p className="text-muted-foreground">
                Available {m.finalise_after ? <RelativeTime value={m.finalise_after} /> : 'after the migration has run for 30 days'}.
              </p>
            )}
          </div>
        </Card>
      ) : null}

      <ConfirmDialog
        open={confirmRetry}
        onOpenChange={(o) => {
          setConfirmRetry(o)
          if (!o) {
            dryRun.reset()
            retry.reset()
          }
        }}
        title="Retry the migration?"
        description={
          <>
            <p>dismist runs the migration again and commits only if every AP’s effective config stays identical. Nothing restarts and no config is pushed.</p>
            <p className="text-foreground">
              {dryRun.isPending
                ? 'Running a dry run…'
                : dryRun.isError
                  ? `The dry run failed: ${describeError(dryRun.error).message}`
                  : dryRun.isSuccess
                    ? 'The dry run passed.'
                    : ''}
            </p>
          </>
        }
        confirmLabel="Commit the migration"
        confirmDisabled={!dryRun.isSuccess}
        pending={retry.isPending}
        error={retry.error ? describeError(retry.error).message : null}
        onConfirm={() =>
          retry.mutate(
            { dryRun: false },
            {
              onSuccess: () => {
                toast.success('Migration committed')
                setConfirmRetry(false)
                refresh()
              },
            },
          )
        }
      />
      <ConfirmDialog
        open={confirmFinalise}
        onOpenChange={setConfirmFinalise}
        title="Finalise the schema 2 migration?"
        description={
          <>
            <p>The legacy fields are cleared and compat becomes 2. Older releases then refuse to start on this database.</p>
            <p>Download a backup first. This cannot be undone without restoring a backup.</p>
          </>
        }
        confirmLabel="Finalise"
        destructive
        typeToConfirm="finalise"
        pending={finalise.isPending}
        error={finalise.error ? describeError(finalise.error).message : null}
        onConfirm={() =>
          finalise.mutate(undefined, {
            onSuccess: () => {
              toast.success('Migration finalised')
              setConfirmFinalise(false)
              refresh()
            },
          })
        }
      />
    </div>
  )
}
