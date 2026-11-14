import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { EyeOff, Inbox, UserPlus, X } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Chip } from '@/components/app/StatusChip'
import { Identifier, Mono } from '@/components/app/identifiers'
import { RelativeTime } from '@/components/app/RelativeTime'
import { EmptyState, ErrorState, SkeletonRows } from '@/components/app/states'
import { describeError } from '@/api/errors'
import { pendingQuery, useDismissPending } from '@/api/queries/onboarding'
import type { PendingAP } from '@/gen/api.gen'
import { cn } from '@/lib/utils'
import { Panel } from './Panel'
import { pendingReasonMeta } from './reasons'

// Card 1 of the hub (design §3.15): unregistered IDs that said hello, and
// registered APs whose attempts are rejected. Two APs behind one NAT share
// the public address; LocalIP and serial tell them apart.

export function PendingCard({ siteId, highlight }: { siteId?: string; highlight?: string }) {
  const [showDismissed, setShowDismissed] = useState(false)
  const pending = useQuery(pendingQuery(false))
  const dismissed = useQuery({ ...pendingQuery(true), enabled: showDismissed })
  const dismiss = useDismissPending()

  const rows = pending.data ?? []
  const dismissedRows = showDismissed ? (dismissed.data ?? []).filter((row) => !rows.some((r) => r.id === row.id)) : []

  const onDismiss = (row: PendingAP) =>
    dismiss.mutate(row.id, {
      onSuccess: () => toast.success(`Dismissed ${row.id} for 30 days`),
      onError: (error) => toast.error(`Could not dismiss ${row.id}: ${describeError(error).message}`),
    })

  return (
    <Panel
      id="pending"
      title="Waiting to be adopted"
      badge={rows.length ? <Chip tone="info">{rows.length} waiting</Chip> : null}
      description="APs that reached this controller but are not served: new IDs, and registered APs whose attempts are refused."
    >
      {pending.isPending ? (
        <SkeletonRows rows={2} />
      ) : pending.isError ? (
        <ErrorState error={pending.error} title="Could not load the waiting APs" onRetry={() => void pending.refetch()} />
      ) : rows.length === 0 && dismissedRows.length === 0 ? (
        <EmptyState icon={<Inbox />} title="No AP is waiting.">
          An AP appears here once it has your controller in its cloud.ini and says hello. Use the CLI below to prepare one.
        </EmptyState>
      ) : (
        <PendingList rows={rows} dismissedRows={dismissedRows} siteId={siteId} highlight={highlight} onDismiss={onDismiss} busyId={dismiss.isPending ? dismiss.variables : undefined} />
      )}
      <div className="flex flex-wrap items-center gap-2">
        <Button variant="outline" size="sm" aria-pressed={showDismissed} onClick={() => setShowDismissed((value) => !value)}>
          <EyeOff /> {showDismissed ? 'Hide dismissed' : 'Show dismissed'}
        </Button>
        {showDismissed && dismissed.isSuccess && dismissedRows.length === 0 ? (
          <span className="text-sm text-muted-foreground">Nothing dismissed in the last 30 days.</span>
        ) : null}
      </div>
    </Panel>
  )
}

type ListProps = {
  rows: PendingAP[]
  dismissedRows: PendingAP[]
  siteId?: string
  highlight?: string
  busyId?: string
  onDismiss: (row: PendingAP) => void
}

function PendingList({ rows, dismissedRows, ...rest }: ListProps) {
  const all = [...rows.map((row) => ({ row, dismissed: false })), ...dismissedRows.map((row) => ({ row, dismissed: true }))]
  return (
    <>
      <ul className="grid gap-3 md:hidden" aria-label="Waiting APs">
        {all.map(({ row, dismissed }) => (
          <li key={row.id}>
            <PendingCardRow row={row} dismissed={dismissed} {...rest} />
          </li>
        ))}
      </ul>
      <div className="min-w-0 max-md:hidden">
        <table className="w-full table-fixed text-sm">
          <caption className="sr-only">APs waiting to be adopted</caption>
          <thead className="text-left text-xs text-muted-foreground">
            <tr className="border-b">
              <th scope="col" className="w-[28%] py-2 pr-3 font-medium">AP</th>
              <th scope="col" className="py-2 pr-3 font-medium">LAN address</th>
              <th scope="col" className="py-2 pr-3 font-medium max-lg:hidden">Public address</th>
              <th scope="col" className="py-2 pr-3 font-medium max-xl:hidden">Firmware</th>
              <th scope="col" className="py-2 pr-3 font-medium">Last seen</th>
              <th scope="col" className="py-2 pr-3 font-medium">Reason</th>
              <th scope="col" className="w-[12.5rem] py-2 font-medium">
                <span className="sr-only">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {all.map(({ row, dismissed }) => (
              <PendingTableRow key={row.id} row={row} dismissed={dismissed} {...rest} />
            ))}
          </tbody>
        </table>
      </div>
    </>
  )
}

type RowProps = Omit<ListProps, 'rows' | 'dismissedRows'> & { row: PendingAP; dismissed: boolean }

function Actions({ row, dismissed, siteId, busyId, onDismiss }: RowProps) {
  const meta = pendingReasonMeta[row.reason] ?? pendingReasonMeta.unregistered
  return (
    <div className="flex flex-wrap gap-2">
      {row.reason === 'unregistered' ? (
        <Button asChild size="sm">
          <Link to="/onboard/register" search={{ id: row.id, site: siteId }} aria-label={`Adopt ${row.id}`}>
            <UserPlus /> Adopt
          </Link>
        </Button>
      ) : (
        <Button asChild size="sm" variant="outline">
          {/* The stable AP link (design §2.3) redirects to the AP's site. */}
          <Link to={`/aps/${encodeURIComponent(row.id)}` as never} aria-label={`Open ${row.id} (${meta.label})`}>
            Open AP
          </Link>
        </Button>
      )}
      {dismissed ? null : (
        <Button
          size="sm"
          variant="ghost"
          aria-label={`Dismiss ${row.id}`}
          disabled={busyId === row.id}
          onClick={() => onDismiss(row)}
        >
          <X /> Dismiss
        </Button>
      )}
    </div>
  )
}

function Reason({ row, dismissed }: { row: PendingAP; dismissed: boolean }) {
  const meta = pendingReasonMeta[row.reason] ?? pendingReasonMeta.unregistered
  return (
    <span className="inline-flex flex-wrap items-center gap-1.5" title={meta.help}>
      <Chip tone={meta.tone}>{meta.label}</Chip>
      {dismissed ? <Chip tone="idle">Dismissed</Chip> : null}
    </span>
  )
}

function identity(row: PendingAP) {
  return [row.model || row.sku, row.serial ? `serial ${row.serial}` : ''].filter(Boolean).join(' · ') || 'Unknown model'
}

function PendingCardRow(props: RowProps) {
  const { row, dismissed, highlight } = props
  return (
    <article
      aria-label={row.id}
      className={cn('grid gap-3 rounded-lg border p-3', dismissed && 'opacity-80', highlight === row.id && 'ring-2 ring-ring/40')}
    >
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="grid min-w-0 gap-0.5">
          <Identifier value={row.id} />
          <span className="text-xs text-muted-foreground">{identity(row)}</span>
        </div>
        <Reason row={row} dismissed={dismissed} />
      </div>
      <dl className="grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
        <div className="grid min-w-0 gap-0.5">
          <dt className="text-xs text-muted-foreground">LAN address</dt>
          <dd>{row.local_ip ? <Mono>{row.local_ip}</Mono> : '–'}</dd>
        </div>
        <div className="grid min-w-0 gap-0.5">
          <dt className="text-xs text-muted-foreground">Public address</dt>
          <dd>{row.remote_address ? <Mono>{row.remote_address}</Mono> : '–'}</dd>
        </div>
        <div className="grid min-w-0 gap-0.5">
          <dt className="text-xs text-muted-foreground">Last seen</dt>
          <dd>
            <RelativeTime value={row.last_seen} />
          </dd>
        </div>
        <div className="grid min-w-0 gap-0.5">
          <dt className="text-xs text-muted-foreground">Attempts</dt>
          <dd className="tabular-nums">
            {row.attempts} since <RelativeTime value={row.first_seen} />
          </dd>
        </div>
        <div className="col-span-2 grid min-w-0 gap-0.5">
          <dt className="text-xs text-muted-foreground">Firmware</dt>
          <dd className="break-all">{row.firmware || '–'}</dd>
        </div>
      </dl>
      <Actions {...props} />
    </article>
  )
}

function PendingTableRow(props: RowProps) {
  const { row, dismissed, highlight } = props
  return (
    <tr className={cn('border-b align-top last:border-0', dismissed && 'opacity-80', highlight === row.id && 'bg-accent/60')}>
      <td className="py-3 pr-3">
        <div className="grid min-w-0 gap-0.5">
          <Identifier value={row.id} />
          <span className="text-xs text-muted-foreground">{identity(row)}</span>
        </div>
      </td>
      <td className="py-3 pr-3">{row.local_ip ? <Mono>{row.local_ip}</Mono> : '–'}</td>
      <td className="py-3 pr-3 max-lg:hidden">{row.remote_address ? <Mono>{row.remote_address}</Mono> : '–'}</td>
      <td className="py-3 pr-3 break-all max-xl:hidden">{row.firmware || '–'}</td>
      <td className="py-3 pr-3">
        <RelativeTime value={row.last_seen} />
        <div className="text-xs text-muted-foreground tabular-nums">{row.attempts} attempts</div>
      </td>
      <td className="py-3 pr-3">
        <Reason row={row} dismissed={dismissed} />
      </td>
      <td className="py-3">
        <Actions {...props} />
      </td>
    </tr>
  )
}
