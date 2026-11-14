import { useEffect, useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { Ellipsis, LocateFixed, Power, RefreshCw, RotateCw, Settings, Trash2, ArrowRightLeft } from 'lucide-react'
import { toast } from 'sonner'
import type { APDetail } from '@/gen/api.gen'
import { useAPAction, useDeleteAP, useMoveAP } from '@/api/queries/aps'
import { describeError, isApiError } from '@/api/errors'
import { Button, buttonVariants } from '@/components/ui/button'
import { Sheet, SheetContent } from '@/components/ui/sheet'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { CopyButton } from '@/components/app/identifiers'
import { sitesQuery } from '@/features/shell/queries'
import { parseTime } from '@/lib/time'
import { cn } from '@/lib/utils'
import { ConfirmDialog, ResponsiveDialog } from '@/features/overview/kit/ConfirmDialog'
import { EXPERIMENTAL_HELP, ExperimentalBadge } from '@/features/overview/kit/ExperimentalBadge'
import { NativeSelect } from '@/features/overview/kit/FilterBar'

// AP actions (design §3.5 "Actions", §5.6). Locate (LED.Locator through the
// config push) and the ap-cmd actions (reboot, restart radios) are
// experimental: the controls show a flask badge and only act when the site
// has experimental options on; reboot and restart need the AP's verified
// `ap_cmd` capability, otherwise the reboot dialog gives the LAN-side
// `dismist ap33 ssh … reboot` command instead. Every disruptive action is
// confirmed in an AlertDialog that names the AP and the impact.

type Dialog = null | 'locate' | 'reboot' | 'restart' | 'push' | 'move' | 'delete'

export function useCountdown(until: string | null | undefined): number | null {
  const end = parseTime(until)?.getTime() ?? null
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (end === null || end <= Date.now()) return
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [end])
  if (end === null) return null
  const left = Math.round((end - now) / 1000)
  return left > 0 ? left : null
}

export function mmss(seconds: number): string {
  const m = Math.floor(seconds / 60)
  const s = seconds % 60
  return `${m}:${String(s).padStart(2, '0')}`
}

function actionError(error: unknown): string {
  if (isApiError(error) && error.code === 'unverified_command') {
    return 'This AP’s firmware is not verified for remote commands yet. Use the LAN-side command instead.'
  }
  if (isApiError(error) && error.code === 'experimental_disabled') {
    return 'Experimental options are off for this site. Turn them on in Site settings › Advanced first.'
  }
  return describeError(error).message
}

export function APActions({ ap, siteId, experimental }: { ap: APDetail; siteId: string; experimental: boolean }) {
  const [dialog, setDialog] = useState<Dialog>(null)
  const [sheet, setSheet] = useState(false)
  const locateLeft = useCountdown(ap.locate_until)
  const action = useAPAction(ap.id)
  const canCommand = ap.capabilities.ap_cmd && ap.connected

  const run = (name: Parameters<typeof action.mutate>[0], success: string) =>
    action.mutate(name, {
      onSuccess: () => {
        toast.success(success)
        setDialog(null)
      },
      onError: (error) => toast.error('The action failed', { description: actionError(error) }),
    })

  const open = (next: Dialog) => {
    setSheet(false)
    action.reset()
    setDialog(next)
  }

  const locateButton =
    locateLeft !== null ? (
      <Button variant="outline" onClick={() => run({ action: 'unlocate' }, 'Locate stopped')} disabled={action.isPending}>
        <LocateFixed className="text-brand" />
        Stop locating ({mmss(locateLeft)})
      </Button>
    ) : (
      <Button variant="outline" onClick={() => open('locate')} disabled={!ap.connected} title={!ap.connected ? 'The AP is not connected' : EXPERIMENTAL_HELP}>
        <LocateFixed />
        Locate
      </Button>
    )

  const menuItems: { id: Dialog; label: string; icon: typeof Power; experimental?: boolean; destructive?: boolean; hidden?: boolean }[] = [
    { id: 'push', label: 'Re-send config', icon: RefreshCw },
    { id: 'reboot', label: 'Reboot', icon: Power, experimental: true },
    { id: 'restart', label: 'Restart radios', icon: RotateCw, experimental: true, hidden: !canCommand },
    { id: 'move', label: 'Move to site', icon: ArrowRightLeft },
    { id: 'delete', label: 'Delete', icon: Trash2, destructive: true },
  ]

  return (
    <>
      {/* md and up: primary buttons, the rest in a ⋯ menu. */}
      <div className="hidden flex-wrap items-center gap-2 md:flex">
        {locateButton}
        <Link to={'/sites/$siteId/aps/$apId/settings' as never} params={{ siteId, apId: ap.id } as never} className={buttonVariants({ variant: 'outline' })}>
          <Settings />
          Settings
        </Link>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="outline" size="icon" aria-label="More actions">
              <Ellipsis />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="min-w-52">
            {menuItems
              .filter((item) => !item.hidden)
              .map((item, index) => (
                <div key={item.id}>
                  {item.destructive && index > 0 ? <DropdownMenuSeparator /> : null}
                  <DropdownMenuItem onSelect={() => open(item.id)} className={cn(item.destructive && 'text-destructive')}>
                    <item.icon />
                    {item.label}
                    {item.experimental ? <ExperimentalBadge className="ml-auto" label="Exp." /> : null}
                  </DropdownMenuItem>
                </div>
              ))}
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
      {/* Phones: one ⋯ sheet with every action. */}
      <div className="md:hidden">
        <Button variant="outline" onClick={() => setSheet(true)} aria-haspopup="dialog">
          <Ellipsis />
          Actions
        </Button>
        <Sheet open={sheet} onOpenChange={setSheet}>
          <SheetContent title={`Actions for ${ap.name}`}>
            <ul className="grid gap-0.5">
              <li>
                {locateLeft !== null ? (
                  <SheetRow icon={LocateFixed} label={`Stop locating (${mmss(locateLeft)})`} onClick={() => run({ action: 'unlocate' }, 'Locate stopped')} />
                ) : (
                  <SheetRow icon={LocateFixed} label="Locate" experimental disabled={!ap.connected} onClick={() => open('locate')} />
                )}
              </li>
              {menuItems
                .filter((item) => !item.hidden)
                .map((item) => (
                  <li key={item.id}>
                    <SheetRow icon={item.icon} label={item.label} experimental={item.experimental} destructive={item.destructive} onClick={() => open(item.id)} />
                  </li>
                ))}
              <li>
                <Link
                  to={'/sites/$siteId/aps/$apId/settings' as never}
                  params={{ siteId, apId: ap.id } as never}
                  className="flex min-h-12 items-center gap-3 rounded-lg px-3 text-base hover:bg-accent"
                  onClick={() => setSheet(false)}
                >
                  <Settings aria-hidden className="size-5 text-muted-foreground" />
                  Settings
                </Link>
              </li>
            </ul>
          </SheetContent>
        </Sheet>
      </div>

      <LocateDialog
        open={dialog === 'locate'}
        onOpenChange={(o) => !o && setDialog(null)}
        ap={ap}
        experimental={experimental}
        pending={action.isPending}
        error={action.error ? actionError(action.error) : null}
        onLocate={(minutes) => run({ action: 'locate', body: { minutes } }, `Locating ${ap.name} for ${minutes} min`)}
      />
      <RebootDialog
        open={dialog === 'reboot'}
        onOpenChange={(o) => !o && setDialog(null)}
        ap={ap}
        canCommand={canCommand}
        experimental={experimental}
        pending={action.isPending}
        error={action.error ? actionError(action.error) : null}
        onReboot={() => run({ action: 'reboot' }, `${ap.name} is rebooting`)}
      />
      <RestartRadiosDialog
        open={dialog === 'restart'}
        onOpenChange={(o) => !o && setDialog(null)}
        ap={ap}
        experimental={experimental}
        pending={action.isPending}
        error={action.error ? actionError(action.error) : null}
        onRestart={(band) => run({ action: 'restart-radios', body: { band } }, `Restarting ${band === 'both' ? 'both radios' : `the ${band} GHz radio`} on ${ap.name}`)}
      />
      <ConfirmDialog
        open={dialog === 'push'}
        onOpenChange={(o) => !o && setDialog(null)}
        title={`Re-send config to ${ap.name}?`}
        description={
          <>
            <p>dismist asks the AP to fetch rev {ap.config_revision} again. If it does not fetch within 15 s, dismist reconnects it.</p>
            <p>Losing the controller session does not drop Wi-Fi clients; the AP keeps serving with its current config.</p>
          </>
        }
        confirmLabel="Re-send config"
        pending={action.isPending}
        error={action.error ? actionError(action.error) : null}
        onConfirm={() => run({ action: 'push-config' }, `Config sent to ${ap.name}`)}
      />
      <MoveDialog open={dialog === 'move'} onOpenChange={(o) => !o && setDialog(null)} ap={ap} siteId={siteId} />
      <DeleteDialog open={dialog === 'delete'} onOpenChange={(o) => !o && setDialog(null)} ap={ap} siteId={siteId} />
    </>
  )
}

function SheetRow({
  icon: Icon,
  label,
  onClick,
  experimental,
  destructive,
  disabled,
}: {
  icon: typeof Power
  label: string
  onClick: () => void
  experimental?: boolean
  destructive?: boolean
  disabled?: boolean
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      className={cn('flex min-h-12 w-full items-center gap-3 rounded-lg px-3 text-left text-base hover:bg-accent disabled:opacity-50', destructive && 'text-destructive')}
    >
      <Icon aria-hidden className={cn('size-5', destructive ? 'text-destructive' : 'text-muted-foreground')} />
      <span className="flex-1">{label}</span>
      {experimental ? <ExperimentalBadge /> : null}
    </button>
  )
}

function ExperimentalNote({ experimental, siteId }: { experimental: boolean; siteId: string }) {
  return (
    <div className="grid gap-1 rounded-md border border-status-warn/45 bg-status-warn/10 px-3 py-2 text-sm">
      <span className="flex items-center gap-2 font-medium">
        <ExperimentalBadge /> {EXPERIMENTAL_HELP}
      </span>
      {!experimental ? (
        <span>
          Experimental options are off for this site.{' '}
          <Link to={'/sites/$siteId/settings/advanced' as never} params={{ siteId } as never} className="text-brand underline-offset-4 hover:underline">
            Turn them on in Site settings › Advanced
          </Link>{' '}
          to use this.
        </span>
      ) : null}
    </div>
  )
}

function LocateDialog({
  open,
  onOpenChange,
  ap,
  experimental,
  pending,
  error,
  onLocate,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  ap: APDetail
  experimental: boolean
  pending: boolean
  error: string | null
  onLocate: (minutes: number) => void
}) {
  const [minutes, setMinutes] = useState('5')
  return (
    <ResponsiveDialog
      open={open}
      onOpenChange={onOpenChange}
      title={`Locate ${ap.name}`}
      description="Flashes the AP’s LED in the locate pattern, then turns it back off. This is a config push (LED.Locator); Wi-Fi clients are not affected."
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={pending}>
            Cancel
          </Button>
          <Button onClick={() => onLocate(Number(minutes))} disabled={pending || !experimental}>
            {pending ? 'Starting…' : `Locate for ${minutes} min`}
          </Button>
        </>
      }
    >
      <ExperimentalNote experimental={experimental} siteId={ap.site_id} />
      <NativeSelect
        label="For how long"
        value={minutes}
        allLabel={null}
        options={['1', '5', '15', '60'].map((m) => ({ value: m, label: `${m} min` }))}
        onChange={setMinutes}
      />
      {error ? (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      ) : null}
    </ResponsiveDialog>
  )
}

function RebootDialog({
  open,
  onOpenChange,
  ap,
  canCommand,
  experimental,
  pending,
  error,
  onReboot,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  ap: APDetail
  canCommand: boolean
  experimental: boolean
  pending: boolean
  error: string | null
  onReboot: () => void
}) {
  const command = `dismist ap33 ssh root@${ap.lan_ip || '<ap-address>'} reboot`
  const impact = ap.client_count === 1 ? '1 client' : `${ap.client_count} clients`
  if (!canCommand) {
    return (
      <ResponsiveDialog
        open={open}
        onOpenChange={onOpenChange}
        title={`Reboot ${ap.name}`}
        description="Remote reboot is not verified for this AP’s firmware yet, so dismist does not send it. Reboot it from a computer on the AP’s LAN instead:"
        footer={<Button onClick={() => onOpenChange(false)}>Done</Button>}
      >
        <div className="flex min-w-0 items-center gap-1 rounded-md border bg-muted/50 py-1 pr-1 pl-3">
          <code className="min-w-0 flex-1 font-mono text-sm break-all">{command}</code>
          <CopyButton value={command} label="Copy command" />
        </div>
        <p className="text-sm text-muted-foreground">
          {ap.connected ? `${impact} will be disconnected while the AP restarts (about 2 minutes).` : 'The AP is not connected to dismist right now.'}{' '}
          The command uses your SSH key, which the site’s SSH settings put on the AP.
        </p>
        <ExperimentalBadge className="justify-self-start" />
      </ResponsiveDialog>
    )
  }
  return (
    <ConfirmDialog
      open={open}
      onOpenChange={onOpenChange}
      title={`Reboot ${ap.name}?`}
      description={
        <>
          <p>{impact} will be disconnected while the AP restarts (about 2 minutes). The AP keeps its config.</p>
          <ExperimentalNote experimental={experimental} siteId={ap.site_id} />
        </>
      }
      confirmLabel="Reboot"
      destructive
      pending={pending}
      error={error}
      confirmDisabled={!experimental}
      onConfirm={onReboot}
    />
  )
}

function RestartRadiosDialog({
  open,
  onOpenChange,
  ap,
  experimental,
  pending,
  error,
  onRestart,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  ap: APDetail
  experimental: boolean
  pending: boolean
  error: string | null
  onRestart: (band: '2.4' | '5' | 'both') => void
}) {
  const [band, setBand] = useState<'2.4' | '5' | 'both'>('both')
  const clients = useMemo(() => {
    const on = (b: string) => ap.radios.find((r) => r.band === b)?.clients ?? 0
    return band === 'both' ? on('2.4') + on('5') : on(band)
  }, [ap.radios, band])
  return (
    <ConfirmDialog
      open={open}
      onOpenChange={onOpenChange}
      title={`Restart radios on ${ap.name}?`}
      description={
        <>
          <p>
            {clients === 1 ? '1 client' : `${clients} clients`} on {band === 'both' ? 'both bands' : `${band} GHz`} will reconnect.
          </p>
          <ExperimentalNote experimental={experimental} siteId={ap.site_id} />
        </>
      }
      confirmLabel="Restart radios"
      destructive
      pending={pending}
      error={error}
      confirmDisabled={!experimental}
      onConfirm={() => onRestart(band)}
    >
      <NativeSelect
        label="Radios"
        value={band}
        allLabel={null}
        options={[
          { value: 'both', label: 'Both bands' },
          { value: '2.4', label: '2.4 GHz' },
          { value: '5', label: '5 GHz' },
        ]}
        onChange={(v) => setBand(v as typeof band)}
      />
    </ConfirmDialog>
  )
}

function MoveDialog({ open, onOpenChange, ap, siteId }: { open: boolean; onOpenChange: (open: boolean) => void; ap: APDetail; siteId: string }) {
  const sites = useQuery(sitesQuery)
  const others = (sites.data ?? []).filter((s) => s.id !== siteId)
  const [target, setTarget] = useState('')
  const move = useMoveAP(ap.id)
  const preview = useMoveAP(ap.id)
  const navigate = useNavigate()
  const targetName = others.find((s) => s.id === target)?.name ?? ''
  useEffect(() => {
    if (!open) {
      setTarget('')
      preview.reset()
      move.reset()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])
  useEffect(() => {
    if (open && target) preview.mutate({ siteId: target, rev: ap.updated_rev, dryRun: true })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, target])
  const summary = preview.data?.impact?.summary
  return (
    <ConfirmDialog
      open={open}
      onOpenChange={onOpenChange}
      title={`Move ${ap.name} to another site?`}
      description={
        <>
          <p>The new site’s WLANs and radio settings replace the current ones on this AP. Clients on WLANs the new site does not have are disconnected.</p>
          {target ? (
            <p className="text-foreground">{preview.isPending ? 'Checking the impact…' : summary ? summary : `${ap.name} will take ${targetName}’s config.`}</p>
          ) : null}
        </>
      }
      confirmLabel={targetName ? `Move to ${targetName}` : 'Move'}
      destructive
      pending={move.isPending}
      error={move.error ? describeError(move.error).message : null}
      onConfirm={() =>
        target &&
        move.mutate(
          { siteId: target, rev: ap.updated_rev },
          {
            onSuccess: () => {
              toast.success(`${ap.name} moved to ${targetName}`)
              onOpenChange(false)
              void navigate({ to: '/sites/$siteId/aps/$apId', params: { siteId: target, apId: ap.id }, replace: true })
            },
          },
        )
      }
    >
      {others.length ? (
        <NativeSelect label="Target site" value={target} allLabel="Choose a site" options={others.map((s) => ({ value: s.id, label: s.name }))} onChange={setTarget} />
      ) : (
        <p className="text-sm">There is no other site. Create one first.</p>
      )}
    </ConfirmDialog>
  )
}

function DeleteDialog({ open, onOpenChange, ap, siteId }: { open: boolean; onOpenChange: (open: boolean) => void; ap: APDetail; siteId: string }) {
  const [purge, setPurge] = useState(false)
  const remove = useDeleteAP(ap.id)
  const navigate = useNavigate()
  useEffect(() => {
    if (!open) {
      setPurge(false)
      remove.reset()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])
  return (
    <ConfirmDialog
      open={open}
      onOpenChange={onOpenChange}
      title={`Delete ${ap.name}?`}
      description={
        <>
          <p>dismist closes the AP’s session and deletes its record and secret. The AP keeps broadcasting its last config until it reboots, then keeps trying to connect and shows up as pending.</p>
          {ap.client_count ? <p>{ap.client_count === 1 ? '1 client is' : `${ap.client_count} clients are`} connected to it now.</p> : null}
        </>
      }
      confirmLabel="Delete AP"
      destructive
      typeToConfirm={ap.name}
      pending={remove.isPending}
      error={remove.error ? describeError(remove.error).message : null}
      onConfirm={() =>
        remove.mutate(
          { rev: ap.updated_rev, purge },
          {
            onSuccess: () => {
              toast.success(`${ap.name} deleted`)
              onOpenChange(false)
              void navigate({ to: '/sites/$siteId/aps', params: { siteId }, replace: true })
            },
          },
        )
      }
    >
      <label className="flex min-h-11 items-center gap-3 text-sm">
        <input type="checkbox" className="size-5 accent-[var(--color-destructive)]" checked={purge} onChange={(e) => setPurge(e.target.checked)} />
        Also delete its history (samples, sessions and events)
      </label>
    </ConfirmDialog>
  )
}
