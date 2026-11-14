import type { APDetail } from '@/gen/api.gen'
import { TestedFirmware } from '@/gen/api.gen'
import { Chip } from '@/components/app/StatusChip'
import { Identifier } from '@/components/app/identifiers'
import { KV } from '@/components/app/KV'
import { PageHeader } from '@/components/app/PageHeader'
import { RelativeTime } from '@/components/app/RelativeTime'
import { formatCount, formatDuration } from '@/lib/format'
import { useNow } from '@/lib/time'
import { Dash } from '@/features/overview/kit/Card'
import { APStatus, PushChip } from './APChips'
import { APActions } from './APActions'
import { usePushStates } from './hooks'
import { flagLabels, lastBoot, modelText } from './model'

// AP detail header (design §3.5): name, status, actions, then the identity
// and connection facts as a two-column list (two columns on phones too).
// Model, firmware and serial come from the AP and are read-only (G5).

export function FirmwareValue({ firmware }: { firmware: string }) {
  if (!firmware) return <span className="text-muted-foreground">Unknown until first connection</span>
  return (
    <span className="inline-flex min-w-0 flex-wrap items-center gap-1.5">
      <span className="font-mono text-[0.92em] break-all">{firmware}</span>
      {firmware === TestedFirmware ? <Chip tone="good">tested</Chip> : <Chip tone="warn">untested firmware</Chip>}
    </span>
  )
}

export function APHeader({ ap, siteId, experimental }: { ap: APDetail; siteId: string; experimental: boolean }) {
  const now = useNow()
  const push = usePushStates(siteId)
  const boot = lastBoot(ap, now)
  const secondary = ap.flags.filter((flag) => flag !== ap.status)
  const items = [
    { label: 'LAN IP', value: ap.lan_ip ? <Identifier value={ap.lan_ip} /> : <Dash /> },
    {
      label: 'Public address',
      value: ap.remote_address ? (
        <span className="inline-flex min-w-0 flex-wrap items-center gap-1">
          <Identifier value={ap.remote_address} />
          {ap.behind_nat ? <Chip tone="idle">via NAT</Chip> : null}
        </span>
      ) : (
        <Dash />
      ),
    },
    { label: 'Model', value: <span className={ap.model ? undefined : 'text-muted-foreground'}>{modelText(ap)}</span> },
    { label: 'Firmware', value: <FirmwareValue firmware={ap.firmware} /> },
    {
      label: 'Serial · SKU',
      value: ap.identity.serial || ap.identity.sku ? [ap.identity.serial, ap.identity.sku].filter(Boolean).join(' · ') : <Dash />,
    },
    {
      label: 'Uptime · boots',
      value: (
        <span className="tabular-nums">
          {ap.connected && ap.uptime_seconds ? formatDuration(ap.uptime_seconds) : '–'}
          {ap.identity.boot_count ? ` · ${formatCount(ap.identity.boot_count)} boots` : ''}
        </span>
      ),
    },
    {
      label: 'Last reboot',
      value: (
        <span className="grid gap-0.5">
          <span>
            {boot.at ? (
              <RelativeTime value={boot.at.toISOString()} />
            ) : (
              <Dash />
            )}
            {boot.source === 'uptime' ? <span className="text-xs text-muted-foreground"> (from uptime)</span> : null}
          </span>
          {ap.identity.last_reboot_reason || ap.identity.last_boot_state ? (
            <span className="text-xs text-muted-foreground">
              {[ap.identity.last_reboot_reason, ap.identity.last_boot_state].filter(Boolean).join(' · ')}
            </span>
          ) : null}
        </span>
      ),
    },
    {
      label: 'Connected since · reconnects',
      value: (
        <span>
          {ap.connected ? <RelativeTime value={ap.connected_at} /> : <span className="text-muted-foreground">Not connected</span>}
          {ap.connection.connection_count ? <span className="tabular-nums"> · {ap.connection.connection_count}</span> : null}
        </span>
      ),
    },
    { label: 'Pinned source IP', value: ap.pinned_ip ? <Identifier value={ap.pinned_ip} /> : 'Any (identified by ID)' },
    { label: 'ID', value: <Identifier value={ap.id} /> },
  ]
  return (
    <div className="grid min-w-0 gap-4">
      <PageHeader
        title={ap.name}
        actions={<APActions ap={ap} siteId={siteId} experimental={experimental} />}
        meta={
          <span className="flex flex-wrap items-center gap-1.5">
            <APStatus ap={ap} />
            <PushChip ap={ap} push={push[ap.id]} />
            {secondary.map((flag) => (
              <Chip key={flag} tone="idle">
                {flagLabels[flag] ?? flag.replace(/_/g, ' ')}
              </Chip>
            ))}
          </span>
        }
      />
      {/* PageHeader hides `meta` on phones; the chips matter there too. */}
      <div className="-mt-2 flex flex-wrap items-center gap-1.5 md:hidden">
        <APStatus ap={ap} />
        <PushChip ap={ap} push={push[ap.id]} />
        {secondary.map((flag) => (
          <Chip key={flag} tone="idle">
            {flagLabels[flag] ?? flag.replace(/_/g, ' ')}
          </Chip>
        ))}
      </div>
      {ap.status === 'rejected' && ap.last_error ? (
        <p role="alert" className="rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2 text-sm break-words">
          The AP rejected rev {ap.config_revision}: {ap.last_error}
        </p>
      ) : null}
      <KV items={items} className="rounded-lg border bg-card p-4" />
    </div>
  )
}
