import { useState } from 'react'
import { Link, useNavigate } from '@tanstack/react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { ChevronDown, Code2, Copy, EllipsisVertical, Pencil, Plus, Power, QrCode, Trash2, Wifi } from 'lucide-react'
import { keys } from '@/api/keys'
import { deleteNetwork, networksQuery, patchNetwork, type WLANView } from '@/api/queries/networks'
import { siteQuery } from '@/api/queries/sites'
import { PageHeader } from '@/components/app/PageHeader'
import { EmptyState, ErrorState, SkeletonRows } from '@/components/app/states'
import { Chip } from '@/components/app/StatusChip'
import { buttonVariants } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { ExperimentalBadge, ShadowedBadge } from '@/features/settings/kit/badges'
import { ConfirmDialog } from '@/features/settings/kit/primitives'
import { LegacyModeBanner, useTypedEditing } from '@/features/settings/kit/site'
import { cn } from '@/lib/utils'
import { securityLabel, slotsPerBand, WLANS_PER_BAND } from './security'
import { WifiQRDialog } from './WifiQR'

// The WLAN list (design §3.8): a table at md and up, cards below; the whole
// row is a link; row actions live in a trailing ⋯ menu.

type Pending = { kind: 'toggle' | 'delete'; view: WLANView } | null

export function WlanList({ siteId, dialog, wlan }: { siteId: string; dialog?: string; wlan?: string }) {
  const networks = useQuery(networksQuery(siteId))
  const site = useQuery(siteQuery(siteId))
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const { typed } = useTypedEditing()
  const [pending, setPending] = useState<Pending>(null)
  const [working, setWorking] = useState(false)
  const base = `/sites/${encodeURIComponent(siteId)}/wlans`
  const qrView = dialog === 'qr' ? networks.data?.find((n) => n.id === wlan) : undefined

  async function setEnabled(view: WLANView, enabled: boolean, undo = true) {
    const key = keys.siteNetworks(siteId)
    const previous = queryClient.getQueryData<WLANView[]>(key)
    // Optimistic (§7.5: only WLAN enable/disable), with rollback.
    queryClient.setQueryData<WLANView[]>(key, (list) => list?.map((n) => (n.id === view.id ? { ...n, enabled } : n)))
    try {
      const response = await patchNetwork(siteId, view.id, { enabled }, { ifMatch: view.updated_rev })
      queryClient.setQueryData(keys.network(view.id), response.object)
      void queryClient.invalidateQueries({ queryKey: key })
      void queryClient.invalidateQueries({ queryKey: keys.siteChanges(siteId) })
      toast.success(`${view.ssid} ${enabled ? 'enabled' : 'disabled'}`, {
        description: response.impact?.summary || undefined,
        duration: 10_000,
        action: undo ? { label: 'Undo', onClick: () => void setEnabled({ ...view, enabled, updated_rev: response.object.updated_rev }, !enabled, false) } : undefined,
      })
    } catch (error) {
      queryClient.setQueryData(key, previous)
      toast.error(`${view.ssid} was not ${enabled ? 'enabled' : 'disabled'}`, { description: error instanceof Error ? error.message : undefined })
    }
  }

  async function remove(view: WLANView) {
    setWorking(true)
    try {
      await deleteNetwork(siteId, view.id, { ifMatch: view.updated_rev })
      toast.success(`WLAN ${view.ssid} deleted`)
      void queryClient.invalidateQueries({ queryKey: keys.siteNetworks(siteId) })
      void queryClient.invalidateQueries({ queryKey: keys.siteChanges(siteId) })
      setPending(null)
    } catch (error) {
      toast.error('Not deleted', { description: error instanceof Error ? error.message : undefined })
    } finally {
      setWorking(false)
    }
  }

  const list = networks.data ?? []
  const slots = slotsPerBand(list)
  const used = Math.max(slots['2.4'], slots['5'])

  return (
    <div className="grid gap-5">
      <PageHeader
        title="WLANs"
        meta={networks.data ? `${used} of ${WLANS_PER_BAND} WLANs per band in use` : undefined}
        actions={<NewWlanMenu base={base} disabled={!typed} />}
      />
      <LegacyModeBanner />
      {networks.isError ? (
        <ErrorState error={networks.error} title="Could not load the WLANs" onRetry={() => void networks.refetch()} />
      ) : !networks.data ? (
        <SkeletonRows rows={2} />
      ) : !list.length ? (
        <EmptyState title="No WLANs yet. Create one" icon={<Wifi />} action={<NewWlanMenu base={base} disabled={!typed} />}>
          Each WLAN is broadcast by every AP in the site.
        </EmptyState>
      ) : (
        <>
          <p className="text-sm text-muted-foreground md:hidden">
            {used} of {WLANS_PER_BAND} WLANs per band in use
          </p>
          <WlanTable
            list={list}
            base={base}
            experimental={Boolean(site.data?.experimental)}
            onToggle={(view) => setPending({ kind: 'toggle', view })}
            onDelete={(view) => setPending({ kind: 'delete', view })}
            onQR={(view) => void navigate({ to: base as never, search: { dialog: 'qr', wlan: view.id } as never })}
            onDuplicate={(view) => void navigate({ to: `${base}/new` as never, search: { from: view.id } as never })}
          />
        </>
      )}
      <ConfirmDialog
        open={pending?.kind === 'toggle'}
        onOpenChange={(open) => !open && setPending(null)}
        title={pending?.view.enabled ? `Disable ${pending.view.ssid}?` : `Enable ${pending?.view.ssid ?? ''}?`}
        description={
          pending?.view.enabled
            ? `${pending.view.live.clients} client${pending.view.live.clients === 1 ? '' : 's'} will be disconnected from ${pending.view.live.aps_broadcasting} AP${pending.view.live.aps_broadcasting === 1 ? '' : 's'}.`
            : `Every AP in the site starts broadcasting ${pending?.view.ssid ?? ''}.`
        }
        action={pending?.view.enabled ? 'Disable' : 'Enable'}
        destructive={pending?.view.enabled}
        onConfirm={() => {
          if (!pending) return
          const view = pending.view
          setPending(null)
          void setEnabled(view, !view.enabled)
        }}
      />
      <ConfirmDialog
        open={pending?.kind === 'delete'}
        onOpenChange={(open) => !open && setPending(null)}
        title={`Delete ${pending?.view.ssid ?? ''}?`}
        description={`${pending?.view.live.clients ?? 0} client(s) will be disconnected. This cannot be undone, but the change is in Changes and can be restored.`}
        action="Delete WLAN"
        destructive
        pending={working}
        onConfirm={() => pending && void remove(pending.view)}
      />
      {qrView ? (
        <WifiQRDialog
          siteId={siteId}
          view={qrView}
          open
          onOpenChange={(open) => !open && void navigate({ to: base as never, search: {} as never, replace: true })}
        />
      ) : null}
    </div>
  )
}

function NewWlanMenu({ base, disabled }: { base: string; disabled?: boolean }) {
  return (
    <div className="flex">
      <Link to={`${base}/new` as never} search={{ preset: 'home' } as never} className={cn(buttonVariants(), 'rounded-r-none', disabled && 'pointer-events-none opacity-50')} aria-disabled={disabled || undefined}>
        <Plus /> New WLAN
      </Link>
      <DropdownMenu>
        <DropdownMenuTrigger className={cn(buttonVariants({ size: 'icon' }), 'rounded-l-none border-l border-primary-foreground/20')} aria-label="New WLAN from a preset" disabled={disabled}>
          <ChevronDown />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          {(
            [
              ['home', 'Home', 'WPA2/WPA3, both bands'],
              ['guest', 'Guest', 'Isolated from each other and the LAN'],
              ['iot', 'IoT', 'WPA2, 2.4 GHz only, no band steering'],
            ] as const
          ).map(([preset, label, help]) => (
            <DropdownMenuItem key={preset} asChild>
              <Link to={`${base}/new` as never} search={{ preset } as never} className="grid gap-0.5 py-1.5">
                <span className="font-medium">{label}</span>
                <span className="text-xs text-muted-foreground">{help}</span>
              </Link>
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}

type TableProps = {
  list: WLANView[]
  base: string
  experimental: boolean
  onToggle: (view: WLANView) => void
  onDelete: (view: WLANView) => void
  onQR: (view: WLANView) => void
  onDuplicate: (view: WLANView) => void
}

function vlanLabel(view: WLANView) {
  return view.vlan_id > 1 ? `VLAN ${view.vlan_id}` : 'Untagged'
}

function StateCell({ view }: { view: WLANView }) {
  return (
    <span className="flex flex-wrap items-center gap-1.5">
      <Chip tone={view.enabled ? 'good' : 'idle'}>{view.enabled ? 'Enabled' : 'Disabled'}</Chip>
      {view.experimental_in_use?.length ? <ExperimentalBadge /> : null}
      {view.expert?.has_patch && view.expert.shadowed?.length ? <ShadowedBadge /> : null}
    </span>
  )
}

function Broadcasting({ view }: { view: WLANView }) {
  const short = view.enabled && view.live.aps_broadcasting < view.live.aps_total
  return (
    <span className={cn('tabular-nums', short && 'font-medium text-warning')}>
      {view.live.aps_broadcasting}/{view.live.aps_total} APs
    </span>
  )
}

function RowMenu({ view, base, ...actions }: { view: WLANView; base: string } & Omit<TableProps, 'list' | 'base' | 'experimental'>) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger className={cn(buttonVariants({ variant: 'ghost', size: 'icon-sm' }), 'relative z-10')} aria-label={`Actions for ${view.ssid}`}>
        <EllipsisVertical />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem asChild>
          <Link to={`${base}/${encodeURIComponent(view.id)}` as never}>
            <Pencil /> Edit
          </Link>
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => actions.onToggle(view)}>
          <Power /> {view.enabled ? 'Disable' : 'Enable'}
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => actions.onDuplicate(view)}>
          <Copy /> Duplicate
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => actions.onQR(view)}>
          <QrCode /> Wi-Fi QR
        </DropdownMenuItem>
        <DropdownMenuItem asChild>
          <Link to={`${base}/${encodeURIComponent(view.id)}/expert` as never}>
            <Code2 /> Expert patch
          </Link>
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => actions.onDelete(view)} className="text-destructive">
          <Trash2 /> Delete
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function WlanTable({ list, base, ...actions }: TableProps) {
  return (
    <>
      {/* Cards below md (§7.7 rule 5): the whole card is a link (stretched). */}
      <ul className="grid gap-3 md:hidden" aria-label="WLANs">
        {list.map((view) => (
          <li key={view.id} className="relative grid gap-2 rounded-xl border bg-card p-4">
            <div className="flex items-start justify-between gap-2">
              <Link to={`${base}/${encodeURIComponent(view.id)}` as never} className="min-w-0 text-base font-semibold break-words after:absolute after:inset-0 after:content-['']">
                {view.ssid}
              </Link>
              <RowMenu view={view} base={base} {...actions} />
            </div>
            <p className="text-sm text-muted-foreground">
              {securityLabel(view.security)} · {view.bands.map((b) => `${b} GHz`).join(' + ')} · {vlanLabel(view)}
            </p>
            <div className="flex flex-wrap items-center justify-between gap-2 text-sm">
              <StateCell view={view} />
              <span className="text-muted-foreground">
                {view.live.clients} client{view.live.clients === 1 ? '' : 's'} · <Broadcasting view={view} />
              </span>
            </div>
          </li>
        ))}
      </ul>
      <div className="hidden overflow-hidden rounded-xl border bg-card md:block">
        <table className="w-full text-sm">
          <thead className="border-b bg-muted/50 text-left text-xs text-muted-foreground">
            <tr>
              <th scope="col" className="px-4 py-2.5 font-medium">SSID</th>
              <th scope="col" className="px-4 py-2.5 font-medium">Security</th>
              <th scope="col" className="hidden px-4 py-2.5 font-medium lg:table-cell">Bands</th>
              <th scope="col" className="hidden px-4 py-2.5 font-medium lg:table-cell">VLAN</th>
              <th scope="col" className="px-4 py-2.5 text-right font-medium">Clients</th>
              <th scope="col" className="hidden px-4 py-2.5 font-medium xl:table-cell">Broadcasting</th>
              <th scope="col" className="px-4 py-2.5 font-medium">State</th>
              <th scope="col" className="w-12 px-2 py-2.5"><span className="sr-only">Actions</span></th>
            </tr>
          </thead>
          <tbody className="divide-y">
            {list.map((view) => (
              <tr key={view.id} className="relative hover:bg-accent/40">
                <td className="px-4 py-3 font-medium">
                  <Link to={`${base}/${encodeURIComponent(view.id)}` as never} className="break-words after:absolute after:inset-0 after:content-['']">
                    {view.ssid}
                  </Link>
                </td>
                <td className="px-4 py-3">{securityLabel(view.security)}</td>
                <td className="hidden px-4 py-3 lg:table-cell">{view.bands.map((b) => `${b} GHz`).join(', ')}</td>
                <td className="hidden px-4 py-3 lg:table-cell">{vlanLabel(view)}</td>
                <td className="px-4 py-3 text-right tabular-nums">{view.live.clients}</td>
                <td className="hidden px-4 py-3 xl:table-cell">
                  <Broadcasting view={view} />
                </td>
                <td className="px-4 py-3">
                  <StateCell view={view} />
                </td>
                <td className="px-2 py-2 text-right">
                  <RowMenu view={view} base={base} {...actions} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  )
}
