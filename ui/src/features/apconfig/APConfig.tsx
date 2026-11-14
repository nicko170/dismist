import { useMemo, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, Copy, Download, FileDiff, History } from 'lucide-react'
import { apConfigQuery, apExpertQuery, apSettingsQuery, putAPExpert, type APConfigDocument } from '@/api/queries/apsettings'
import { changesQuery } from '@/api/queries/changes'
import { keys } from '@/api/keys'
import type { DiffEntry } from '@/gen/api.gen'
import { Mono } from '@/components/app/identifiers'
import { PageHeader } from '@/components/app/PageHeader'
import { RelativeTime } from '@/components/app/RelativeTime'
import { EmptyState, ErrorState, SkeletonRows } from '@/components/app/states'
import { Chip } from '@/components/app/StatusChip'
import { Button } from '@/components/ui/button'
import { usePageTitle } from '@/features/shell/title'
import { DiffTable } from '@/features/settings/kit/ConfigForm'
import { ExpertPatchEditor } from '@/features/settings/kit/ExpertPatchEditor'
import { UnsavedChangesGuard } from '@/features/settings/kit/guard'
import { JsonView } from '@/features/settings/kit/JsonEditor'
import { getOption, hasOption } from '@/features/settings/kit/options'
import { changedPaths } from '@/features/settings/kit/patch'
import { Dialog } from '@/features/settings/kit/primitives'
import { LegacyModeBanner, useTypedEditing } from '@/features/settings/kit/site'
import { cn } from '@/lib/utils'

// AP config (design §3.7): the redacted effective config with the compile
// trace, the AP-scope expert patch, and the history of revisions that
// affected this AP.

export type ConfigTab = 'effective' | 'expert' | 'history'

const TABS: { id: ConfigTab; label: string }[] = [
  { id: 'effective', label: 'Effective config' },
  { id: 'expert', label: 'Expert patch' },
  { id: 'history', label: 'History' },
]

export function APConfig({ siteId, apId, view }: { siteId: string; apId: string; view: ConfigTab }) {
  const ap = useQuery(apSettingsQuery(apId))
  const name = ap.data?.name || apId
  usePageTitle(`${name} config`)
  const base = `/sites/${encodeURIComponent(siteId)}/aps/${encodeURIComponent(apId)}/config`
  return (
    <div className="grid gap-5">
      <PageHeader title={`${name} config`} />
      <LegacyModeBanner />
      <nav aria-label="Config views" className="-mx-4 overflow-x-auto px-4 md:mx-0 md:px-0">
        <ul className="flex w-max gap-1 border-b md:w-auto">
          {TABS.map((tab) => (
            <li key={tab.id}>
              <Link
                to={base as never}
                search={{ view: tab.id } as never}
                aria-current={view === tab.id ? 'page' : undefined}
                className={cn(
                  '-mb-px inline-flex min-h-11 items-center border-b-2 border-transparent px-3 text-sm font-medium whitespace-nowrap text-muted-foreground hover:text-foreground fine:md:min-h-9',
                  view === tab.id && 'border-brand text-foreground',
                )}
              >
                {tab.label}
              </Link>
            </li>
          ))}
        </ul>
      </nav>
      {view === 'effective' ? <EffectiveView apId={apId} /> : null}
      {view === 'expert' ? <ExpertView siteId={siteId} apId={apId} name={name} /> : null}
      {view === 'history' ? <HistoryView siteId={siteId} apId={apId} /> : null}
      <UnsavedChangesGuard />
    </div>
  )
}

function leafDiff(from: unknown, to: unknown): DiffEntry[] {
  const read = (source: unknown, path: string) => path.split('.').reduce<unknown>((n, p) => (n as Record<string, unknown> | undefined)?.[p], source)
  return changedPaths(from, to).map((path) => ({ path, from: read(from, path), to: read(to, path) }))
}

const layerLabel: Record<string, string> = {
  template: 'Template',
  site: 'Site settings',
  site_expert: 'Site expert patch',
  ap: 'AP settings',
  ap_expert: 'AP expert patch',
  wlan: 'WLAN settings',
  wlan_expert: 'WLAN expert patch',
  protected: 'Protected',
}

function EffectiveView({ apId }: { apId: string }) {
  const config = useQuery(apConfigQuery(apId, 'effective'))
  const [compare, setCompare] = useState(false)
  const [copied, setCopied] = useState(false)
  if (config.isError) return <ErrorState error={config.error} title="Could not load the effective config" onRetry={() => void config.refetch()} />
  if (!config.data) return <SkeletonRows rows={4} />
  const doc = config.data
  const text = JSON.stringify(doc.config, null, 2)

  function download() {
    const blob = new Blob([text], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${apId}-config-rev${doc.revision}-redacted.json`
    a.click()
    URL.revokeObjectURL(url)
  }

  return (
    <div className="grid gap-4">
      <div className="grid gap-2 rounded-xl border bg-card p-4 text-sm md:p-5">
        <p>
          Revision <span className="font-medium tabular-nums">{doc.revision}</span> · effective hash <Mono>{doc.effective_hash ? doc.effective_hash.slice(0, 22) : 'none'}</Mono>
        </p>
        {doc.h0 ? (
          <p className="flex flex-wrap items-center gap-2">
            Baseline H0 (rev {doc.h0.config_revision}, <RelativeTime value={doc.h0.recorded_at} />):
            <Chip tone={doc.h0.matches == null ? 'idle' : doc.h0.matches ? 'good' : 'warn'}>
              {doc.h0.matches == null ? 'normaliser changed: recompute to compare' : doc.h0.matches ? 'unchanged' : 'differs'}
            </Chip>
          </p>
        ) : null}
        {doc.report?.ignored?.length ? <p className="text-muted-foreground">Ignored by AP33 0.15: {doc.report.ignored.join(', ')}</p> : null}
        <p className="text-muted-foreground">Secrets are redacted. This is the config dismist sends; what the AP runs is checked after each delivery.</p>
        <div className="flex flex-wrap gap-2 pt-1">
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              void navigator.clipboard?.writeText(text).then(() => {
                setCopied(true)
                window.setTimeout(() => setCopied(false), 1500)
              })
            }}
          >
            {copied ? <Check /> : <Copy />} {copied ? 'Copied' : 'Copy'}
          </Button>
          <Button variant="outline" size="sm" onClick={download}>
            <Download /> Download (redacted)
          </Button>
          <Button variant="outline" size="sm" onClick={() => setCompare(true)}>
            <FileDiff /> Compare with applied
          </Button>
        </div>
      </div>
      <JsonView value={text} ariaLabel={`Effective config, revision ${doc.revision}`} />
      {doc.trace?.length ? <Trace doc={doc} /> : null}
      {compare ? <CompareDialog apId={apId} effective={doc} onClose={() => setCompare(false)} /> : null}
    </div>
  )
}

function Trace({ doc }: { doc: APConfigDocument }) {
  return (
    <section aria-labelledby="trace-heading" className="grid gap-2">
      <h2 id="trace-heading" className="text-base font-semibold">
        Where each setting comes from
      </h2>
      <ul className="grid gap-1.5">
        {doc.trace!.map((t) => (
          <li key={t.key} className="flex flex-wrap items-center gap-2 rounded-lg border bg-card px-3 py-2 text-sm">
            <span className="font-medium">{hasOption(t.key) ? getOption(t.key).label : t.key}</span>
            <span className="text-muted-foreground">{layerLabel[t.layer] ?? t.layer}</span>
            {t.shadowed ? <Chip tone="warn">Overridden by expert patch</Chip> : null}
          </li>
        ))}
      </ul>
    </section>
  )
}

function CompareDialog({ apId, effective, onClose }: { apId: string; effective: APConfigDocument; onClose: () => void }) {
  const applied = useQuery(apConfigQuery(apId, 'applied'))
  const diff = useMemo(() => (applied.data ? leafDiff(applied.data.config, effective.config) : []), [applied.data, effective.config])
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()} title="Compare with applied" description={applied.data ? `Applied revision ${applied.data.revision} → current revision ${effective.revision}` : 'Loading the last delivered config…'} wide>
      {applied.isError ? <ErrorState error={applied.error} title="Could not load the applied config" /> : !applied.data ? <SkeletonRows rows={2} /> : diff.length ? <DiffTable diff={diff} /> : <p className="text-sm text-muted-foreground">The AP has the current config.</p>}
    </Dialog>
  )
}

function ExpertView({ siteId, apId, name }: { siteId: string; apId: string; name: string }) {
  const expert = useQuery(apExpertQuery(apId))
  const queryClient = useQueryClient()
  const { typed } = useTypedEditing()
  if (expert.isError) return <ErrorState error={expert.error} title="Could not load the expert patch" onRetry={() => void expert.refetch()} />
  if (!expert.data) return <SkeletonRows rows={3} />
  return (
    <ExpertPatchEditor
      scope="ap"
      heading={`Expert patch for AP ${name}`}
      view={expert.data}
      siteId={siteId}
      legacy={!typed}
      save={(patch, options) => putAPExpert(apId, { patch }, options)}
      onSaved={(response) => {
        queryClient.setQueryData(apExpertQuery(apId).queryKey, response.object)
        void queryClient.invalidateQueries({ queryKey: keys.apConfig(apId) })
        void queryClient.invalidateQueries({ queryKey: keys.ap(apId), exact: true })
      }}
    />
  )
}

function HistoryView({ siteId, apId }: { siteId: string; apId: string }) {
  const history = useInfiniteQuery(changesQuery(siteId, { ap: apId }))
  if (history.isError) return <ErrorState error={history.error} title="Could not load the history" onRetry={() => void history.refetch()} />
  if (!history.data) return <SkeletonRows rows={3} />
  const changes = history.data.pages.flatMap((p) => p.changes)
  if (!changes.length) return <EmptyState title="No config change has affected this AP yet." icon={<History />} />
  return (
    <div className="grid gap-3">
      <ul className="grid gap-2">
        {changes.map((c) => {
          const result = c.affected_aps.find((a) => a.id === apId)
          return (
            <li key={c.revision} className="relative grid gap-1 rounded-lg border bg-card p-3 text-sm hover:bg-accent/40">
              <div className="flex flex-wrap items-center gap-2">
                <Link to={`/sites/${encodeURIComponent(siteId)}/changes/${c.revision}` as never} className="inline-flex min-h-11 items-center font-medium after:absolute after:inset-0 after:content-['']">
                  Revision {c.revision}
                </Link>
                <RelativeTime value={c.at} />
                {result ? <ResultChip state={result.state} /> : null}
              </div>
              <span>{c.summary}</span>
              {result?.error ? <span className="text-destructive">{result.error}</span> : null}
            </li>
          )
        })}
      </ul>
      {history.hasNextPage ? (
        <Button variant="outline" className="justify-self-start" disabled={history.isFetchingNextPage} onClick={() => void history.fetchNextPage()}>
          Older revisions
        </Button>
      ) : null}
    </div>
  )
}

export function ResultChip({ state }: { state: string }) {
  const map: Record<string, { label: string; tone: 'good' | 'warn' | 'crit' | 'info' | 'idle' }> = {
    applied: { label: 'Applied', tone: 'good' },
    rejected: { label: 'Rejected', tone: 'crit' },
    pending: { label: 'Pending', tone: 'info' },
    timeout: { label: 'Timed out', tone: 'crit' },
    unchanged: { label: 'No change', tone: 'idle' },
    superseded: { label: 'Superseded', tone: 'idle' },
  }
  const m = map[state] ?? { label: state, tone: 'idle' as const }
  return <Chip tone={m.tone}>{m.label}</Chip>
}
