import { useState } from 'react'
import { z } from 'zod'
import { useWatch } from 'react-hook-form'
import { Pencil, Power, RotateCw, Trash2 } from 'lucide-react'
import type { DiffEntry, Impact } from '@/api/types'
import { AttentionList } from '@/components/app/AttentionList'
import { ExperimentalBadge, InheritedValue, ShadowedBadge, UnverifiedBadge } from '@/components/app/badges'
import { ConfirmDialog, useConfirm } from '@/components/app/ConfirmDialog'
import { DetailDrawer } from '@/components/app/DetailDrawer'
import { DiffView } from '@/components/app/DiffView'
import { FilterSheet } from '@/components/app/FilterSheet'
import { Identifier } from '@/components/app/identifiers'
import { ImpactDialog } from '@/components/app/ImpactDialog'
import { ImpactNotice } from '@/components/app/ImpactNotice'
import { JsonEditor } from '@/components/app/JsonEditor'
import { KV } from '@/components/app/KV'
import { PageHeader } from '@/components/app/PageHeader'
import { QRCode } from '@/components/app/QRCode'
import { BandLabel, ChannelBadge, SignalChip, UtilBar, UtilLegend } from '@/components/app/radio'
import { RelativeTime } from '@/components/app/RelativeTime'
import { ResponsiveList, type ListColumn, type ListSort } from '@/components/app/ResponsiveList'
import { RowMenu } from '@/components/app/RowMenu'
import { SearchInput } from '@/components/app/SearchInput'
import { Section } from '@/components/app/Section'
import { StatGrid, StatTile } from '@/components/app/StatTile'
import { DisruptionChip, PushStateChip, SeverityChip, StatusChip } from '@/components/app/StatusChip'
import { EmptyState, ErrorState, SkeletonTiles } from '@/components/app/states'
import { TimeSeries } from '@/components/app/TimeSeries'
import { notify } from '@/components/app/toast'
import { Button } from '@/components/ui/button'
import { ApiError } from '@/api/errors'
import {
  AppForm,
  ChipsField,
  ComboboxField,
  FormSaveBar,
  ListField,
  MACListField,
  NumberField,
  PasswordField,
  SSHKeysField,
  SSIDField,
  SegmentedField,
  SelectField,
  SliderField,
  SwitchField,
  TextField,
  UnsavedChangesGuard,
  useAppForm,
  useDryRun,
  useExperimentalAck,
  useFormSubmit,
  validators,
} from '@/forms'
import { wifiQRPayload } from '@/lib/wifi'

// Synthetic content only (RFC 5737 addresses, 02-aa MACs, SSID "Example").

type Row = { id: string; name: string; status: 'in_sync' | 'offline' | 'applying'; clients: number; lan: string; uptime: string; model: string }
const rows: Row[] = [
  { id: '02-aa-00-00-00-01', name: 'Hallway', status: 'in_sync', clients: 15, lan: '192.0.2.21', uptime: '3 d 4 h', model: 'AP33-WW · apfw-0.15' },
  { id: '02-aa-00-00-00-02', name: 'Garage', status: 'offline', clients: 0, lan: '192.0.2.22', uptime: '–', model: 'AP33-WW · apfw-0.15' },
  { id: '02-aa-00-00-00-03', name: 'Study with a long name that wraps', status: 'applying', clients: 4, lan: '192.0.2.23', uptime: '12 h', model: 'AP33-WW · apfw-0.15' },
]

const columns: ListColumn<Row>[] = [
  { id: 'name', header: 'Name', sortable: true, cell: (row) => row.name, width: '28%' },
  { id: 'status', header: 'Status', card: 'aside', cell: (row) => <StatusChip status={row.status} /> },
  { id: 'clients', header: 'Clients', sortable: true, align: 'end', cell: (row) => row.clients },
  { id: 'lan', header: 'LAN IP', cell: (row) => <Identifier value={row.lan} /> },
  { id: 'uptime', header: 'Uptime', priority: 2, cell: (row) => row.uptime },
  { id: 'model', header: 'Model · Firmware', priority: 3, cell: (row) => row.model, truncate: true },
]

const diff: DiffEntry[] = [
  { path: 'WLANs[Example].Hidden', from: false, to: true },
  { path: 'WLANs[Example].Auth.PSK', from: '[changed]', to: '[changed]' },
  { path: 'Radios[5].RRM.Channel', from: 149, to: 36 },
]

const impact: Impact = {
  aps: [
    { id: '02-aa-00-00-00-01', name: 'Hallway', connected: true, push: 'notify', disruption: ['wlan_restart'], diff },
    { id: '02-aa-00-00-00-02', name: 'Garage', connected: false, push: 'reconnect', disruption: ['wlan_restart'], diff },
  ],
  disruption: 'wlan_restart',
  summary: 'WLAN Example restarts on 2 APs; about 15 clients reconnect',
}

const schema = z.object({
  ssid: validators.ssid,
  psk: validators.passphraseOrKeep,
  vlan_mode: z.enum(['untagged', 'tagged']),
  bands: z.array(z.enum(['2.4', '5'])).min(1, 'Choose at least one band.'),
  security: z.string(),
  min_rssi: validators.numberInRange(-95, -50, 'dBm').nullable(),
  offset: validators.numberInRange(-10, 10).nullable(),
  led: z.number().int().min(0).max(255).nullable(),
  hidden: z.boolean(),
  timezone: z.string(),
  dns: z.array(validators.ip),
  blocked: z.array(validators.mac),
  keys: z.array(z.string()),
  name: z.string().min(1, 'Enter a name.'),
  rf_mode: z.enum(['auto', 'static', 'experimental']),
})

// Fixed at load: the kit's times do not need to tick.
const loadedAt = Date.now()
const ago = (ms: number) => new Date(loadedAt - ms).toISOString()
const chartTimes = Array.from({ length: 24 }, (_, i) => Math.floor(loadedAt / 1000) - (23 - i) * 3600)

const timezones = ['Australia/Sydney', 'Australia/Melbourne', 'Australia/Perth', 'Pacific/Auckland', 'UTC'].map((zone) => ({ value: zone, label: zone }))

function KitForm() {
  const form = useAppForm(schema, {
    defaultValues: {
      ssid: 'Example',
      psk: '',
      vlan_mode: 'untagged',
      bands: ['2.4', '5'],
      security: 'wpa2_wpa3_personal',
      min_rssi: null,
      offset: 0,
      led: null,
      hidden: false,
      timezone: 'Australia/Sydney',
      dns: ['192.0.2.1'],
      blocked: [],
      keys: [],
      name: 'Hallway',
      rf_mode: 'auto',
    },
  })
  const values = useWatch({ control: form.control })
  const ack = useExperimentalAck('kit', values.rf_mode === 'experimental')
  const dryRun = useDryRun({
    scope: ['kit'],
    values,
    enabled: form.formState.isDirty,
    run: async () => {
      await new Promise((resolve) => setTimeout(resolve, 150))
      return { impact, warnings: [{ code: 'example', message: 'Example warning: band steering needs both bands.' }] }
    },
  })
  const { submit, alert, staleDialog, pending } = useFormSubmit({
    form,
    successMessage: 'Saved',
    mutationFn: async (input) => {
      await new Promise((resolve) => setTimeout(resolve, 200))
      if (input.ssid === 'taken') throw new ApiError({ status: 422, code: 'invalid', message: 'Check the highlighted fields.', fields: { ssid: 'Example-Guest already uses that name.' } })
      return { revision: 44 }
    },
  })
  return (
    <AppForm form={form} onSubmit={submit} alert={alert} id="kit-form">
      <UnsavedChangesGuard when={form.formState.isDirty} draft={{ key: 'kit', read: () => form.getValues() }} />
      <Section id="basic" title="Basic" description="Every control the forms kit offers, bound to one zod schema.">
        <SSIDField name="ssid" label="Network name (SSID)" help="Type “taken” and save to see a server field error." required />
        <PasswordField name="psk" label="Passphrase" stored help="8 to 63 printable ASCII characters." />
        <SelectField
          name="security"
          label="Security"
          choices={[
            { value: 'wpa2_personal', label: 'WPA2-Personal' },
            { value: 'wpa2_wpa3_personal', label: 'WPA2/WPA3-Personal' },
            { value: 'wpa3_personal', label: 'WPA3-Personal' },
            { value: 'owe', label: 'Enhanced Open (OWE)', experimental: true },
          ]}
          experimentalAllowed
        />
        <ChipsField name="bands" label="Bands" choices={[{ value: '2.4', label: '2.4 GHz' }, { value: '5', label: '5 GHz' }]} />
        <SegmentedField name="vlan_mode" label="VLAN" choices={[{ value: 'untagged', label: 'Untagged' }, { value: 'tagged', label: 'Tagged' }]} />
        <SwitchField name="hidden" label="Hidden SSID" help="Clients must type the name to join." />
      </Section>
      <Section id="numbers" title="Numbers" collapsible summary="Minimum signal off · offset 0">
        <NumberField name="min_rssi" label="Minimum client signal" min={-95} max={-50} unit="dBm" help="Empty turns it off." experimental />
        <NumberField name="offset" label="Power offset" min={-10} max={10} unit="dB" />
        <SliderField name="led" label="LED brightness" min={0} max={255} offLabel="Off" inherit={{ value: 255 }} />
        <TextField name="name" label="Name" required />
      </Section>
      <Section id="lists" title="Lists" collapsible defaultOpen={false} summary="1 DNS server · no blocked clients · no keys">
        <ComboboxField name="timezone" label="Timezone" choices={timezones} />
        <ListField name="dns" label="DNS servers" max={4} inputMode="decimal" itemLabel="DNS server" addLabel="Add DNS server" />
        <MACListField name="blocked" label="Blocked clients" />
        <SSHKeysField name="keys" label="SSH keys" />
      </Section>
      <Section id="rf" title="RF mode" badges={<ShadowedBadge paths={['Radios[5].Power']} />}>
        <SegmentedField
          name="rf_mode"
          label="Channel"
          choices={[
            { value: 'auto', label: 'Auto', hint: 'dismist plans the channel.' },
            { value: 'static', label: 'Static' },
            { value: 'experimental', label: 'Locate', experimental: true, hint: 'Experimental: shows the acknowledgement.' },
          ]}
          experimentalAllowed
        />
        {ack.node}
      </Section>
      <FormSaveBar form={form} formId="kit-form" dryRun={dryRun} pending={pending} blocked={ack.required} />
      {staleDialog}
    </AppForm>
  )
}

function renderFilters(draft: { status: string }, set: (next: { status: string }) => void) {
  return (
    <p className="px-2 text-sm">
      Status: {draft.status}{' '}
      <Button variant="outline" size="sm" onClick={() => set({ status: 'offline' })}>
        Offline only
      </Button>
    </p>
  )
}

export function KitPage() {
  const [sort, setSort] = useState<ListSort>({ id: 'name', desc: false })
  const [impactOpen, setImpactOpen] = useState(false)
  const [drawer, setDrawer] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [filters, setFilters] = useState({ status: 'all' })
  const [patch, setPatch] = useState('{\n  "LED": { "Brightness": 128 },\n  "WLANs": []\n}')
  const confirm = useConfirm()
  const sorted = rows
    .filter((row) => !query || row.name.toLowerCase().includes(query.toLowerCase()))
    .toSorted((a, b) => {
      const value = sort.id === 'clients' ? a.clients - b.clients : a.name.localeCompare(b.name)
      return sort.desc ? -value : value
    })
  return (
    <div className="grid gap-6">
      <PageHeader title="Component kit" meta="Shared components and the forms kit, with synthetic data." />

      <Section title="Status and markers">
        <div className="flex flex-wrap gap-2">
          <StatusChip status="in_sync" />
          <StatusChip status="applying" />
          <StatusChip status="pending" />
          <StatusChip status="offline" suffix="12 m" />
          <StatusChip status="rejected" />
          <SeverityChip severity="warning" />
          <PushStateChip state="pending" revision={43} />
          <PushStateChip state="applied" />
          <DisruptionChip disruption="radio_bounce" />
          <ExperimentalBadge />
          <UnverifiedBadge />
        </div>
        <ShadowedBadge full patch={{ to: '/dev/kit', label: 'Edit the expert patch' }} />
        <InheritedValue value={255} />
        <KV
          items={[
            { label: 'LAN IP', value: <Identifier value="192.0.2.21" /> },
            { label: 'Public address', value: <Identifier value="198.51.100.7" /> },
            { label: 'Last seen', value: <RelativeTime value={ago(42_000)} /> },
            { label: 'Channel', value: <ChannelBadge band="5" channel={149} width={80} /> },
            { label: 'Signal', value: <SignalChip signal="good" rssi={-61} /> },
            { label: 'Band', value: <BandLabel band="2.4" /> },
          ]}
        />
        <UtilBar util={0.38} parts={{ tx: 0.12, rx_in_bss: 0.14, rx_other_bss: 0.07, non_wifi: 0.05 }} />
        <UtilLegend />
      </Section>

      <StatGrid>
        <StatTile label="Access points" value="2/3" detail="1 offline" tone="warn" to="/dev/kit" />
        <StatTile label="Clients" value={19} detail="2.4 GHz 4 · 5 GHz 15" spark={[12, 14, 13, 17, 19, 18, 19]} />
        <StatTile label="WLANs" value={2} detail="2 enabled" />
        <StatTile label="Channels" value="1 · 149/80" />
      </StatGrid>
      <SkeletonTiles />

      <AttentionList
        items={[
          { id: 'a', severity: 'warn', title: 'Study has not applied rev 43 for 3 min', to: '/dev/kit' },
          { id: 'b', severity: 'crit', title: 'Garage offline for 12 min', detail: 'Last disconnect: ping timeout.', to: '/dev/kit' },
          { id: 'c', severity: 'info', title: '1 AP is waiting to be adopted' },
        ]}
      />

      <Section
        title="List"
        actions={
          <FilterSheet value={filters} defaults={{ status: 'all' }} count={filters.status === 'all' ? 0 : 1} onApply={setFilters}>
            {renderFilters}
          </FilterSheet>
        }
      >
        <SearchInput value={query} onChange={setQuery} label="Search access points" placeholder="Search access points" />
        <ResponsiveList
          label="Access points"
          rows={sorted}
          columns={columns}
          rowKey={(row) => row.id}
          rowLink={() => ({ to: '/dev/kit' })}
          rowTone={(row) => (row.status === 'offline' ? 'crit' : undefined)}
          renderActions={(row) => (
            <RowMenu
              label={`Actions for ${row.name}`}
              actions={[
                { id: 'edit', label: 'Edit', icon: Pencil, onSelect: () => notify.info(`Edit ${row.name}`) },
                { id: 'reboot', label: 'Reboot', icon: Power, disabled: true, hint: 'Needs a verified ap-cmd capability.' },
                { id: 'delete', label: 'Delete', icon: Trash2, destructive: true, onSelect: () => setDeleteOpen(true) },
              ]}
            />
          )}
          sort={sort}
          onSortChange={setSort}
          empty={<EmptyState title="No access points match." />}
        />
      </Section>

      <Section title="Charts">
        <TimeSeries
          label="Clients, 24 h"
          times={chartTimes}
          series={[
            { label: '5 GHz', values: Array.from({ length: 24 }, (_, i) => 10 + Math.round(5 * Math.sin(i / 3))), color: 2 },
            { label: '2.4 GHz', values: Array.from({ length: 24 }, (_, i) => 3 + (i % 4)), color: 3 },
          ]}
          unit="clients"
        />
      </Section>

      <Section title="Impact, diff and dialogs">
        <ImpactNotice impact={impact} />
        <ImpactNotice impact={{ aps: [], disruption: 'none', summary: '' }} />
        <DiffView entries={diff} />
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" onClick={() => setImpactOpen(true)}>
            Preview impact
          </Button>
          <Button variant="outline" onClick={() => setDrawer(true)}>
            Open details
          </Button>
          <Button
            variant="outline"
            onClick={async () => {
              if (await confirm({ title: 'Disable Example?', description: '15 clients will be disconnected from 2 APs.', confirmLabel: 'Disable', tone: 'destructive' })) notify.undo('Example disabled', () => notify.info('Example enabled again'))
            }}
          >
            Disable WLAN
          </Button>
          <Button variant="outline" onClick={() => notify.error(new ApiError({ status: 501, code: 'unverified_command', message: 'This AP has no verified remote command support yet.' }), 'Reboot not sent')}>
            <RotateCw /> Reboot
          </Button>
        </div>
        <ImpactDialog open={impactOpen} onOpenChange={setImpactOpen} title="Save WLAN Example?" impact={impact} warnings={[{ code: 'x', message: '5 GHz is enabled but no WLAN uses it.' }]} onConfirm={() => setImpactOpen(false)} />
        <DetailDrawer open={drawer} onOpenChange={setDrawer} title="DHCP failures on VLAN 30" description="Hallway · 3 clients">
          <KV items={[{ label: 'First seen', value: <RelativeTime value={ago(5_400_000)} /> }, { label: 'Count', value: 3 }]} />
        </DetailDrawer>
        <ConfirmDialog open={deleteOpen} onOpenChange={setDeleteOpen} title="Delete Hallway?" description="dismist forgets this AP and its secret. It stops being served at once." tone="destructive" confirmLabel="Delete AP" confirmText="Hallway" onConfirm={() => new Promise((resolve) => setTimeout(resolve, 300))} />
        <ErrorState error={new ApiError({ status: 503, code: 'unavailable', message: 'The controller is starting.' })} onRetry={() => undefined} />
      </Section>

      <Section title="Expert patch · Site Home" description="JSON lint, key completion from the firmware path list, and the forbidden-path mirror (WLANs is managed by dismist).">
        <JsonEditor label="Expert patch for site Home" value={patch} onChange={setPatch} paths={['LED.Brightness', 'LED.Locator', 'Radios[].Channel', 'Radios[].RRM.Power', 'Monitor.CCRev']} forbidden={['WLANs', 'Timestamp']} />
      </Section>

      <Section title="Wi-Fi QR">
        <QRCode value={wifiQRPayload({ ssid: 'Example', passphrase: 'correct-horse-battery' })} label="Scan to join Example" />
      </Section>

      <KitForm />
    </div>
  )
}
