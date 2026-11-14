import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Ban, PencilLine, Unplug } from 'lucide-react'
import { toast } from 'sonner'
import type { ClientDetail, EventView } from '@/gen/api.gen'
import { apQuery } from '@/api/queries/aps'
import { clientQuery, metaOf, useBlockClient, useClientMeta, useDisconnectClient } from '@/api/queries/clients'
import { describeError, isApiError } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { Input, Textarea } from '@/components/ui/input'
import { Chip } from '@/components/app/StatusChip'
import { Identifier, Mono } from '@/components/app/identifiers'
import { PageHeader } from '@/components/app/PageHeader'
import { RelativeTime } from '@/components/app/RelativeTime'
import { ErrorState, SkeletonRows } from '@/components/app/states'
import { formatBytes, formatDuration } from '@/lib/format'
import { isPrivateMac } from '@/lib/mac'
import { usePageTitle } from '@/features/shell/title'
import { Card, Dash, Facts, actionLink } from '@/features/overview/kit/Card'
import { ConfirmDialog, ResponsiveDialog } from '@/features/overview/kit/ConfirmDialog'
import { ExperimentalBadge } from '@/features/overview/kit/ExperimentalBadge'
import { SignalChip } from '@/features/overview/kit/SignalChip'
import { siteViewQuery } from '@/features/overview/site'
import { EventDrawer } from '@/features/events/EventDrawer'
import { EventList } from '@/features/events/EventList'
import { PrivateBadge, IPValue } from './ClientList'
import { bandText, displayName } from './model'

// Client detail (design §3.12): identity, connection, signal, traffic, a
// timeline of sessions and events, and the Block / Disconnect actions.

function BlockAction({ siteId, mac, name }: { siteId: string; mac: string; name: string }) {
  const [open, setOpen] = useState(false)
  const preview = useBlockClient(siteId, mac)
  const block = useBlockClient(siteId, mac)
  useEffect(() => {
    if (open) preview.mutate({ block: true, dryRun: true })
    else {
      preview.reset()
      block.reset()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])
  const summary = preview.data?.impact?.summary
  return (
    <>
      <Button variant="outline" onClick={() => setOpen(true)}>
        <Ban />
        Block
      </Button>
      <ConfirmDialog
        open={open}
        onOpenChange={setOpen}
        title={`Block ${name}?`}
        description={
          <>
            <p>The client is added to the site’s blocked clients and cannot join any WLAN that enforces the block list. This is a config change for every AP in the site.</p>
            <p className="text-foreground">
              {preview.isPending ? 'Checking the impact…' : summary || (preview.isError ? 'The impact could not be previewed.' : 'No AP changes.')}
            </p>
          </>
        }
        confirmLabel="Block client"
        destructive
        pending={block.isPending}
        error={block.error ? describeError(block.error).message : null}
        onConfirm={() =>
          block.mutate(
            { block: true },
            {
              onSuccess: () => {
                toast.success(`${name} blocked`)
                setOpen(false)
              },
            },
          )
        }
      />
    </>
  )
}

function DisconnectAction({ siteId, mac, name, apId }: { siteId: string; mac: string; name: string; apId: string }) {
  const [open, setOpen] = useState(false)
  const ap = useQuery({ ...apQuery(apId), enabled: Boolean(apId) })
  const site = useQuery({ ...siteViewQuery(siteId), retry: false })
  const disconnect = useDisconnectClient(siteId, mac)
  if (!ap.data?.capabilities.ap_cmd) return null
  const experimental = Boolean(site.data?.experimental)
  return (
    <>
      <Button variant="outline" onClick={() => setOpen(true)}>
        <Unplug />
        Disconnect
      </Button>
      <ConfirmDialog
        open={open}
        onOpenChange={setOpen}
        title={`Disconnect ${name}?`}
        description={
          <>
            <p>The client is disconnected from {ap.data.name}. It can reconnect straight away unless it is also blocked.</p>
            <p className="flex items-center gap-2">
              <ExperimentalBadge />
              {experimental ? 'Not yet verified on AP33 0.15.' : 'Turn on experimental options in Site settings › Advanced to use this.'}
            </p>
          </>
        }
        confirmLabel="Disconnect"
        destructive
        confirmDisabled={!experimental}
        pending={disconnect.isPending}
        error={disconnect.error ? (isApiError(disconnect.error) && disconnect.error.code === 'unverified_command' ? 'This AP’s firmware is not verified for remote commands yet.' : describeError(disconnect.error).message) : null}
        onConfirm={() =>
          disconnect.mutate(undefined, {
            onSuccess: () => {
              toast.success(`${name} disconnected`)
              setOpen(false)
            },
          })
        }
      />
    </>
  )
}

function MetaAction({ mac, detail }: { mac: string; detail: ClientDetail }) {
  const current = metaOf(detail)
  const [open, setOpen] = useState(false)
  const [alias, setAlias] = useState(current.alias)
  const [notes, setNotes] = useState(current.notes)
  const save = useClientMeta(mac)
  useEffect(() => {
    if (open) {
      setAlias(current.alias)
      setNotes(current.notes)
      save.reset()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])
  return (
    <>
      <Button variant="ghost" onClick={() => setOpen(true)}>
        <PencilLine />
        {current.alias ? 'Edit name' : 'Name this client'}
      </Button>
      <ResponsiveDialog
        open={open}
        onOpenChange={setOpen}
        title="Client name and notes"
        description="Shown instead of the hostname in dismist only; the client is not changed."
        footer={
          <>
            <Button variant="outline" onClick={() => setOpen(false)} disabled={save.isPending}>
              Cancel
            </Button>
            <Button
              disabled={save.isPending || [...alias].length > 64 || notes.length > 1000}
              onClick={() =>
                save.mutate(
                  { alias: alias.trim(), notes: notes.trim() },
                  {
                    onSuccess: () => {
                      toast.success('Saved')
                      setOpen(false)
                    },
                  },
                )
              }
            >
              {save.isPending ? 'Saving…' : 'Save'}
            </Button>
          </>
        }
      >
        <div className="grid gap-1.5">
          <label htmlFor="client-alias" className="text-sm font-medium">
            Name
          </label>
          <Input id="client-alias" value={alias} onChange={(e) => setAlias(e.target.value)} maxLength={64} autoComplete="off" />
        </div>
        <div className="grid gap-1.5">
          <label htmlFor="client-notes" className="text-sm font-medium">
            Notes
          </label>
          <Textarea id="client-notes" value={notes} onChange={(e) => setNotes(e.target.value)} maxLength={1000} />
        </div>
        {save.error ? (
          <p role="alert" className="text-sm text-destructive">
            {describeError(save.error).message}
          </p>
        ) : null}
      </ResponsiveDialog>
    </>
  )
}

export function ClientDetailPage({ siteId, mac }: { siteId: string; mac: string }) {
  const query = useQuery(clientQuery(mac, siteId))
  const [openEvent, setOpenEvent] = useState<EventView | null>(null)
  const detail = query.data
  const live = detail?.live ?? null
  const latest = detail?.sessions[0]
  const name = live ? displayName(live) : metaOf(detail).alias || mac
  usePageTitle(name)

  if (query.isPending) return <SkeletonRows rows={5} />
  if (query.isError || !detail) {
    return <ErrorState error={query.error} title="Could not load this client" onRetry={() => void query.refetch()} />
  }

  const privateMac = live?.private_mac ?? isPrivateMac(mac)
  const notes = metaOf(detail).notes
  const vendor = (live as unknown as { vendor?: string } | null)?.vendor
  return (
    <div className="grid min-w-0 gap-5">
      <PageHeader
        title={name}
        meta={live ? `Connected to ${live.ap_name || live.ap_id} on ${live.ssid || 'a WLAN'}` : 'Not connected right now'}
        actions={
          <>
            <MetaAction mac={mac} detail={detail} />
            <BlockAction siteId={siteId} mac={mac} name={name} />
            {live ? <DisconnectAction siteId={siteId} mac={mac} name={name} apId={live.ap_id} /> : null}
          </>
        }
      />
      <div className="-mt-2 flex flex-wrap items-center gap-1.5">
        {live ? <SignalChip signal={live.signal} rssi={live.rssi} /> : <Chip tone="idle">Not connected</Chip>}
        <PrivateBadge show={privateMac} />
        {live?.self_assigned ? <Chip tone="crit">self-assigned (DHCP failing)</Chip> : null}
      </div>
      {notes ? <p className="rounded-md border bg-muted/40 px-3 py-2 text-sm whitespace-pre-wrap">{notes}</p> : null}

      <div className="grid min-w-0 gap-4 lg:grid-cols-2">
        <Card title="Identity">
          <Facts
            items={[
              { label: 'MAC', value: <Identifier value={mac} /> },
              { label: 'Private address', value: privateMac ? 'Yes (randomised by the device)' : 'No' },
              { label: 'Vendor', value: vendor, hidden: !vendor },
              { label: 'Hostname', value: live?.hostname || <Dash /> },
              { label: 'IP', value: live ? <IPValue client={live} /> : latest?.ip ? <Mono>{latest.ip}</Mono> : <Dash /> },
              { label: 'IPv6', value: <Mono>{live?.ipv6.join(', ')}</Mono>, hidden: !live?.ipv6.length },
              { label: 'Username (802.1X)', value: detail.username, hidden: !detail.username },
              { label: 'User agent', value: <span className="text-xs break-words">{detail.user_agent}</span>, hidden: !detail.user_agent },
            ]}
          />
        </Card>
        <Card title="Connection">
          {live ? (
            <Facts
              items={[
                {
                  label: 'AP',
                  value: live.ap_id ? (
                    <Link to="/sites/$siteId/aps/$apId" params={{ siteId, apId: live.ap_id }} data-inline-link className="text-brand underline underline-offset-4">
                      {live.ap_name || live.ap_id}
                    </Link>
                  ) : (
                    <Dash />
                  ),
                },
                { label: 'WLAN', value: live.ssid || <Dash /> },
                { label: 'Band and channel', value: `${bandText(live) || '–'}${live.width ? ` · ${live.width} MHz` : ''}` },
                { label: 'BSSID', value: live.bssid ? <Mono className="text-xs">{live.bssid}</Mono> : <Dash /> },
                { label: 'Protocol', value: live.protocol ? `802.11${live.protocol}` : <Dash /> },
                { label: 'Spatial streams', value: live.streams ?? <Dash /> },
                { label: 'Connected for', value: live.connected_seconds ? formatDuration(live.connected_seconds) : <Dash /> },
                { label: 'Idle', value: live.idle_ms ? `${(live.idle_ms / 1000).toFixed(1)} s` : <Dash /> },
              ]}
            />
          ) : (
            <p className="text-sm text-muted-foreground">
              {latest ? (
                <>
                  Last seen <RelativeTime value={latest.disconnected_at ?? latest.last_seen} /> on {latest.ap_name || latest.ap_id}
                  {latest.ssid ? ` (${latest.ssid})` : ''}.
                </>
              ) : (
                'No recent sessions for this client.'
              )}
            </p>
          )}
        </Card>
        {live ? (
          <>
            <Card title="Signal">
              <Facts
                items={[
                  { label: 'RSSI', value: live.rssi ? `${live.rssi} dBm` : <Dash /> },
                  { label: 'Average', value: live.avg_rssi ? `${live.avg_rssi} dBm` : <Dash /> },
                  { label: 'Per antenna', value: detail.per_antenna_rssi.length ? detail.per_antenna_rssi.map((v) => `${v} dBm`).join(' · ') : <Dash /> },
                  { label: 'SNR', value: live.snr !== null ? `${live.snr} dB` : <Dash /> },
                  { label: 'Quality', value: <SignalChip signal={live.signal} /> },
                ]}
              />
            </Card>
            <Card title="Traffic">
              <Facts
                items={[
                  {
                    label: 'Link rate (tx / rx)',
                    value: live.tx_rate_mbps !== null || live.rx_rate_mbps !== null ? `${live.tx_rate_mbps ?? '–'} / ${live.rx_rate_mbps ?? '–'} Mbit/s` : <Dash />,
                  },
                  { label: 'Download (to client)', value: formatBytes(live.down_bytes) },
                  { label: 'Upload (from client)', value: formatBytes(live.up_bytes) },
                  { label: 'Retries (tx / rx)', value: detail.retries.tx || detail.retries.rx ? `${detail.retries.tx} / ${detail.retries.rx}` : <Dash /> },
                  ...detail.qos.map((q) => ({ label: `QoS ${q.ac}`, value: `${formatBytes(q.tx_bytes)} / ${formatBytes(q.rx_bytes)}` })),
                ]}
              />
              <p className="mt-3 text-xs text-muted-foreground">Link rate units are not yet verified against the AP.</p>
            </Card>
          </>
        ) : null}
      </div>

      <Card title="Sessions" bodyClassName={detail.sessions.length ? 'p-0' : undefined}>
        {detail.sessions.length ? (
          <ul className="divide-y text-sm">
            {detail.sessions.map((s) => (
              <li key={`${s.ap_id}-${s.connected_at}`} className="grid min-w-0 gap-0.5 px-4 py-2.5">
                <span className="flex flex-wrap items-center justify-between gap-2">
                  <span className="font-medium">
                    {s.ap_name || s.ap_id}
                    {s.ssid ? <span className="font-normal text-muted-foreground"> · {s.ssid}</span> : null}
                  </span>
                  {s.disconnected_at ? <span className="text-xs text-muted-foreground">{s.end_reason || 'Ended'}</span> : <Chip tone="good">Connected</Chip>}
                </span>
                <span className="text-xs text-muted-foreground tabular-nums">
                  From <RelativeTime value={s.connected_at} />
                  {s.disconnected_at ? (
                    <>
                      {' '}
                      to <RelativeTime value={s.disconnected_at} />
                    </>
                  ) : null}
                  {s.ip ? ` · ${s.ip}` : ''} · {formatBytes(s.down_bytes)} down / {formatBytes(s.up_bytes)} up
                </span>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-sm text-muted-foreground">No sessions recorded.</p>
        )}
      </Card>

      <Card
        title="Events"
        bodyClassName={detail.events.length ? 'p-0' : undefined}
        action={
          <Link to="/sites/$siteId/events" params={{ siteId }} search={{ client: mac }} className={actionLink}>
            All events for this client
          </Link>
        }
      >
        {detail.events.length ? (
          <EventList events={detail.events} onOpen={setOpenEvent} label="Events for this client" className="rounded-none border-0" />
        ) : (
          <p className="text-sm text-muted-foreground">No events for this client.</p>
        )}
      </Card>
      <EventDrawer siteId={siteId} summary={openEvent} onOpenChange={(next) => !next && setOpenEvent(null)} />
    </div>
  )
}
