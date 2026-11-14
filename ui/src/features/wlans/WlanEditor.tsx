import { useMemo, useRef, useState } from 'react'
import { Link, useNavigate, useRouter } from '@tanstack/react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useFormContext, useWatch } from 'react-hook-form'
import { toast } from 'sonner'
import { Code2, Copy, EllipsisVertical, QrCode, Trash2 } from 'lucide-react'
import { keys } from '@/api/keys'
import { createNetwork, deleteNetwork, networkQuery, networksQuery, patchNetwork, type WLANView } from '@/api/queries/networks'
import { siteQuery } from '@/api/queries/sites'
import type { MutationResponse } from '@/gen/api.gen'
import { PageHeader } from '@/components/app/PageHeader'
import { ErrorState, SkeletonRows } from '@/components/app/states'
import { Identifier } from '@/components/app/identifiers'
import { KV } from '@/components/app/KV'
import { Button, buttonVariants } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { usePageTitle } from '@/features/shell/title'
import { ConfigForm } from '@/features/settings/kit/ConfigForm'
import { BoundSwitch, OptionField, useOptionState } from '@/features/settings/kit/fields'
import { UnsavedChangesGuard } from '@/features/settings/kit/guard'
import { ConfirmDialog } from '@/features/settings/kit/primitives'
import { Section } from '@/features/settings/kit/Section'
import { LegacyModeBanner, useSiteFormContext, useTypedEditing } from '@/features/settings/kit/site'
import { useConfigForm } from '@/features/settings/kit/useConfigForm'
import { duplicateForm, presetForm, toForm, toInput, type Preset, type WlanForm } from './form'
import {
  CoARows,
  DynamicVlanField,
  MinRSSIField,
  MPSKKeys,
  PassphraseField,
  RateLimitField,
  ScheduleDays,
  SecurityField,
  ServerRows,
  VlanField,
} from './fields'
import { caseOnlyDuplicate, wlanSchema } from './schema'
import { effectivePMF, isEnterprise, isPersonal, passphraseHelp, securityLabel } from './security'
import { WifiQRDialog } from './WifiQR'

// The WLAN editor (design §3.9): one form, one POST or PATCH. Basic fields
// first; advanced sections start collapsed with a summary line.

type Props = { siteId: string; wlanId?: string; preset?: Preset; from?: string; dialog?: string }

export function WlanEditor(props: Props) {
  const { siteId, wlanId } = props
  const network = useQuery({ ...networkQuery(siteId, wlanId ?? ''), enabled: Boolean(wlanId) })
  const networks = useQuery(networksQuery(siteId))
  const source = useQuery({ ...networkQuery(siteId, props.from ?? ''), enabled: Boolean(props.from) })
  const site = useQuery(siteQuery(siteId))
  usePageTitle(wlanId ? network.data?.ssid : 'New WLAN')

  if (wlanId && network.isError) return <ErrorState error={network.error} title="Could not load this WLAN" onRetry={() => void network.refetch()} />
  if ((wlanId && !network.data) || !networks.data || !site.data || (props.from && !source.data && !source.isError)) {
    return (
      <div className="grid gap-4">
        <PageHeader title={wlanId ? 'WLAN' : 'New WLAN'} />
        <SkeletonRows rows={4} />
      </div>
    )
  }
  return (
    <WlanEditorForm
      {...props}
      view={network.data}
      networks={networks.data}
      experimental={site.data.experimental}
      source={source.data}
    />
  )
}

function WlanEditorForm({
  siteId,
  wlanId,
  preset,
  view,
  networks,
  experimental,
  source,
  dialog,
}: Props & { view?: WLANView; networks: WLANView[]; experimental: boolean; source?: WLANView }) {
  const create = !wlanId
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { typed } = useTypedEditing()
  const values = useMemo(
    () => (view ? toForm(view) : source ? duplicateForm(source) : presetForm(preset, experimental)),
    [view, source, preset, experimental],
  )
  const schema = useMemo(() => wlanSchema({ create, current: view, others: networks.filter((n) => n.id !== view?.id) }), [create, view, networks])
  const context = useSiteFormContext(siteId, {
    expert: view?.expert ?? null,
    expertLink: view ? { to: `/sites/${encodeURIComponent(siteId)}/wlans/${encodeURIComponent(view.id)}/expert` } : null,
  })
  const [deleting, setDeleting] = useState(false)
  // The Wi-Fi QR dialog lives at ?dialog=qr (design §2.5): opening pushes
  // it so Back closes it; closing pops that entry, or replaces the URL when
  // the dialog came from a shared link.
  const router = useRouter()
  const pushed = useRef(false)
  const qrOpen = dialog === 'qr'
  const setQrOpen = (open: boolean) => {
    if (!view) return
    const here = `/sites/${encodeURIComponent(siteId)}/wlans/${encodeURIComponent(view.id)}`
    if (open) {
      pushed.current = true
      void navigate({ to: here as never, search: { dialog: 'qr' } as never })
    } else if (qrOpen) {
      if (pushed.current) router.history.back()
      else void navigate({ to: here as never, search: {} as never, replace: true })
      pushed.current = false
    }
  }

  const controller = useConfigForm<WlanForm, WLANView>({
    formKey: `wlan:${siteId}:${wlanId ?? 'new'}`,
    scope: 'wlan',
    values,
    schema,
    siteId,
    ifMatch: view?.updated_rev,
    readOnly: false,
    submit: (body, options) => (create ? createNetwork(siteId, body as never, options) : patchNetwork(siteId, wlanId!, body as never, options)),
    toBody: (patch, vals) => toInput(patch, vals, create),
    toValues: (object) => toForm(object),
    fieldMap: { vlan_id: 'vlan.id' },
    successMessage: (res) => (create ? `WLAN ${res.object.ssid} created` : `WLAN ${res.object.ssid} saved`),
    confirm: (vals, base) => {
      if (!create && base.enabled && !vals.enabled) {
        const clients = view?.live.clients ?? 0
        const aps = view?.live.aps_broadcasting ?? 0
        return {
          title: `Disable ${base.ssid}?`,
          description: `${clients} client${clients === 1 ? '' : 's'} will be disconnected from ${aps} AP${aps === 1 ? '' : 's'}.`,
          action: 'Disable and save',
          destructive: true,
        }
      }
      return null
    },
    onSaved: (response: MutationResponse<WLANView>) => {
      queryClient.setQueryData(keys.network(response.object.id), response.object)
      void queryClient.invalidateQueries({ queryKey: keys.siteNetworks(siteId) })
      if (create) void navigate({ to: `/sites/${encodeURIComponent(siteId)}/wlans/${encodeURIComponent(response.object.id)}` as never, replace: true })
    },
  })

  async function remove() {
    if (!view) return
    try {
      await deleteNetwork(siteId, view.id, { ifMatch: view.updated_rev })
      toast.success(`WLAN ${view.ssid} deleted`)
      queryClient.removeQueries({ queryKey: keys.network(view.id) })
      void queryClient.invalidateQueries({ queryKey: keys.siteNetworks(siteId) })
      setDeleting(false)
      void navigate({ to: `/sites/${encodeURIComponent(siteId)}/wlans` as never, replace: true })
    } catch (error) {
      toast.error('Not deleted', { description: error instanceof Error ? error.message : undefined })
    }
  }

  const title = create ? (source ? `Duplicate ${source.ssid}` : 'New WLAN') : view!.ssid
  const legacyEditable = !typed
  return (
    <div className="grid gap-5">
      <PageHeader
        title={title}
        meta={view ? `${securityLabel(view.security)} · ${view.bands.map((b) => `${b} GHz`).join(' + ')}` : undefined}
        actions={view ? <EditorActions siteId={siteId} view={view} onDelete={() => setDeleting(true)} onQR={() => setQrOpen(true)} /> : null}
      />
      <LegacyModeBanner />
      <ConfigForm controller={controller} context={context} saveLabel={create ? 'Create WLAN' : 'Save'} alwaysShowBar={create}>
        <WlanSections create={create} view={view} networks={networks} legacy={legacyEditable} />
      </ConfigForm>
      {view ? <ReadOnlyFacts view={view} /> : null}
      <UnsavedChangesGuard />
      {view ? (
        <>
          <ConfirmDialog
            open={deleting}
            onOpenChange={setDeleting}
            title={`Delete ${view.ssid}?`}
            description={`${view.live.clients} client${view.live.clients === 1 ? '' : 's'} will be disconnected from ${view.live.aps_broadcasting} AP${view.live.aps_broadcasting === 1 ? '' : 's'}. Its BSSID index is freed for the next new WLAN.`}
            action="Delete WLAN"
            destructive
            onConfirm={() => void remove()}
          />
          <WifiQRDialog siteId={siteId} view={view} open={qrOpen} onOpenChange={setQrOpen} />
        </>
      ) : null}
    </div>
  )
}

function EditorActions({ siteId, view, onDelete, onQR }: { siteId: string; view: WLANView; onDelete: () => void; onQR: () => void }) {
  const base = `/sites/${encodeURIComponent(siteId)}/wlans`
  return (
    <>
      <div className="hidden flex-wrap gap-2 md:flex">
        <Button variant="outline" onClick={onQR} disabled={!isPersonal(view.security) && view.security !== 'open'}>
          <QrCode /> Wi-Fi QR
        </Button>
        <Link to={`${base}/new` as never} search={{ from: view.id } as never} className={buttonVariants({ variant: 'outline' })}>
          <Copy /> Duplicate
        </Link>
        <Link to={`${base}/${encodeURIComponent(view.id)}/expert` as never} className={buttonVariants({ variant: 'outline' })}>
          <Code2 /> Expert patch
        </Link>
        <Button variant="outline" onClick={onDelete} className="text-destructive">
          <Trash2 /> Delete
        </Button>
      </div>
      <DropdownMenu>
        <DropdownMenuTrigger className={buttonVariants({ variant: 'outline', size: 'icon', className: 'md:hidden' })} aria-label="WLAN actions">
          <EllipsisVertical />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem onSelect={onQR}>
            <QrCode /> Wi-Fi QR
          </DropdownMenuItem>
          <DropdownMenuItem asChild>
            <Link to={`${base}/new` as never} search={{ from: view.id } as never}>
              <Copy /> Duplicate
            </Link>
          </DropdownMenuItem>
          <DropdownMenuItem asChild>
            <Link to={`${base}/${encodeURIComponent(view.id)}/expert` as never}>
              <Code2 /> Expert patch
            </Link>
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={onDelete} className="text-destructive">
            <Trash2 /> Delete
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </>
  )
}

function WlanSections({ create, view, networks, legacy }: { create: boolean; view?: WLANView; networks: WLANView[]; legacy: boolean }) {
  const { control, formState, setValue, getValues } = useFormContext<WlanForm>()
  const values = useWatch({ control }) as WlanForm
  const security = values.security
  const personal = isPersonal(security)
  const enterprise = isEnterprise(security)
  const pmf = effectivePMF(security, values.pmf)
  const caseWarning = values.ssid ? caseOnlyDuplicate(values.ssid, { others: networks, current: view }) : null
  const hadPassphrase = Boolean(view?.has_psk && isPersonal(view.security))
  const mpsk = useOptionState('wlan.mpsk')
  const schedule = useOptionState('wlan.schedule')
  const errorIn = (prefix: string) => Object.keys(formState.errors).some((k) => k === prefix || k.startsWith(prefix))

  return (
    <>
      <Section id="basic" title="Basic">
        <OptionField k="wlan.ssid" footer={caseWarning ? <p className="text-sm text-warning">{caseWarning}</p> : null} />
        <OptionField k="wlan.enabled" />
        <SecurityField />
        {personal ? <PassphraseField saved={hadPassphrase && !create} help={passphraseHelp(security)} /> : null}
        <OptionField k="wlan.bands" />
        <VlanField />
        <OptionField k="wlan.hidden" />
        <OptionField k="wlan.isolation" />
        {enterprise ? <ServerRows name="radius.auth" optionKey="wlan.radius.auth" defaultPort={1812} /> : null}
        {legacy ? <p className="text-sm text-muted-foreground">Only the SSID, passphrase and Enabled can be changed until typed settings resume.</p> : null}
      </Section>

      <Section
        id="security"
        title="Security"
        collapsible
        defaultOpen={false}
        forceOpen={errorIn('pmf') || errorIn('group_rekey_sec')}
        summary={`${securityLabel(security)} · PMF ${pmf.label.toLowerCase()}`}
      >
        {pmf.editable ? (
          <OptionField k="wlan.pmf" />
        ) : (
          <div className="grid gap-1">
            <span className="text-sm font-medium">Protected management frames (802.11w)</span>
            <span className="text-sm text-muted-foreground">{pmf.label}</span>
          </div>
        )}
        <OptionField k="wlan.sae_h2e_only" />
        <OptionField k="wlan.transition_disable" />
        <OptionField k="wlan.group_rekey_sec" />
        <OptionField k="wlan.eapol_v1" />
        {enterprise ? <OptionField k="wlan.radius.mac_auth" /> : null}
        <OptionField k="wlan.owe_ssid" />
      </Section>

      <Section
        id="roaming"
        title="Roaming"
        collapsible
        defaultOpen={false}
        summary={`Fast roaming ${values.roaming.fast === 'none' ? 'off' : values.roaming.fast === '11r' ? '802.11r' : 'OKC'} · 802.11k ${values.roaming.neighbor_report ? 'on' : 'off'} · 802.11v ${values.roaming.bss_transition ? 'on' : 'off'}`}
      >
        <OptionField k="wlan.roaming.fast" />
        <OptionField k="wlan.roaming.ap_key_cache" />
        <OptionField k="wlan.roaming.neighbor_report" />
        <OptionField k="wlan.roaming.bss_transition" />
        <OptionField k="wlan.roaming.ap_steering" />
        <OptionField k="wlan.roaming.bss_load" />
      </Section>

      <Section
        id="radio"
        title="Radio and rates"
        collapsible
        defaultOpen={false}
        forceOpen={errorIn('band_steering') || errorIn('dtim') || errorIn('min_rssi')}
        summary={`Band steering ${values.band_steering ? 'on' : 'off'} · ${values.rates.replace('_', ' ')} rates · DTIM ${values.dtim ?? '–'}`}
      >
        <OptionField k="wlan.band_steering" />
        <OptionField k="wlan.rates" />
        <MinRSSIField />
        <OptionField k="wlan.dtim" />
        <OptionField k="wlan.wmm_disabled" />
        <OptionField k="wlan.uapsd_disabled" />
        <OptionField k="wlan.disable_11ax" />
        <OptionField k="wlan.disable_11nac" />
      </Section>

      <Section
        id="network"
        title="Network"
        collapsible
        defaultOpen={false}
        summary={`Broadcast ${values.broadcast.limit ? 'limited' : 'unlimited'} · ${values.dhcp_only ? 'DHCP clients only' : 'static IPs allowed'}`}
      >
        <OptionField k="wlan.broadcast.limit" />
        <div className="grid gap-4 border-l-2 pl-4">
          <OptionField k="wlan.broadcast.allow_mdns" />
          <OptionField k="wlan.broadcast.allow_ssdp" />
          <OptionField k="wlan.broadcast.allow_ipv6_nd" />
        </div>
        <OptionField k="wlan.broadcast.arp_filter" />
        <OptionField k="wlan.broadcast.mcast_to_ucast" />
        <OptionField k="wlan.broadcast.wireless_bridge_dhcp" />
        <OptionField k="wlan.dhcp_only" />
        <OptionField k="wlan.dhcp_dns_only" />
        <OptionField k="wlan.option82.enabled" />
        <OptionField k="wlan.option82.circuit_id" />
      </Section>

      <Section
        id="limits"
        title="Limits and clients"
        collapsible
        defaultOpen={false}
        forceOpen={errorIn('max_clients') || errorIn('idle_timeout_sec') || errorIn('rate_limit')}
        summary={`${values.max_clients ? `${values.max_clients} clients max` : 'No client limit'} · idle ${values.idle_timeout_sec ?? '–'} s`}
      >
        <OptionField k="wlan.max_clients" placeholder="No limit (512)" />
        <OptionField k="wlan.idle_timeout_sec" />
        <RateLimitField />
        <OptionField k="wlan.qos_class" />
        <OptionField k="wlan.honour_blocklist" />
      </Section>

      {enterprise ? (
        <Section id="radius" title="RADIUS" collapsible defaultOpen={false} forceOpen={errorIn('radius')} summary={`${values.radius.auth.length} authentication · ${values.radius.acct.length} accounting server(s)`}>
          <ServerRows
            name="radius.acct"
            optionKey="wlan.radius.acct"
            defaultPort={1813}
            sameAs={() =>
              setValue(
                'radius.acct',
                getValues('radius.auth').map((s) => ({ ...s, port: 1813 })),
                { shouldDirty: true, shouldValidate: true },
              )
            }
          />
          <OptionField k="wlan.radius.acct_interim_sec" placeholder="Server’s value" />
          <OptionField k="wlan.radius.nas_id" placeholder="Chosen by the AP" />
          <OptionField k="wlan.radius.nas_ip" />
          <DynamicVlanField />
          <OptionField k="wlan.radius.reauth_sec" placeholder="Off" />
          <OptionField k="wlan.radius.reauth_on_session_timeout" />
          <CoARows />
        </Section>
      ) : null}

      {!mpsk.hidden && (security === 'wpa2_personal' || values.mpsk.enabled || values.mpsk.keys.length) ? (
        <Section id="mpsk" title="Per-device passphrases" badges={mpsk.badges} collapsible defaultOpen={values.mpsk.enabled} forceOpen={errorIn('mpsk')} summary={values.mpsk.enabled ? `${values.mpsk.keys.length} key(s)` : 'Off'} description={mpsk.meta.help}>
          <BoundSwitch name="mpsk.enabled" optionKey="wlan.mpsk" label="Per-device passphrases" />
          {values.mpsk.enabled ? (
            <>
              <MPSKKeys />
              <BoundSwitch name="mpsk.private" optionKey="wlan.mpsk" label="Isolate each key’s devices from the others" />
            </>
          ) : null}
        </Section>
      ) : null}

      {!schedule.hidden ? (
        <Section id="schedule" title="Schedule" badges={schedule.badges} collapsible defaultOpen={values.schedule.enabled} forceOpen={errorIn('schedule')} summary={values.schedule.enabled ? 'On a schedule' : 'Always on'} description={schedule.meta.help}>
          <BoundSwitch name="schedule.enabled" optionKey="wlan.schedule" label="Only broadcast during these hours" />
          {values.schedule.enabled ? <ScheduleDays disabled={!schedule.editable} /> : null}
        </Section>
      ) : null}
    </>
  )
}

function ReadOnlyFacts({ view }: { view: WLANView }) {
  const pmf = effectivePMF(view.security, view.pmf)
  return (
    <section aria-labelledby="facts-heading" className="grid gap-4 rounded-xl border bg-card px-4 py-4 md:px-5">
      <h2 id="facts-heading" className="text-base font-semibold">
        Facts
      </h2>
      <KV
        items={[
          { label: 'WLAN ID', value: <Identifier value={view.id} /> },
          { label: 'BSSID index', value: `${view.bssid_index}${view.owe_bssid_index ? ` (OWE companion ${view.owe_bssid_index})` : ''}` },
          { label: 'Effective PMF', value: pmf.label },
          { label: 'Effective max clients', value: view.max_clients ?? 512 },
          ...(view.mdid && view.roaming.fast === '11r' ? [{ label: 'MDID (802.11r)', value: <Identifier value={String(view.mdid)} /> }] : []),
          { label: 'Broadcasting', value: `${view.live.aps_broadcasting} of ${view.live.aps_total} APs` },
          { label: 'Clients now', value: view.live.clients },
        ]}
      />
      {view.live_vaps?.length ? (
        <div className="grid gap-2">
          <h3 className="text-sm font-semibold text-muted-foreground">Live BSSIDs</h3>
          <ul className="grid gap-2 md:grid-cols-2">
            {view.live_vaps.map((vap) => (
              <li key={`${vap.ap_id}:${vap.band}`} className="grid gap-0.5 rounded-lg border p-3 text-sm">
                <span className="font-medium">
                  {vap.ap_name} · {vap.band} GHz
                </span>
                <Identifier value={vap.bssid} />
                <span className="text-xs text-muted-foreground">
                  {vap.dev} · {vap.clients} client{vap.clients === 1 ? '' : 's'}
                </span>
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </section>
  )
}
