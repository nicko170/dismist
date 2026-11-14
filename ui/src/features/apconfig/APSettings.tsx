import { useEffect, useId, useMemo, useState, type ReactNode } from 'react'
import { Link } from '@tanstack/react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useController, useFormContext, useWatch, type FieldValues } from 'react-hook-form'
import { toast } from 'sonner'
import { z } from 'zod'
import { Code2, FileUp, Lightbulb, LightbulbOff, Loader2, TriangleAlert } from 'lucide-react'
import { describeError } from '@/api/errors'
import { keys } from '@/api/keys'
import { apSettingsQuery, locateAP, patchAP, PROTOCOL_FEATURES, unlocateAP, type APPatch, type APSettingsView } from '@/api/queries/apsettings'
import { rfQuery, type RFProfileView } from '@/api/queries/rf'
import { siteQuery, systemMigrationQuery, type SiteConfigView } from '@/api/queries/sites'
import type { Band, MutationResponse, RadioView } from '@/gen/api.gen'
import { Identifier } from '@/components/app/identifiers'
import { KV } from '@/components/app/KV'
import { PageHeader } from '@/components/app/PageHeader'
import { RelativeTime } from '@/components/app/RelativeTime'
import { ErrorState, SkeletonRows } from '@/components/app/states'
import { Chip } from '@/components/app/StatusChip'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button, buttonVariants } from '@/components/ui/button'
import { Input, Textarea } from '@/components/ui/input'
import { sitesQuery } from '@/features/shell/queries'
import { usePageTitle } from '@/features/shell/title'
import { ExperimentalBadge, InheritedValue } from '@/features/settings/kit/badges'
import { ConfigForm } from '@/features/settings/kit/ConfigForm'
import { describedBy, FieldShell, NumberInput, OptionField, SliderInput, SwitchRow, useOptionState } from '@/features/settings/kit/fields'
import { UnsavedChangesGuard } from '@/features/settings/kit/guard'
import { NativeSelect, Segmented, Switch } from '@/features/settings/kit/primitives'
import { Section } from '@/features/settings/kit/Section'
import { LegacyModeBanner, useSiteFormContext } from '@/features/settings/kit/site'
import { useConfigForm, type ConfirmRequest } from '@/features/settings/kit/useConfigForm'
import { isIP, isIPv4, isNTPServer, secretByteLength, bytesToBase64 } from '@/features/settings/kit/validators'
import { eth1Error, eth1FromForm, eth1ToForm, Eth1Field, ServerListField, SmallNumber, type Eth1Form } from '@/features/settings/fields'
import { primaries, staticChannelError, WIDTHS } from '@/features/rf/channels'
import { BAND_KEY } from '@/features/rf/form'
import { cn } from '@/lib/utils'
import { hostnamePreview } from './hostname'

// AP settings (design §3.6). Sections are independent forms, each with its
// own PATCH of only its fields and its own save bar. Values inherited from the
// site show "Site default" next to an Override switch. Model and firmware are
// read-only (they come from the AP).

export function APSettings({ siteId, apId }: { siteId: string; apId: string }) {
  const ap = useQuery(apSettingsQuery(apId))
  const site = useQuery(siteQuery(siteId))
  usePageTitle(ap.data ? `${ap.data.name || ap.data.id} settings` : undefined)
  if (ap.isError) {
    return (
      <div className="grid gap-4">
        <PageHeader as="h2" title="AP settings" />
        <ErrorState error={ap.error} title="Could not load this AP" onRetry={() => void ap.refetch()} />
      </div>
    )
  }
  if (!ap.data || !site.data) {
    return (
      <div className="grid gap-4">
        <PageHeader as="h2" title="AP settings" />
        <SkeletonRows rows={4} />
      </div>
    )
  }
  return <APSettingsContent siteId={siteId} ap={ap.data} site={site.data} />
}

function APSettingsContent({ siteId, ap, site }: { siteId: string; ap: APSettingsView; site: SiteConfigView }) {
  const name = ap.name || ap.id
  const base = `/sites/${encodeURIComponent(siteId)}/aps/${encodeURIComponent(ap.id)}`
  return (
    <div className="grid gap-5">
      <PageHeader
        as="h2"
        title={`${name} settings`}
        meta={`${ap.model || 'Model unknown until first connection'} · ${ap.firmware || 'firmware unknown'}`}
        actions={
          <Link to={`${base}/config` as never} search={{ view: 'expert' } as never} className={buttonVariants({ variant: 'outline' })}>
            <Code2 /> Expert patch
          </Link>
        }
      />
      <LegacyModeBanner />
      <GeneralSection siteId={siteId} ap={ap} site={site} />
      <LEDSection siteId={siteId} ap={ap} site={site} />
      <SSHSection siteId={siteId} ap={ap} site={site} />
      <RadiosSection siteId={siteId} ap={ap} />
      <DiagnosticsSection siteId={siteId} ap={ap} />
      <ManagementSection siteId={siteId} ap={ap} site={site} />
      <PortsSection siteId={siteId} ap={ap} site={site} />
      <Section id="expert" title="Expert patch" description="A JSON merge patch for this AP only. It wins over the site settings and this AP’s typed settings.">
        <Link to={`${base}/config` as never} search={{ view: 'expert' } as never} className={buttonVariants({ variant: 'outline', className: 'justify-self-start' })}>
          <Code2 /> Open the expert patch
        </Link>
      </Section>
      <UnsavedChangesGuard />
    </div>
  )
}

// ---- section form ----

type APSectionProps<V extends FieldValues> = {
  siteId: string
  ap: APSettingsView
  formKey: string
  values: V
  schema: z.ZodType
  toBody: (values: V, patch: Record<string, unknown>) => APPatch
  toValues: (ap: APSettingsView) => V
  confirm?: (values: V, base: V) => ConfirmRequest | null
  legacyWritable?: boolean
  experimentalOverride?: boolean
  children: ReactNode
}

function APSectionForm<V extends FieldValues>({ siteId, ap, formKey, values, schema, toBody, toValues, confirm, legacyWritable, children }: APSectionProps<V>) {
  const queryClient = useQueryClient()
  const context = useSiteFormContext(siteId, {
    expert: { has_patch: false, field: 'expert', shadowed: ap.config?.shadowed ?? [], partly_shadowed: [], overridden_on: {} },
    expertLink: { to: `/sites/${encodeURIComponent(siteId)}/aps/${encodeURIComponent(ap.id)}/config`, search: { view: 'expert' } },
  })
  const controller = useConfigForm<V, APSettingsView>({
    formKey: `ap:${ap.id}:${formKey}`,
    scope: 'ap',
    values,
    schema,
    siteId,
    ifMatch: ap.updated_rev,
    readOnly: context.readOnly && !legacyWritable,
    submit: (body, options) => patchAP(ap.id, body as APPatch, options),
    toBody: (patch, vals) => toBody(vals, patch),
    toValues: (object) => toValues(object),
    confirm,
    successMessage: () => `${ap.name || ap.id} saved`,
    onSaved: (response: MutationResponse<APSettingsView>) => {
      queryClient.setQueryData(keys.ap(ap.id), (old: APSettingsView | undefined) => ({ ...old, ...response.object }))
      void queryClient.invalidateQueries({ queryKey: keys.siteAPs(siteId) })
      void queryClient.invalidateQueries({ queryKey: keys.siteRF(siteId) })
    },
  })
  return (
    <ConfigForm controller={controller} context={context}>
      {children}
    </ConfigForm>
  )
}

// ---- General ----

type GeneralForm = { name: string; site_id: string; management_ip: string; secret: string }

function GeneralSection({ siteId, ap, site }: { siteId: string; ap: APSettingsView; site: SiteConfigView }) {
  const sites = useQuery(sitesQuery)
  const system = useQuery(systemMigrationQuery)
  const dnsMode = Boolean(system.data?.dns_mode || ap.dns_mode)
  const toValues = (a: APSettingsView): GeneralForm => ({ name: a.name, site_id: a.site_id, management_ip: a.pinned_ip ?? '', secret: '' })
  const values = toValues(ap)
  const schema = z.object({
    name: z.string().trim().min(1, { error: 'Enter a name.' }).max(64, { error: 'At most 64 characters.' }),
    site_id: z.string().min(1, { error: 'Choose a site.' }),
    management_ip: z
      .string()
      .trim()
      .refine((v) => !v || isIP(v), { error: 'Enter an IPv4 or IPv6 address, or leave it empty.' })
      .refine((v) => !dnsMode || Boolean(v), { error: 'Required while dismist serves AP DNS (DNS mode).' }),
    secret: z.string().refine((v) => !v || secretByteLength(v) === 32, { error: 'The secret must be exactly 32 bytes, as hex or base64.' }),
  })
  const target = (id: string) => sites.data?.find((s) => s.id === id)?.name ?? id
  return (
    <APSectionForm
      siteId={siteId}
      ap={ap}
      formKey="general"
      values={values}
      schema={schema}
      legacyWritable
      toValues={toValues}
      toBody={(v, patch) => {
        const body: APPatch = {}
        if ('name' in patch) body.name = v.name.trim()
        if ('site_id' in patch) body.site_id = v.site_id
        if ('management_ip' in patch) body.management_ip = v.management_ip.trim()
        if (v.secret) body.secret = v.secret.replace(/\s/g, '')
        return body
      }}
      confirm={(v, base) =>
        v.site_id !== base.site_id
          ? {
              title: `Move ${base.name || ap.id} to ${target(v.site_id)}?`,
              description: `${target(v.site_id)}’s WLANs and radio settings replace the current ones. ${ap.client_count} client${ap.client_count === 1 ? '' : 's'} on this AP will be disconnected.`,
              action: 'Move AP',
              destructive: true,
            }
          : null
      }
    >
      <Section id="general" title="General">
        <OptionField k="ap.name" footer={<HostnamePreview apId={ap.id} fromName={site.ap_defaults.hostname_from_name} />} />
        <OptionField k="ap.site_id" control="select" choices={(sites.data ?? []).map((s) => ({ value: s.id, label: s.name }))} />
        <PinField dnsMode={dnsMode} />
        <SecretField hasSecret={ap.has_secret} />
        <KV
          items={[
            { label: 'Model', value: ap.model || 'Unknown until first connection' },
            {
              label: 'Firmware',
              value: (
                <span className="flex flex-wrap items-center gap-1.5">
                  <span className="break-all">{ap.firmware || 'Unknown'}</span>
                  {ap.firmware ? <Chip tone={ap.firmware_tested ? 'good' : 'warn'}>{ap.firmware_tested ? 'Tested' : 'Untested firmware'}</Chip> : null}
                </span>
              ),
            },
            { label: 'Serial', value: ap.identity?.serial || '–' },
            { label: 'ID', value: <Identifier value={ap.id} /> },
            { label: 'LAN IP', value: ap.lan_ip ? <Identifier value={ap.lan_ip} /> : '–' },
            { label: 'Public address', value: ap.remote_address ? <Identifier value={ap.remote_address} /> : '–' },
          ]}
        />
        <p className="text-xs text-muted-foreground">Model, firmware and serial come from the AP and cannot be edited.</p>
      </Section>
    </APSectionForm>
  )
}

function HostnamePreview({ apId, fromName }: { apId: string; fromName: boolean }) {
  const { control } = useFormContext<GeneralForm>()
  const name = useWatch({ control, name: 'name' })
  return (
    <p className="text-sm text-muted-foreground">
      Hostname: <span className="font-mono text-foreground">{hostnamePreview(name, apId, fromName) || '–'}</span>
      {fromName ? '' : ' (the site sends the name unchanged; see AP defaults)'}
    </p>
  )
}

function PinField({ dnsMode }: { dnsMode: boolean }) {
  const { setValue, control } = useFormContext<GeneralForm>()
  const pin = useWatch({ control, name: 'management_ip' })
  return (
    <OptionField
      k="ap.management_ip"
      placeholder="Any address"
      help={dnsMode ? 'Required while dismist serves AP DNS (DNS mode).' : undefined}
      footer={
        pin ? (
          <Button variant="ghost" size="sm" className="justify-self-start" onClick={() => setValue('management_ip', '', { shouldDirty: true, shouldValidate: true })}>
            Clear pin
          </Button>
        ) : null
      }
    />
  )
}

function SecretField({ hasSecret }: { hasSecret: boolean }) {
  const state = useOptionState('ap.secret')
  const { control } = useFormContext<GeneralForm>()
  const { field, fieldState } = useController({ control, name: 'secret' })
  const [open, setOpen] = useState(false)
  const id = useId()
  // Collapse the paste box again once the form is reset (after a save).
  const [seen, setSeen] = useState(field.value)
  if (seen !== field.value) {
    setSeen(field.value)
    if (!field.value) setOpen(false)
  }
  return (
    <FieldShell id={id} label={state.meta.label} help={state.meta.help} error={fieldState.error?.message} dirty={Boolean(field.value)} labelAs={open ? 'label' : 'span'}>
      <p className="text-sm">{hasSecret ? 'A secret is stored. It is never shown.' : 'No secret yet: the AP cannot connect until one is uploaded.'}</p>
      {open ? (
        <div className="grid gap-2">
          <Textarea
            id={id}
            value={field.value}
            onChange={(e) => field.onChange(e.target.value)}
            onBlur={field.onBlur}
            placeholder="Paste the 32-byte secret as hex or base64"
            className="min-h-20 font-mono text-xs"
            autoCapitalize="none"
            autoCorrect="off"
            spellCheck={false}
            disabled={!state.editable}
            aria-invalid={fieldState.error ? true : undefined}
            aria-describedby={describedBy(id, state.meta.help, fieldState.error)}
          />
          <label className={buttonVariants({ variant: 'outline', size: 'sm', className: 'cursor-pointer justify-self-start' })}>
            <FileUp /> Upload secret.bin
            <input
              type="file"
              className="sr-only"
              accept=".bin,application/octet-stream"
              disabled={!state.editable}
              onChange={async (e) => {
                const file = e.target.files?.[0]
                if (file) field.onChange(bytesToBase64(await file.arrayBuffer()))
                e.target.value = ''
              }}
            />
          </label>
        </div>
      ) : (
        <Button variant="outline" size="sm" className="justify-self-start" disabled={!state.editable} onClick={() => setOpen(true)}>
          Replace secret…
        </Button>
      )}
    </FieldShell>
  )
}

// ---- LED ----

type LEDForm = { led: { override: boolean; value: number | string | null } }

function LEDSection({ siteId, ap, site }: { siteId: string; ap: APSettingsView; site: SiteConfigView }) {
  const toValues = (a: APSettingsView): LEDForm => ({ led: { override: a.overrides.led_brightness != null, value: a.overrides.led_brightness ?? site.ap_defaults.led_brightness } })
  const values = toValues(ap)
  const schema = z.object({
    led: z.object({ override: z.boolean(), value: z.unknown() }).superRefine((v, ctx) => {
      if (v.override && !(Number.isInteger(v.value) && (v.value as number) >= 0 && (v.value as number) <= 255)) ctx.addIssue({ code: 'custom', path: ['value'], message: 'Enter 0 to 255.' })
    }),
  })
  return (
    <APSectionForm
      siteId={siteId}
      ap={ap}
      formKey="led"
      values={values}
      schema={schema}
      toValues={toValues}
      toBody={(v) => ({ overrides: { led_brightness: v.led.override ? Number(v.led.value) : null } })}
    >
      <Section id="led" title="LED">
        <OverrideRow name="led.override" label="LED brightness" inherited={site.ap_defaults.led_brightness === 0 ? 'Off' : String(site.ap_defaults.led_brightness)} optionKey="ap.led_brightness">
          <LEDSlider />
        </OverrideRow>
        <Locate siteId={siteId} ap={ap} experimental={site.experimental} />
      </Section>
    </APSectionForm>
  )
}

function LEDSlider() {
  const { control } = useFormContext<LEDForm>()
  const { field } = useController({ control, name: 'led.value' })
  const id = useId()
  return <SliderInput id={id} value={field.value as number | null} onChange={field.onChange} min={0} max={255} label="LED brightness" offAtZero />
}

/** A site-inherited value with an Override switch (§3.6). */
function OverrideRow({ name, label, inherited, optionKey, children }: { name: string; label: string; inherited: string; optionKey: string; children: ReactNode }) {
  const state = useOptionState(optionKey, { value: null })
  const { control } = useFormContext()
  const { field } = useController({ control, name })
  const id = useId()
  return (
    <FieldShell id={id} label={label} labelAs="span" badges={state.badges} help={state.lockReason ?? undefined}>
      <div className="flex flex-wrap items-center gap-3">
        <SwitchRow reverse label={`Override for this AP`} checked={Boolean(field.value)} onCheckedChange={field.onChange} disabled={!state.editable} />
        <InheritedValue>{inherited}</InheritedValue>
      </div>
      {field.value ? children : null}
    </FieldShell>
  )
}

function Locate({ siteId, ap, experimental }: { siteId: string; ap: APSettingsView; experimental: boolean }) {
  const queryClient = useQueryClient()
  const [busy, setBusy] = useState(false)
  const [minutes, setMinutes] = useState('5')
  const until = ap.locate_until ? new Date(ap.locate_until) : null
  const [now, setNow] = useState(() => Date.now())
  const active = Boolean(until && until.getTime() > now)
  useEffect(() => {
    if (!active) return
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [active])
  if (!experimental && !active) return null
  const left = until ? Math.max(0, Math.round((until.getTime() - now) / 1000)) : 0

  async function run(start: boolean) {
    setBusy(true)
    try {
      const result = start ? await locateAP(ap.id, Number(minutes)) : await unlocateAP(ap.id)
      queryClient.setQueryData(keys.ap(ap.id), (old: APSettingsView | undefined) => (old ? { ...old, locate_until: start ? result.until ?? new Date(Date.now() + Number(minutes) * 60_000).toISOString() : null } : old))
      void queryClient.invalidateQueries({ queryKey: keys.siteChanges(siteId) })
      toast.success(start ? `Locating ${ap.name || ap.id}` : 'Stopped locating')
    } catch (e) {
      toast.error(start ? 'Could not start locate' : 'Could not stop locate', { description: describeError(e).message })
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="grid gap-2 rounded-lg border border-dashed p-3">
      <div className="flex flex-wrap items-center gap-2 text-sm font-medium">
        Locate <ExperimentalBadge />
      </div>
      <p className="text-sm text-muted-foreground">Blinks the LED (LED.Locator) so you can find the AP. Not yet verified on AP33 0.15; it is pushed as a config change.</p>
      {active ? (
        <Button variant="outline" className="justify-self-start" disabled={busy} onClick={() => void run(false)}>
          <LightbulbOff /> Stop locating ({Math.floor(left / 60)}:{String(left % 60).padStart(2, '0')})
        </Button>
      ) : (
        <div className="flex flex-wrap items-end gap-2">
          <label className="grid gap-1 text-sm">
            <span className="text-muted-foreground">For</span>
            <NativeSelect value={minutes} onChange={(e) => setMinutes(e.target.value)}>
              {['1', '5', '15', '60'].map((m) => (
                <option key={m} value={m}>
                  {m} min
                </option>
              ))}
            </NativeSelect>
          </label>
          <Button variant="outline" disabled={busy} onClick={() => void run(true)}>
            {busy ? <Loader2 className="motion-safe:animate-spin" /> : <Lightbulb />} Locate
          </Button>
        </div>
      )}
    </div>
  )
}

// ---- SSH ----

type SSHForm = { ssh: 'inherit' | 'on' | 'off' }

function SSHSection({ siteId, ap, site }: { siteId: string; ap: APSettingsView; site: SiteConfigView }) {
  const toValues = (a: APSettingsView): SSHForm => ({ ssh: a.overrides.ssh_enabled == null ? 'inherit' : a.overrides.ssh_enabled ? 'on' : 'off' })
  const values = toValues(ap)
  const keysCount = site.ssh.keys.length
  const schema = z.object({ ssh: z.enum(['inherit', 'on', 'off']) }).superRefine((v, ctx) => {
    if (v.ssh === 'on' && !keysCount) ctx.addIssue({ code: 'custom', path: ['ssh'], message: 'Add a root SSH key in the site’s Access settings first.' })
  })
  return (
    <APSectionForm
      siteId={siteId}
      ap={ap}
      formKey="ssh"
      values={values}
      schema={schema}
      toValues={toValues}
      toBody={(v) => ({ overrides: { ssh_enabled: v.ssh === 'inherit' ? null : v.ssh === 'on' } })}
      confirm={(v, base) => (v.ssh === 'off' && base.ssh !== 'off' ? { title: 'Turn off SSH on this AP?', description: 'The LAN-side recovery path (dismist ap33 ssh) stops working for this AP.', action: 'Turn off SSH', destructive: true } : null)}
    >
      <Section id="ssh" title="SSH" collapsible defaultOpen={false} summary={`SSH ${values.ssh === 'inherit' ? `inherited (${site.ssh.enabled ? 'on' : 'off'})` : values.ssh}`}>
        <OptionField
          k="ap.ssh_enabled"
          control="segmented"
          label="SSH access"
          choices={[
            { value: 'inherit', label: `Inherit (${site.ssh.enabled ? 'on' : 'off'})` },
            { value: 'on', label: 'On' },
            { value: 'off', label: 'Off' },
          ]}
          help={`Keys are set for the whole site (${keysCount} key${keysCount === 1 ? '' : 's'}) in Settings › Access.`}
        />
      </Section>
    </APSectionForm>
  )
}

// ---- Radios ----

type BandOverrideForm = {
  use_site: boolean
  enabled: 'inherit' | 'on' | 'off'
  channel_mode: 'auto' | 'static'
  channel: number | string | null
  width: number
  power_mode: 'inherit' | 'fixed'
  power: number | string | null
}
/** b24/b5, not "2.4": a dot in a key is a nested path to react-hook-form. */
type RadiosForm = { rf: { b24: BandOverrideForm; b5: BandOverrideForm } }
const FORM_BAND: Record<Band, 'b24' | 'b5'> = { '2.4': 'b24', '5': 'b5' }

function bandOverrideForm(ap: APSettingsView, band: Band): BandOverrideForm {
  const o = ap.overrides.rf?.[band]
  if (!o) return { use_site: true, enabled: 'inherit', channel_mode: 'auto', channel: null, width: 0, power_mode: 'inherit', power: null }
  return {
    use_site: false,
    enabled: o.disabled == null ? 'inherit' : o.disabled ? 'off' : 'on',
    channel_mode: o.channel ? 'static' : 'auto',
    channel: o.channel || null,
    width: o.width ?? 0,
    power_mode: o.power_dbm ? 'fixed' : 'inherit',
    power: o.power_dbm ?? null,
  }
}

function RadiosSection({ siteId, ap }: { siteId: string; ap: APSettingsView }) {
  const rf = useQuery(rfQuery(siteId))
  const toValues = (a: APSettingsView): RadiosForm => ({ rf: { b24: bandOverrideForm(a, '2.4'), b5: bandOverrideForm(a, '5') } })
  const values = toValues(ap)
  const profile = rf.data
  const schema = useMemo(
    () =>
      z.object({
        rf: z.record(z.string(), z.any()).superRefine((value, ctx) => {
          for (const band of ['2.4', '5'] as Band[]) {
            const key = FORM_BAND[band]
            const v = value[key] as BandOverrideForm
            if (v.use_site) continue
            const bandProfile = profile?.[BAND_KEY[band]]
            const width = v.width || bandProfile?.width || 20
            if (v.channel_mode === 'static') {
              if (!Number.isInteger(v.channel)) ctx.addIssue({ code: 'custom', path: [key, 'channel'], message: 'Choose a channel.' })
              else {
                const error = staticChannelError(band, v.channel as number, width, Boolean(bandProfile?.dfs), bandProfile?.valid_channels)
                if (error) ctx.addIssue({ code: 'custom', path: [key, 'channel'], message: error })
              }
            }
            if (v.power_mode === 'fixed' && !(Number.isInteger(v.power) && (v.power as number) >= 1 && (v.power as number) <= 25)) ctx.addIssue({ code: 'custom', path: [key, 'power'], message: 'Enter 1 to 25 dBm.' })
          }
        }),
      }),
    [profile],
  )
  const radioInput = (v: BandOverrideForm) =>
    v.use_site
      ? null
      : {
          disabled: v.enabled === 'inherit' ? null : v.enabled === 'off',
          channel: v.channel_mode === 'static' ? Number(v.channel) : null,
          width: v.width || null,
          power_dbm: v.power_mode === 'fixed' ? Number(v.power) : null,
        }
  return (
    <APSectionForm
      siteId={siteId}
      ap={ap}
      formKey="radios"
      values={values}
      schema={schema}
      toValues={toValues}
      toBody={(v, patch) => {
        const changed = (patch.rf ?? {}) as Record<string, unknown>
        const rfBody: Record<string, unknown> = {}
        for (const band of ['2.4', '5'] as Band[]) if (FORM_BAND[band] in changed) rfBody[band] = radioInput(v.rf[FORM_BAND[band]])
        return { overrides: { rf: rfBody } }
      }}
      confirm={(v, base) => {
        const off = (['2.4', '5'] as Band[]).filter((b) => v.rf[FORM_BAND[b]].enabled === 'off' && base.rf[FORM_BAND[b]].enabled !== 'off')
        return off.length
          ? { title: `Turn off the ${off.join(' and ')} GHz radio on ${ap.name || ap.id}?`, description: 'Clients on that band are disconnected from this AP.', action: 'Turn off and save', destructive: true }
          : null
      }}
    >
      <Section id="radios" title="Radios" description="Per-AP overrides of the site radio profile. Each change restarts that radio on this AP.">
        {!profile?.managed && profile ? (
          <Alert variant="warning">
            <TriangleAlert />
            <AlertTitle>RF is not managed for this site.</AlertTitle>
            <AlertDescription>Radio overrides apply once the site’s radio settings are managed by dismist.</AlertDescription>
          </Alert>
        ) : null}
        <div className="grid gap-4 lg:grid-cols-2">
          <BandOverrideCard band="2.4" ap={ap} profile={profile} />
          <BandOverrideCard band="5" ap={ap} profile={profile} />
        </div>
      </Section>
    </APSectionForm>
  )
}

function liveRadio(ap: APSettingsView, band: Band): RadioView | undefined {
  return ap.radios_detail?.find((r) => r.role === 'access' && r.band === band)
}

function BandOverrideCard({ band, ap, profile }: { band: Band; ap: APSettingsView; profile: RFProfileView | undefined }) {
  const { control, setValue, formState } = useFormContext<RadiosForm>()
  const v = useWatch({ control, name: `rf.${FORM_BAND[band]}` }) as BandOverrideForm
  const channelState = useOptionState('ap.rf.channel', { value: v.use_site ? null : v.channel })
  const bandProfile = profile?.[BAND_KEY[band]]
  const siteWidth = bandProfile?.width ?? (band === '2.4' ? 20 : 80)
  const width = v.width || siteWidth
  const plan = ap.rf_plan?.[band]
  const live = liveRadio(ap, band)
  const errors = (formState.errors.rf as Record<string, Record<string, { message?: string }>> | undefined)?.[FORM_BAND[band]]
  const editable = channelState.editable
  const set = <K extends keyof BandOverrideForm>(key: K, value: BandOverrideForm[K]) => setValue(`rf.${FORM_BAND[band]}.${key}` as never, value as never, { shouldDirty: true, shouldValidate: true })
  const choices = primaries(band, width, bandProfile?.valid_channels)
  const sitePower = bandProfile?.power.mode === 'fixed' ? `${bandProfile.power.dbm} dBm` : bandProfile ? `Auto ${bandProfile.power.min}–${bandProfile.power.max} dBm` : '–'
  const id = useId()

  return (
    <div className="grid content-start gap-4 rounded-lg border p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-sm font-semibold">{band} GHz</h3>
        <span className="text-xs text-muted-foreground">
          Now: {live ? `ch ${live.channel}/${live.width} · max ${live.max_power_dbm ?? '–'} dBm · ${live.clients} clients` : 'no telemetry'}
        </span>
      </div>
      <SwitchRow label="Use site profile" checked={v.use_site} disabled={!editable} onCheckedChange={(checked) => set('use_site', checked)} />
      {v.use_site ? (
        <p className="text-sm text-muted-foreground">
          {plan ? `Auto: channel ${plan.channel}/${plan.width} at ${plan.power_dbm} dBm (${(plan as { source?: string }).source ?? 'plan'}).` : `Site profile: ${siteWidth} MHz, ${sitePower}.`}
        </p>
      ) : (
        <>
          <div className="grid gap-1">
            <span id={`${id}-enabled`} className="text-sm font-medium">
              Enabled
            </span>
            <Segmented
              aria-labelledby={`${id}-enabled`}
              value={v.enabled}
              disabled={!editable}
              onValueChange={(x) => set('enabled', x as BandOverrideForm['enabled'])}
              options={[
                { value: 'inherit', label: `Inherit (${bandProfile?.enabled === false ? 'off' : 'on'})` },
                { value: 'on', label: 'On' },
                { value: 'off', label: 'Off' },
              ]}
            />
          </div>
          <div className="grid gap-2">
            <span id={`${id}-channel`} className="flex flex-wrap items-center gap-2 text-sm font-medium">
              Channel {channelState.badges}
            </span>
            <Segmented
              aria-labelledby={`${id}-channel`}
              value={v.channel_mode}
              disabled={!editable}
              onValueChange={(x) => set('channel_mode', x as 'auto' | 'static')}
              options={[
                { value: 'auto', label: 'Auto (planner)' },
                { value: 'static', label: 'Static' },
              ]}
            />
            {v.channel_mode === 'static' ? (
              <label className="grid gap-1 text-sm">
                <span className="text-muted-foreground">Static channel</span>
                <NativeSelect value={v.channel == null ? '' : String(v.channel)} disabled={!editable} onChange={(e) => set('channel', e.target.value ? Number(e.target.value) : null)} aria-invalid={errors?.channel ? true : undefined}>
                  <option value="">Choose…</option>
                  {choices.map((p) => (
                    <option key={p.channel} value={p.channel} disabled={p.dfs && !bandProfile?.dfs}>
                      {p.channel}
                      {p.dfs ? ' (DFS)' : ''}
                    </option>
                  ))}
                </NativeSelect>
              </label>
            ) : plan ? (
              <p className="text-sm text-muted-foreground">Assigned channel {plan.channel} ({(plan as { source?: string }).source ?? 'channel plan'}).</p>
            ) : null}
            {errors?.channel?.message ? <p role="alert" className="text-sm font-medium text-destructive">{errors.channel.message}</p> : null}
          </div>
          <label className="grid gap-1 text-sm">
            <span className="font-medium">Width</span>
            <NativeSelect value={String(v.width)} disabled={!editable} onChange={(e) => set('width', Number(e.target.value))}>
              <option value="0">Inherit ({siteWidth} MHz)</option>
              {WIDTHS[band].map((w) => (
                <option key={w} value={w}>
                  {w} MHz
                </option>
              ))}
            </NativeSelect>
            {band === '2.4' ? <span className="text-muted-foreground">40 MHz on 2.4 GHz is expert only.</span> : null}
          </label>
          <div className="grid gap-2">
            <span id={`${id}-power`} className="text-sm font-medium">
              Transmit power
            </span>
            <Segmented
              aria-labelledby={`${id}-power`}
              value={v.power_mode}
              disabled={!editable}
              onValueChange={(x) => set('power_mode', x as 'inherit' | 'fixed')}
              options={[
                { value: 'inherit', label: `Inherit (${sitePower})` },
                { value: 'fixed', label: 'Fixed' },
              ]}
            />
            {v.power_mode === 'fixed' ? <PowerInput band={band} disabled={!editable} error={errors?.power?.message} /> : null}
            {v.power_mode === 'fixed' && typeof v.power === 'number' && v.power > 20 ? (
              <p className="text-sm text-warning">Above 20 dBm is rarely useful indoors.</p>
            ) : null}
          </div>
        </>
      )}
    </div>
  )
}

function PowerInput({ band, disabled, error }: { band: Band; disabled: boolean; error?: string }) {
  const { control } = useFormContext<RadiosForm>()
  const { field } = useController({ control, name: `rf.${FORM_BAND[band]}.power` as never })
  const id = useId()
  return (
    <div className="grid gap-1">
      <label htmlFor={id} className="sr-only">
        {band} GHz power in dBm
      </label>
      <NumberInput id={id} name={field.name} inputRef={field.ref} value={field.value as number | null} onChange={field.onChange} onBlur={field.onBlur} min={1} max={25} unit="dBm" disabled={disabled} aria-invalid={error ? true : undefined} />
      {error ? <p role="alert" className="text-sm font-medium text-destructive">{error}</p> : null}
    </div>
  )
}

// ---- Diagnostics ----

type DiagnosticsForm = { features: Record<string, boolean> }

function DiagnosticsSection({ siteId, ap }: { siteId: string; ap: APSettingsView }) {
  const names = useMemo(() => [...new Set([...PROTOCOL_FEATURES.map((f) => f.name as string), ...Object.keys(ap.protocol_features ?? {})])], [ap.protocol_features])
  const toValues = (a: APSettingsView): DiagnosticsForm => ({ features: Object.fromEntries(names.map((n) => [n, Boolean(a.protocol_features?.[n])])) })
  const values = toValues(ap)
  const on = Object.values(values.features).filter(Boolean).length
  return (
    <APSectionForm
      siteId={siteId}
      ap={ap}
      formKey="diagnostics"
      values={values}
      schema={z.object({ features: z.record(z.string(), z.boolean()) })}
      toValues={toValues}
      legacyWritable
      toBody={(v, patch) => ({ protocol_features: Object.fromEntries(Object.keys((patch.features ?? {}) as object).map((n) => [n, v.features[n]])) })}
      confirm={(v, base) => {
        const enabling = names.filter((n) => v.features[n] && !base.features[n])
        return enabling.length
          ? {
              title: `Switch on ${enabling.length} protocol feature${enabling.length === 1 ? '' : 's'} for ${ap.name || ap.id}?`,
              description: 'dismist starts sending new frames to this AP on its next frame. Switch on one feature at a time and compare reconnects and the boot count with the baseline; switching it off again takes effect at once.',
              action: 'Switch on',
            }
          : null
      }}
    >
      <Section id="diagnostics" title="Diagnostics" collapsible defaultOpen={false} summary={`${on} protocol feature${on === 1 ? '' : 's'} on · push ${ap.connection?.push_mode ?? 'reconnect'}`}>
        <p className="text-sm text-muted-foreground">
          Replies to AP subjects dismist used to drop. Each is off until you switch it on for this AP, and switching it off needs no restart.
        </p>
        <ul className="grid gap-4">
          {names.map((name) => (
            <FeatureSwitch key={name} name={name} at={ap.protocol_features_at?.[name]} />
          ))}
        </ul>
        <div className="grid gap-1 text-sm">
          <span className="font-medium">ep-ip-register</span>
          <span className="text-muted-foreground">Always recorded (the tunnel IPv6 address); dismist sends nothing back, so there is no switch.</span>
        </div>
        <KV
          items={[
            { label: 'Config push', value: ap.connection?.push_mode === 'notify' ? 'Notify (ep-config-change)' : 'Reconnect' },
            { label: 'ep-config-change support', value: ap.capabilities?.config_change_notify == null ? 'Not learned yet' : ap.capabilities.config_change_notify ? 'Yes' : 'No' },
            { label: 'AP commands', value: ap.capabilities?.ap_cmd ? 'Verified for this firmware' : 'Unverified: reboot with dismist ap33 ssh' },
          ]}
        />
        <LocalFiles files={ap.local_files} />
      </Section>
    </APSectionForm>
  )
}

function FeatureSwitch({ name, at }: { name: string; at?: string }) {
  const meta = PROTOCOL_FEATURES.find((f) => f.name === name)
  const { control } = useFormContext<DiagnosticsForm>()
  const { field, fieldState } = useController({ control, name: `features.${name}` })
  const id = useId()
  return (
    <li>
      <FieldShell
        id={id}
        inline
        dirty={fieldState.isDirty}
        label={
          <>
            {meta?.label ?? name} <span className="font-mono text-xs font-normal text-muted-foreground">{meta?.subject ?? name}</span>
          </>
        }
        badges={meta?.confidence === 'needs-live-test' ? <ExperimentalBadge /> : null}
        help={
          <>
            {meta?.help ?? 'A protocol feature reported by the controller.'}
            {at ? (
              <>
                {' '}
                Changed <RelativeTime value={at} />.
              </>
            ) : null}
          </>
        }
      >
        <Switch id={id} checked={Boolean(field.value)} onCheckedChange={field.onChange} aria-describedby={`${id}-help`} />
      </FieldShell>
    </li>
  )
}

function LocalFiles({ files }: { files: APSettingsView['local_files'] }) {
  return (
    <div className="grid gap-1 text-sm">
      <span className="font-medium">Local config files on the AP</span>
      {files == null ? (
        <span className="text-muted-foreground">Never checked. Run dismist ap33 inspect from the LAN to record them.</span>
      ) : !files.length ? (
        <span className="text-muted-foreground">None (checked by the CLI).</span>
      ) : (
        <ul className="grid gap-1">
          {files.map((f) => (
            <li key={f.name} className="flex flex-wrap items-center gap-2">
              <span className="font-mono">{f.name}</span>
              <span className="font-mono text-xs text-muted-foreground">{f.sha256.slice(0, 16)}…</span>
              <RelativeTime value={f.seen_at} />
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

// ---- Management network (experimental, phase 3) ----

type ManagementForm = {
  management: {
    override: boolean
    ip_mode: 'dhcp' | 'static'
    address: string
    prefix: number | string | null
    gateway: string
    dns: string[]
    vlan: { mode: 'inherit' | 'untagged' | 'tagged'; tagged: boolean; id: number | string | null }
    ntp_inherit: boolean
    ntp: string[]
  }
}

function inSubnet(ip: string, gw: string, prefix: number) {
  const n = (s: string) => s.split('.').reduce((a, b) => (a << 8) + Number(b), 0) >>> 0
  const mask = prefix === 0 ? 0 : (0xffffffff << (32 - prefix)) >>> 0
  return (n(ip) & mask) === (n(gw) & mask)
}

function ManagementSection({ siteId, ap, site }: { siteId: string; ap: APSettingsView; site: SiteConfigView }) {
  const recovery = Boolean(site.ap_defaults.config_recovery?.enabled)
  const allowed = site.experimental && recovery
  // The stored override (model.ManagementOverride): static, address,
  // prefix, gateway, dns, vlan_id (absent inherits the site; 1 untagged;
  // 2 to 4094 tagged) and ntp (absent inherits).
  const current = ap.overrides.management as null | {
    static?: boolean
    address?: string
    prefix?: number
    gateway?: string
    dns?: string[]
    vlan_id?: number | null
    ntp?: string[] | null
  }
  const toValues = (a: APSettingsView): ManagementForm => {
    const m = a.overrides.management as typeof current
    const vlanMode = m?.vlan_id == null ? 'inherit' : m.vlan_id === 1 ? 'untagged' : 'tagged'
    return {
      management: {
        override: Boolean(m),
        ip_mode: m?.static ? 'static' : 'dhcp',
        address: m?.address ?? '',
        prefix: m?.prefix ?? null,
        gateway: m?.gateway ?? '',
        dns: m?.dns ?? [],
        vlan: { mode: vlanMode, tagged: vlanMode === 'tagged', id: vlanMode === 'tagged' ? (m?.vlan_id ?? null) : null },
        ntp_inherit: m?.ntp == null,
        ntp: m?.ntp ?? [],
      },
    }
  }
  const values = toValues(ap)
  if (!allowed && !current) return null
  const schema = z.object({
    management: z.any().superRefine((m: ManagementForm['management'], ctx) => {
      if (!m.override) return
      const add = (path: string[], message: string) => ctx.addIssue({ code: 'custom', path, message })
      if (m.ip_mode === 'static') {
        if (!isIPv4(m.address)) add(['address'], 'An IPv4 address.')
        if (!(Number.isInteger(m.prefix) && (m.prefix as number) >= 1 && (m.prefix as number) <= 30)) add(['prefix'], 'Prefix 1 to 30.')
        if (!isIPv4(m.gateway)) add(['gateway'], 'An IPv4 gateway.')
        else if (isIPv4(m.address) && Number.isInteger(m.prefix) && !inSubnet(m.address, m.gateway, m.prefix as number)) add(['gateway'], 'The gateway must be inside the subnet.')
        if (isIPv4(m.address) && Number.isInteger(m.prefix)) {
          const host = m.address.split('.').map(Number).reduce((a, b) => (a << 8) + b, 0) >>> 0
          const size = 2 ** (32 - (m.prefix as number))
          if (host % size === 0 || host % size === size - 1) add(['address'], 'That is the network or broadcast address.')
        }
        if (!m.dns.length) add(['dns'], 'At least one DNS server with a static address.')
      }
      m.dns.forEach((d, i) => !isIP(d.trim()) && add(['dns', String(i)], 'An IP address.'))
      if (m.vlan.tagged && !(Number.isInteger(m.vlan.id) && (m.vlan.id as number) >= 2 && (m.vlan.id as number) <= 4094)) add(['vlan', 'id'], 'VLAN 2 to 4094.')
      if (!m.ntp_inherit && m.ntp.length > 6) add(['ntp'], 'At most 6 NTP servers.')
      if (!m.ntp_inherit) m.ntp.forEach((s, i) => !isNTPServer(s.trim()) && add(['ntp', String(i)], 'A hostname or IP address.'))
    }),
  })
  return (
    <APSectionForm
      siteId={siteId}
      ap={ap}
      formKey="management"
      values={values}
      schema={schema}
      toValues={toValues}
      toBody={(v) => {
        const m = v.management
        return {
          overrides: {
            management: m.override
              ? {
                  static: m.ip_mode === 'static',
                  address: m.address.trim(),
                  prefix: m.prefix == null || m.prefix === '' ? 0 : Number(m.prefix),
                  gateway: m.gateway.trim(),
                  dns: m.dns.map((d) => d.trim()),
                  vlan_id: m.vlan.mode === 'inherit' ? null : m.vlan.tagged ? Number(m.vlan.id) : 1,
                  ntp: m.ntp_inherit ? null : m.ntp.map((s) => s.trim()),
                }
              : null,
          },
        }
      }}
      confirm={() => ({
        title: `Change the management network of ${ap.name || ap.id}?`,
        description: 'A wrong address, gateway or VLAN strands the AP: dismist cannot reach it to undo the change. Config auto-recovery should revert it if it loses the controller.',
        action: 'Apply',
        destructive: true,
      })}
    >
      <Section id="management" title="Management network" badges={<ExperimentalBadge />} collapsible defaultOpen={Boolean(current)} summary={current ? 'Overridden on this AP' : 'From DHCP'}>
        {!allowed ? (
          <Alert variant="warning">
            <TriangleAlert />
            <AlertTitle>Locked</AlertTitle>
            <AlertDescription>Needs experimental options and config auto-recovery on the site. The stored values are kept.</AlertDescription>
          </Alert>
        ) : null}
        <p className="text-sm text-muted-foreground">
          Current lease: {ap.ip?.address ? `${ap.ip.address}/${ap.ip.prefix} via ${ap.ip.gateway || '–'} (VLAN ${ap.ip.vlan || 'untagged'}, ${ap.ip.dhcp_state || 'unknown'})` : 'no telemetry yet'}
        </p>
        <ManagementFields disabled={!allowed} />
      </Section>
    </APSectionForm>
  )
}

function ManagementFields({ disabled }: { disabled: boolean }) {
  const { control, setValue, register, formState } = useFormContext<ManagementForm>()
  const m = useWatch({ control, name: 'management' })
  const err = (path: string) => path.split('.').reduce<unknown>((n, p) => (n as Record<string, unknown> | undefined)?.[p], formState.errors.management) as { message?: string } | undefined
  const set = (name: string, value: unknown) => setValue(`management.${name}` as never, value as never, { shouldDirty: true, shouldValidate: true })
  return (
    <div className={cn('grid gap-4', disabled && 'pointer-events-none opacity-60')} aria-disabled={disabled || undefined}>
      <SwitchRow label="Override the management network for this AP" checked={m.override} disabled={disabled} onCheckedChange={(c) => set('override', c)} />
      {m.override ? (
        <>
          <Segmented
            aria-label="IP mode"
            value={m.ip_mode}
            disabled={disabled}
            onValueChange={(x) => set('ip_mode', x)}
            options={[
              { value: 'dhcp', label: 'DHCP' },
              { value: 'static', label: 'Static' },
            ]}
          />
          {m.ip_mode === 'static' ? (
            <div className="grid gap-3 sm:grid-cols-3">
              <label className="grid gap-1 text-sm">
                <span className="text-muted-foreground">Address</span>
                <Input inputMode="decimal" {...register('management.address')} disabled={disabled} aria-invalid={err('address') ? true : undefined} />
                {err('address')?.message ? <span role="alert" className="text-destructive">{err('address')?.message}</span> : null}
              </label>
              <SmallNumber name="management.prefix" label="Prefix" min={1} max={30} disabled={disabled} error={err('prefix')?.message} />
              <label className="grid gap-1 text-sm">
                <span className="text-muted-foreground">Gateway</span>
                <Input inputMode="decimal" {...register('management.gateway')} disabled={disabled} aria-invalid={err('gateway') ? true : undefined} />
                {err('gateway')?.message ? <span role="alert" className="text-destructive">{err('gateway')?.message}</span> : null}
              </label>
            </div>
          ) : null}
          <ServerListField optionKey="ap.management" name="management.dns" label="DNS servers" help="At least one when the address is static." placeholder="192.0.2.53" inputMode="decimal" />
          <div className="grid gap-2">
            <span className="text-sm font-medium">Management VLAN</span>
            <Segmented
              aria-label="Management VLAN"
              value={m.vlan.mode === 'inherit' ? 'inherit' : m.vlan.tagged ? 'tagged' : 'untagged'}
              disabled={disabled}
              onValueChange={(x) => {
                set('vlan.mode', x)
                set('vlan.tagged', x === 'tagged')
              }}
              options={[
                { value: 'inherit', label: 'Inherit' },
                { value: 'untagged', label: 'Untagged' },
                { value: 'tagged', label: 'Tagged' },
              ]}
            />
            {m.vlan.tagged ? <SmallNumber name="management.vlan.id" label="VLAN ID" min={2} max={4094} disabled={disabled} error={err('vlan.id')?.message} /> : null}
          </div>
          <SwitchRow label="Use the site’s NTP servers" checked={m.ntp_inherit} disabled={disabled} onCheckedChange={(c) => set('ntp_inherit', c)} />
          {!m.ntp_inherit ? <ServerListField optionKey="ap.management" name="management.ntp" label="NTP servers" help="At most 6." placeholder="pool.ntp.org" /> : null}
        </>
      ) : null}
    </div>
  )
}

// ---- Ports (experimental, phase 3) ----

type PortsForm = { eth1: Eth1Form; wired_client_stats: 'inherit' | 'on' | 'off' }

function PortsSection({ siteId, ap, site }: { siteId: string; ap: APSettingsView; site: SiteConfigView }) {
  const toValues = (a: APSettingsView): PortsForm => ({
    eth1: eth1ToForm(a.overrides.eth1 as never, true),
    wired_client_stats: a.overrides.wired_client_stats == null ? 'inherit' : a.overrides.wired_client_stats ? 'on' : 'off',
  })
  const values = toValues(ap)
  if (!site.experimental && values.eth1.mode === 'inherit' && values.wired_client_stats === 'inherit') return null
  const schema = z.object({ eth1: z.any(), wired_client_stats: z.enum(['inherit', 'on', 'off']) }).superRefine((v, ctx) => {
    const e = eth1Error(v.eth1 as Eth1Form)
    if (e) ctx.addIssue({ code: 'custom', path: ['eth1', e.path], message: e.message })
  })
  return (
    <APSectionForm
      siteId={siteId}
      ap={ap}
      formKey="ports"
      values={values}
      schema={schema}
      toValues={toValues}
      toBody={(v, patch) => ({
        overrides: {
          ...('eth1' in patch ? { eth1: eth1FromForm(v.eth1) as never } : {}),
          ...('wired_client_stats' in patch ? { wired_client_stats: v.wired_client_stats === 'inherit' ? null : v.wired_client_stats === 'on' } : {}),
        },
      })}
    >
      <Section id="ports" title="Ports" badges={<ExperimentalBadge />} collapsible defaultOpen={false} summary={`eth1 ${values.eth1.mode.replaceAll('_', ' ')}`}>
        <Eth1Field optionKey="ap.eth1" name="eth1" allowInherit />
        <OptionField
          k="ap.wired_client_stats"
          control="segmented"
          choices={[
            { value: 'inherit', label: `Inherit (${site.ap_defaults.wired_client_stats ? 'on' : 'off'})` },
            { value: 'on', label: 'On' },
            { value: 'off', label: 'Off' },
          ]}
        />
      </Section>
    </APSectionForm>
  )
}
