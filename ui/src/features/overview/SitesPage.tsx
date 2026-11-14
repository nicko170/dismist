import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Plus } from 'lucide-react'
import type { SiteSummary } from '@/gen/api.gen'
import { Button } from '@/components/ui/button'
import { Chip } from '@/components/app/StatusChip'
import { PageHeader } from '@/components/app/PageHeader'
import { EmptyState, ErrorState, SkeletonRows } from '@/components/app/states'
import { sitesQuery } from '@/features/shell/queries'
import { ResponsiveList, type Column } from './kit/ResponsiveList'
import { NewSiteDialog } from './NewSiteDialog'

// Sites (design §3.2): every site with its health, and the New site dialog
// at ?dialog=new-site (so Back closes it).

function onlineTone(site: SiteSummary): 'good' | 'crit' | 'idle' {
  if (!site.counts.aps) return 'idle'
  return site.counts.aps_online < site.counts.aps ? 'crit' : 'good'
}

function Health({ site }: { site: SiteSummary }) {
  return (
    <Chip tone={onlineTone(site)}>
      {site.counts.aps ? `${site.counts.aps_online}/${site.counts.aps} APs online` : 'No APs'}
    </Chip>
  )
}

function PendingChip({ site }: { site: SiteSummary }) {
  if (!site.counts.pending_aps) return null
  return <Chip tone="warn">{site.counts.pending_aps} pending</Chip>
}

export function SitesPage({ dialog, onDialog }: { dialog: boolean; onDialog: (open: boolean) => void }) {
  const sites = useQuery(sitesQuery)
  const rows = sites.data ?? []
  const columns: Column<SiteSummary>[] = [
    { id: 'name', header: 'Name', primary: true, render: (s) => <span className="text-xs text-muted-foreground">{[s.country, s.timezone].filter(Boolean).join(' · ')}</span>, className: 'w-[34%]' },
    {
      id: 'health',
      header: 'Access points',
      render: (s) => (
        <span className="flex flex-wrap gap-1.5">
          <Health site={s} />
          <PendingChip site={s} />
        </span>
      ),
    },
    { id: 'clients', header: 'Clients', align: 'end', render: (s) => s.counts.clients },
    { id: 'wlans', header: 'WLANs', align: 'end', render: (s) => s.counts.wlans },
  ]
  return (
    <div className="grid min-w-0 gap-5">
      <PageHeader
        title="Sites"
        meta="Each site has its own WLANs, radio settings and access points."
        actions={
          <Button variant="brand" onClick={() => onDialog(true)}>
            <Plus />
            New site
          </Button>
        }
      />
      {sites.isPending ? (
        <SkeletonRows rows={3} />
      ) : sites.isError ? (
        <ErrorState error={sites.error} title="Could not load sites" onRetry={() => void sites.refetch()} />
      ) : rows.length === 0 ? (
        <EmptyState
          title="No sites yet"
          action={
            <Button variant="brand" onClick={() => onDialog(true)}>
              Create your first site
            </Button>
          }
        >
          Create your first site, then add an AP.
        </EmptyState>
      ) : (
        <ResponsiveList
          caption="Sites"
          rows={rows}
          rowKey={(s) => s.id}
          columns={columns}
          rowLink={(s) => ({ to: '/sites/$siteId/overview', params: { siteId: s.id }, label: s.name })}
          renderCard={(s, title) => (
            <div className="grid min-w-0 gap-1.5">
              <div className="flex min-w-0 items-center justify-between gap-2">
                <span className="min-w-0 truncate">{title}</span>
                <Health site={s} />
              </div>
              <p className="text-sm text-muted-foreground tabular-nums">
                {s.counts.clients} clients · {s.counts.wlans} {s.counts.wlans === 1 ? 'WLAN' : 'WLANs'}
              </p>
              <p className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                {[s.country, s.timezone].filter(Boolean).join(' · ')}
                <PendingChip site={s} />
              </p>
            </div>
          )}
        />
      )}
      <p className="text-xs text-muted-foreground">
        Looking for an AP that is not in a site yet?{' '}
        <Link to={'/onboard' as never} className="text-brand underline-offset-4 hover:underline">
          Add an AP
        </Link>
      </p>
      <NewSiteDialog open={dialog} onOpenChange={onDialog} existing={rows.map((s) => s.name)} />
    </div>
  )
}
