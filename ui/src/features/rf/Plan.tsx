import { useEffect, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { ArrowRight, Loader2 } from 'lucide-react'
import { describeError } from '@/api/errors'
import { keys } from '@/api/keys'
import { planChannels, type PlanProposal, type PlanRow, type PlanStatus } from '@/api/queries/rf'
import { EmptyState } from '@/components/app/states'
import { Chip } from '@/components/app/StatusChip'
import { Button } from '@/components/ui/button'
import { impactLine } from '@/features/settings/kit/ConfigForm'
import { Dialog } from '@/features/settings/kit/primitives'
import { cn } from '@/lib/utils'

// The channel plan table and the planner preview (design §3.10 "Channel plan
// table", "Plan channels"). v1 never re-plans automatically: the operator
// previews, then applies.

const statusMeta: Record<PlanStatus, { label: string; tone: 'good' | 'warn' | 'info' | 'crit' | 'idle' }> = {
  match: { label: 'Match', tone: 'good' },
  mismatch: { label: 'Mismatch', tone: 'warn' },
  cac: { label: 'DFS check', tone: 'info' },
  radio_missing: { label: 'Radio missing', tone: 'crit' },
  offline: { label: 'Offline', tone: 'idle' },
}

function ModeCell({ siteId, row }: { siteId: string; row: PlanRow }) {
  if (row.mode === 'static') {
    return (
      <Link
        to={`/sites/${encodeURIComponent(siteId)}/aps/${encodeURIComponent(row.ap_id)}/settings` as never}
        hash="radios"
        className="font-medium text-brand underline-offset-2 hover:underline"
      >
        Static (AP override)
      </Link>
    )
  }
  return <span>Auto{row.source ? <span className="text-muted-foreground"> · {row.source}</span> : null}</span>
}

function Actual({ row }: { row: PlanRow }) {
  if (!row.actual) return <span className="text-muted-foreground">–</span>
  return (
    <span className="tabular-nums">
      {row.actual.channel}/{row.actual.width} · {row.actual.max_power_dbm} dBm
    </span>
  )
}

export function PlanTable({ siteId, rows }: { siteId: string; rows: PlanRow[] }) {
  if (!rows.length) return <EmptyState title="No APs in this site yet. Their radios appear here once they are added." />
  const sorted = rows.toSorted((a, b) => a.ap_name.localeCompare(b.ap_name) || a.band.localeCompare(b.band))
  return (
    <>
      <ul className="grid gap-3 md:hidden" aria-label="Channel plan">
        {sorted.map((row) => (
          <li key={`${row.ap_id}:${row.band}`} className="grid gap-2 rounded-xl border bg-card p-4 text-sm">
            <div className="flex items-start justify-between gap-2">
              <span className="font-semibold">
                {row.ap_name} · {row.band} GHz
              </span>
              <Chip tone={statusMeta[row.status].tone}>{statusMeta[row.status].label}</Chip>
            </div>
            <dl className="grid grid-cols-2 gap-x-4 gap-y-2">
              <div className="grid gap-0.5">
                <dt className="text-xs text-muted-foreground">Mode</dt>
                <dd>
                  <ModeCell siteId={siteId} row={row} />
                </dd>
              </div>
              <div className="grid gap-0.5">
                <dt className="text-xs text-muted-foreground">Planned</dt>
                <dd className="tabular-nums">
                  {row.channel}/{row.width} · {row.power_dbm} dBm
                </dd>
              </div>
              <div className="grid gap-0.5">
                <dt className="text-xs text-muted-foreground">Actual</dt>
                <dd>
                  <Actual row={row} />
                </dd>
              </div>
              <div className="grid gap-0.5">
                <dt className="text-xs text-muted-foreground">Load</dt>
                <dd className="tabular-nums">{row.actual ? `${Math.round(row.actual.util)} % · ${row.actual.clients} clients` : '–'}</dd>
              </div>
            </dl>
          </li>
        ))}
      </ul>
      <div className="hidden overflow-hidden rounded-xl border bg-card md:block">
        <table className="w-full text-sm">
          <caption className="sr-only">Channel plan per AP and band</caption>
          <thead className="border-b bg-muted/50 text-left text-xs text-muted-foreground">
            <tr>
              <th scope="col" className="px-4 py-2.5 font-medium">AP</th>
              <th scope="col" className="px-4 py-2.5 font-medium">Band</th>
              <th scope="col" className="px-4 py-2.5 font-medium">Mode</th>
              <th scope="col" className="px-4 py-2.5 font-medium">Planned</th>
              <th scope="col" className="hidden px-4 py-2.5 font-medium lg:table-cell">Actual</th>
              <th scope="col" className="hidden px-4 py-2.5 text-right font-medium xl:table-cell">Utilisation</th>
              <th scope="col" className="hidden px-4 py-2.5 text-right font-medium xl:table-cell">Clients</th>
              <th scope="col" className="px-4 py-2.5 font-medium">Status</th>
            </tr>
          </thead>
          <tbody className="divide-y">
            {sorted.map((row) => (
              <tr key={`${row.ap_id}:${row.band}`}>
                <td className="px-4 py-3 font-medium">{row.ap_name}</td>
                <td className="px-4 py-3">{row.band} GHz</td>
                <td className="px-4 py-3">
                  <ModeCell siteId={siteId} row={row} />
                </td>
                <td className="px-4 py-3 tabular-nums">
                  {row.channel}/{row.width} · {row.power_dbm} dBm
                </td>
                <td className="hidden px-4 py-3 lg:table-cell">
                  <Actual row={row} />
                </td>
                <td className="hidden px-4 py-3 text-right tabular-nums xl:table-cell">{row.actual ? `${Math.round(row.actual.util)} %` : '–'}</td>
                <td className="hidden px-4 py-3 text-right tabular-nums xl:table-cell">{row.actual?.clients ?? '–'}</td>
                <td className="px-4 py-3">
                  <Chip tone={statusMeta[row.status].tone}>{statusMeta[row.status].label}</Chip>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  )
}

function age(seconds: number | null) {
  if (seconds == null) return 'no scan yet'
  if (seconds < 120) return `${Math.round(seconds)} s old`
  if (seconds < 7200) return `${Math.round(seconds / 60)} min old`
  return `${Math.round(seconds / 3600)} h old`
}

/** "Plan channels": dry run, then apply. Changes under 25 % better show as "keep". */
export function PlanDialog({ siteId, onClose }: { siteId: string; onClose: () => void }) {
  const queryClient = useQueryClient()
  const [proposal, setProposal] = useState<PlanProposal | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    let cancelled = false
    ;(planChannels(siteId, { dryRun: true }) as Promise<PlanProposal>)
      .then((p) => !cancelled && setProposal(p))
      .catch((e: unknown) => !cancelled && setError(describeError(e).message))
    return () => {
      cancelled = true
    }
  }, [siteId])

  const moves = proposal?.changes.filter((c) => !c.keep) ?? []

  async function apply() {
    setBusy(true)
    try {
      const response = await planChannels(siteId, { inputsHash: proposal?.inputs_hash })
      toast.success('Channel plan applied', { description: 'revision' in response ? impactLine(response.impact) : undefined })
      void queryClient.invalidateQueries({ queryKey: keys.siteRF(siteId) })
      void queryClient.invalidateQueries({ queryKey: keys.siteChanges(siteId) })
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
      title="Plan channels"
      description={proposal ? (moves.length ? `${moves.length} radio${moves.length === 1 ? '' : 's'} would move; each restarts and its clients reconnect.` : 'The current channels are already the best choice.') : 'Working out the best channels…'}
      wide
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={() => void apply()} disabled={!proposal || !moves.length || busy}>
            {busy ? <Loader2 className="motion-safe:animate-spin" /> : null}
            Apply plan
          </Button>
        </>
      }
    >
      {error ? (
        <p role="alert" className="text-sm font-medium text-destructive">
          {error}
        </p>
      ) : !proposal ? (
        <p className="inline-flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 aria-hidden className="size-4 motion-safe:animate-spin" /> Running the planner (dry run)…
        </p>
      ) : (
        <div className="grid gap-4">
          <p className="text-sm text-muted-foreground">
            Inputs: {proposal.inputs.aps} AP{proposal.inputs.aps === 1 ? '' : 's'}; scans{' '}
            {Object.entries(proposal.inputs.scans_age_s)
              .map(([ap, s]) => `${ap} ${age(s)}`)
              .join(', ') || 'none'}
            .
          </p>
          <ul className="grid gap-2">
            {proposal.changes.map((c) => (
              <li key={`${c.ap_id}:${c.band}`} className={cn('grid gap-1 rounded-lg border p-3 text-sm', c.keep && 'bg-muted/40')}>
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-medium">
                    {c.ap_name ?? c.ap_id} · {c.band} GHz
                  </span>
                  {c.keep ? <Chip tone="idle">Keep</Chip> : <Chip tone="warn">Radio restarts</Chip>}
                </div>
                <span className="flex flex-wrap items-center gap-1.5 tabular-nums">
                  ch {c.from.channel} · {c.from.power_dbm} dBm <ArrowRight aria-label="to" className="size-3.5" /> ch {c.to.channel} · {c.to.power_dbm} dBm
                  {c.to.dfs_fallback ? <span className="text-muted-foreground"> (DFS fallback {c.to.dfs_fallback})</span> : null}
                </span>
                <span className="text-muted-foreground">
                  Cost {c.cost_before.toFixed(1)} → {c.cost_after.toFixed(1)} · {c.reason}
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </Dialog>
  )
}
