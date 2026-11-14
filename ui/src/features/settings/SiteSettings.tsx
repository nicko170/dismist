import { useMemo, useState, type ReactNode } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useFormContext, useWatch, type FieldValues } from 'react-hook-form'
import { toast } from 'sonner'
import { z } from 'zod'
import { FlaskConical, Loader2, RotateCcw, Trash2 } from 'lucide-react'
import { describeError } from '@/api/errors'
import { keys } from '@/api/keys'
import { networksQuery } from '@/api/queries/networks'
import { recommendationsQuery } from '@/api/queries/recommendations'
import { deleteSite, patchSite, putSiteExpert, siteExpertQuery, siteQuery, type SiteConfigView, type SitePatch } from '@/api/queries/sites'
import type { MutationResponse } from '@/gen/api.gen'
import { ErrorState, SkeletonRows } from '@/components/app/states'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { KV } from '@/components/app/KV'
import { sitesQuery } from '@/features/shell/queries'
import { forgetLastSite } from '@/features/shell/site'
import { ConfigForm, impactLine } from './kit/ConfigForm'
import { EXPERIMENTAL_HELP, ExperimentalBadge } from './kit/badges'
import { ExpertPatchEditor } from './kit/ExpertPatchEditor'
import { OptionField, useOptionState } from './kit/fields'
import { getOption, hasOption, leafSchema } from './kit/options'
import { ConfirmDialog } from './kit/primitives'
import { Section } from './kit/Section'
import { useSiteFormContext, useTypedEditing } from './kit/site'
import { useConfigForm, type ConfirmRequest } from './kit/useConfigForm'
import { isHostname, isIP, isNTPServer } from './kit/validators'
import {
  eth1Error,
  eth1FromForm,
  eth1ToForm,
  Eth1Field,
  MACListField,
  ServerListField,
  SmallNumber,
  SSHKeysField,
  SyslogField,
  ToggleWithNumbers,
  type BlockedRow,
  type Eth1Form,
  type SSHKeyRow,
  type SyslogRow,
} from './fields'
import { RecommendationList } from './Recommendations'

// Site settings (design §3.11): each sub-page is one form with its own PATCH
// of only its fields.

// ---- shared section form ----

type SectionProps<V extends FieldValues> = {
  siteId: string
  site: SiteConfigView
  formKey: string
  values: V
  schema: z.ZodType
  toBody: (values: V, patch: Record<string, unknown>) => SitePatch
  toValues: (site: SiteConfigView) => V
  confirm?: (values: V, base: V) => ConfirmRequest | null
  /** True when the form holds fields that still write the legacy record in legacy mode. */
  legacyWritable?: boolean
  children: ReactNode
}

function SiteSectionForm<V extends FieldValues>({ siteId, site, formKey, values, schema, toBody, toValues, confirm, legacyWritable, children }: SectionProps<V>) {
  const queryClient = useQueryClient()
  const context = useSiteFormContext(siteId, {
    expert: site.expert,
    expertLink: { to: `/sites/${encodeURIComponent(siteId)}/settings/advanced` },
  })
  const controller = useConfigForm<V, SiteConfigView>({
    formKey: `site:${siteId}:${formKey}`,
    scope: 'site',
    values,
    schema,
    siteId,
    ifMatch: site.updated_rev,
    readOnly: context.readOnly && !legacyWritable,
    submit: (body, options) => patchSite(siteId, body as SitePatch, options),
    toBody: (patch, vals) => toBody(vals, patch),
    toValues,
    confirm,
    successMessage: () => 'Site settings saved',
    onSaved: (response: MutationResponse<SiteConfigView>) => {
      queryClient.setQueryData(keys.site(siteId), response.object)
      void queryClient.invalidateQueries({ queryKey: keys.sites })
      void queryClient.invalidateQueries({ queryKey: keys.siteRF(siteId) })
    },
  })
  return (
    <ConfigForm controller={controller} context={context}>
      {children}
    </ConfigForm>
  )
}

/** Loads the site and renders a section once it is there. */
export function SiteSettingsSection({ siteId, children }: { siteId: string; children: (site: SiteConfigView) => ReactNode }) {
  const site = useQuery(siteQuery(siteId))
  if (site.isError) return <ErrorState error={site.error} title="Could not load the site" onRetry={() => void site.refetch()} />
  if (!site.data) return <SkeletonRows rows={3} />
  return <>{children(site.data)}</>
}

/** Only the keys the patch touches, with the values' current content (arrays whole). */
function pick<T extends Record<string, unknown>>(patch: Record<string, unknown>, values: T, keysToSend: (keyof T)[]): Partial<T> {
  const out: Partial<T> = {}
  for (const key of keysToSend) if (key in patch) out[key] = values[key]
  return out
}

// ---- General ----

type GeneralForm = { name: string; country: string; timezone: string }

function timezones(): string[] {
  try {
    return (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf?.('timeZone') ?? []
  } catch {
    return []
  }
}

export function GeneralSettings({ siteId, site }: { siteId: string; site: SiteConfigView }) {
  const sites = useQuery(sitesQuery)
  const values = useMemo<GeneralForm>(() => ({ name: site.name, country: site.country, timezone: site.timezone }), [site])
  const others = (sites.data ?? []).filter((s) => s.id !== siteId).map((s) => s.name.toLowerCase())
  const schema = useMemo(
    () =>
      z.object({
        name: z
          .string()
          .trim()
          .min(1, { error: 'Enter a name.' })
          .max(64, { error: 'At most 64 characters.' })
          .refine((v) => !others.includes(v.toLowerCase()), { error: 'Another site already has this name.' }),
        country: z.string().min(1, { error: 'Choose a country.' }),
        timezone: z.string().min(1, { error: 'Choose a timezone.' }),
      }),
    [others.join('\u0000')],
  )
  const zones = useMemo(() => {
    const list = timezones()
    return list.includes(site.timezone) || !site.timezone ? list : [site.timezone, ...list]
  }, [site.timezone])
  const countries = getOption('site.country').enum.map((e) => ({ value: e.value, label: e.label }))
  if (site.country && !countries.some((c) => c.value === site.country)) countries.unshift({ value: site.country, label: `${site.country} (not verified on AP33)` })
  return (
    <SiteSectionForm
      siteId={siteId}
      site={site}
      formKey="general"
      values={values}
      schema={schema}
      legacyWritable
      toValues={(s) => ({ name: s.name, country: s.country, timezone: s.timezone })}
      toBody={(v, patch) => pick(patch, { ...v, name: v.name.trim() }, ['name', 'country', 'timezone'])}
      confirm={(v, base) =>
        v.country !== base.country
          ? {
              title: `Change the country to ${v.country}?`,
              description: `Every radio on every AP in ${site.name} restarts (${site.counts.aps} AP${site.counts.aps === 1 ? '' : 's'}), and the channels and power limits change to the new regulatory rules.`,
              action: 'Change country',
              destructive: true,
            }
          : null
      }
    >
      <Section id="general" title="General">
        <OptionField k="site.name" />
        <OptionField k="site.country" control="select" choices={countries} help={`${getOption('site.country').help} ccrev: ${site.ccrev ?? 'none'}.`} />
        <OptionField k="site.timezone" control="select" choices={zones.map((z) => ({ value: z, label: z.replaceAll('_', ' ') }))} />
      </Section>
    </SiteSectionForm>
  )
}

// ---- Access ----

type AccessForm = { ssh: { enabled: boolean; keys: SSHKeyRow[]; readonly_keys: SSHKeyRow[] }; syslog: SyslogRow[] }

export function AccessSettings({ siteId, site }: { siteId: string; site: SiteConfigView }) {
  const toValues = (s: SiteConfigView): AccessForm => ({
    ssh: { enabled: s.ssh.enabled, keys: s.ssh.keys.map((k) => ({ ...k })), readonly_keys: s.ssh.readonly_keys.map((k) => ({ ...k })) },
    syslog: s.syslog.map((t) => ({ ...t })),
  })
  const values = useMemo(() => toValues(site), [site])
  const schema = z
    .object({
      ssh: z.object({ enabled: z.boolean(), keys: z.array(z.any()).max(32, { error: 'At most 32 keys.' }), readonly_keys: z.array(z.any()).max(32, { error: 'At most 32 keys.' }) }),
      syslog: z.array(
        z.object({
          protocol: z.enum(['udp', 'tcp']),
          host: z.string().trim().refine((v) => isIP(v) || isHostname(v), { error: 'Enter an IP address or a DNS name.' }),
          port: z.unknown().refine((v) => Number.isInteger(v) && (v as number) >= 1 && (v as number) <= 65535, { error: 'Port 1 to 65535.' }),
          facility: z.unknown().refine((v) => Number.isInteger(v) && (v as number) >= 0 && (v as number) <= 23, { error: '0 to 23.' }),
          severity: z.unknown().refine((v) => Number.isInteger(v) && (v as number) >= 0 && (v as number) <= 7, { error: '0 to 7.' }),
          tag: z.string(),
        }),
      ),
    })
    .superRefine((v, ctx) => {
      if (v.ssh.enabled && !v.ssh.keys.length) ctx.addIssue({ code: 'custom', path: ['ssh', 'enabled'], message: 'SSH needs at least one root key.' })
    })
  return (
    <SiteSectionForm
      siteId={siteId}
      site={site}
      formKey="access"
      values={values}
      schema={schema}
      legacyWritable
      toValues={toValues}
      toBody={(v, patch) => {
        const body: SitePatch = {}
        if (patch.ssh) body.ssh = { enabled: v.ssh.enabled, keys: v.ssh.keys.map(({ key, comment }) => ({ key, comment })), readonly_keys: v.ssh.readonly_keys.map(({ key, comment }) => ({ key, comment })) }
        if (patch.syslog) body.syslog = v.syslog.map((t) => ({ ...t, host: t.host.trim(), port: Number(t.port), facility: Number(t.facility), severity: Number(t.severity) }))
        return body
      }}
      confirm={(v, base) =>
        base.ssh.enabled && !v.ssh.enabled
          ? { title: 'Turn off SSH on every AP?', description: 'The LAN-side recovery path (dismist ap33 ssh) stops working until SSH is turned back on.', action: 'Turn off SSH', destructive: true }
          : null
      }
    >
      <Section id="ssh" title="SSH">
        <OptionField k="site.ssh.enabled" />
        <SSHKeysField optionKey="site.ssh.keys" name="ssh.keys" otherList="ssh.readonly_keys" />
        <SSHKeysField optionKey="site.ssh.readonly_keys" name="ssh.readonly_keys" otherList="ssh.keys" />
      </Section>
      <SyslogSection />
    </SiteSectionForm>
  )
}

function SyslogSection() {
  const state = useOptionState('site.syslog', { value: null })
  const { control } = useFormContext<AccessForm>()
  const rows = useWatch({ control, name: 'syslog' })
  if (state.hidden && !rows.length) return null
  return (
    <Section id="syslog" title="Remote syslog" badges={<ExperimentalBadge />}>
      <SyslogField />
    </Section>
  )
}

// ---- Network ----

type NetworkForm = { management: { ntp: string[]; dns: string[]; vlan: { tagged: boolean; id: number | string | null } } }

export function NetworkSettings({ siteId, site }: { siteId: string; site: SiteConfigView }) {
  const toValues = (s: SiteConfigView): NetworkForm => ({
    management: { ntp: [...s.management.ntp], dns: [...s.management.dns], vlan: { tagged: s.management.vlan_id > 1, id: s.management.vlan_id > 1 ? s.management.vlan_id : null } },
  })
  const values = useMemo(() => toValues(site), [site])
  const recovery = Boolean(site.ap_defaults.config_recovery?.enabled)
  const schema = z.object({
    management: z
      .object({
        ntp: z.array(z.string().trim().refine(isNTPServer, { error: 'A hostname or IP address (not broadcast or multicast).' })).max(6, { error: 'At most 6 NTP servers.' }),
        dns: z.array(z.string().trim().refine(isIP, { error: 'An IP address.' })).max(3, { error: 'At most 3 DNS servers.' }),
        vlan: z.object({ tagged: z.boolean(), id: z.unknown() }),
      })
      .superRefine((v, ctx) => {
        if (v.vlan.tagged && !(Number.isInteger(v.vlan.id) && (v.vlan.id as number) >= 2 && (v.vlan.id as number) <= 4094)) ctx.addIssue({ code: 'custom', path: ['vlan', 'id'], message: 'Enter a VLAN from 2 to 4094.' })
        if (v.vlan.tagged && !recovery) ctx.addIssue({ code: 'custom', path: ['vlan', 'id'], message: 'Turn on config auto-recovery in AP defaults first.' })
      }),
  })
  return (
    <SiteSectionForm
      siteId={siteId}
      site={site}
      formKey="network"
      values={values}
      schema={schema}
      toValues={toValues}
      toBody={(v) => ({ management: { ntp: v.management.ntp.map((s) => s.trim()), dns: v.management.dns.map((s) => s.trim()), vlan_id: v.management.vlan.tagged ? Number(v.management.vlan.id) : 1 } })}
      confirm={(v, base) =>
        v.management.vlan.tagged !== base.management.vlan.tagged || v.management.vlan.id !== base.management.vlan.id
          ? {
              title: 'Change the management VLAN on every AP?',
              description: 'A wrong VLAN strands every AP: dismist cannot reach them to undo it. Config auto-recovery should revert an AP that loses the controller.',
              action: 'Change management VLAN',
              destructive: true,
            }
          : null
      }
    >
      <Section id="management" title="Management defaults">
        <ServerListField optionKey="site.management.ntp" name="management.ntp" placeholder="pool.ntp.org" />
        <ServerListField optionKey="site.management.dns" name="management.dns" placeholder="192.0.2.53" inputMode="decimal" warning="Overrides DNS from DHCP on every AP." />
        <ManagementVlan recovery={recovery} />
      </Section>
    </SiteSectionForm>
  )
}

function ManagementVlan({ recovery }: { recovery: boolean }) {
  const { control } = useFormContext<NetworkForm>()
  const vlan = useWatch({ control, name: 'management.vlan' })
  const state = useOptionState('site.management.vlan_id', { value: vlan.tagged ? vlan.id : null })
  if (state.hidden) return null
  return <VlanToggle namePrefix="management.vlan" optionKey="site.management.vlan_id" disabled={!recovery} help={recovery ? state.meta.help : 'Turn on config auto-recovery in AP defaults first: a wrong VLAN strands every AP.'} />
}

/** Untagged · Tagged N, bound to {tagged, id}. */
export function VlanToggle({ namePrefix, optionKey, disabled, help }: { namePrefix: string; optionKey: string; disabled?: boolean; help?: string }) {
  const { control, setValue, formState } = useFormContext()
  const vlan = useWatch({ control, name: namePrefix }) as { tagged: boolean; id: unknown }
  const state = useOptionState(optionKey, { value: vlan.tagged ? vlan.id : null, disabled })
  const err = (formState.errors as Record<string, unknown>)
  const message = namePrefix.split('.').concat('id').reduce<unknown>((n, p) => (n as Record<string, unknown> | undefined)?.[p], err) as { message?: string } | undefined
  return (
    <fieldset className="grid gap-2 border-0 p-0">
      <legend className="float-left mb-1 flex w-full flex-wrap items-center gap-2 text-sm font-medium">
        {state.meta.label} {state.badges}
      </legend>
      <div className="flex flex-wrap items-center gap-3">
        <label className="flex min-h-11 items-center gap-2 text-sm">
          <input type="radio" className="size-5 accent-brand" checked={!vlan.tagged} disabled={!state.editable} onChange={() => setValue(`${namePrefix}.tagged`, false, { shouldDirty: true, shouldValidate: true })} />
          Untagged
        </label>
        <label className="flex min-h-11 items-center gap-2 text-sm">
          <input type="radio" className="size-5 accent-brand" checked={vlan.tagged} disabled={!state.editable} onChange={() => setValue(`${namePrefix}.tagged`, true, { shouldDirty: true, shouldValidate: true })} />
          Tagged
        </label>
        {vlan.tagged ? <SmallNumber name={`${namePrefix}.id`} label="VLAN ID" min={2} max={4094} disabled={!state.editable} error={message?.message} /> : null}
      </div>
      {help || state.lockReason ? <p className="text-sm text-muted-foreground">{state.lockReason ?? help}</p> : null}
    </fieldset>
  )
}

// ---- AP defaults ----

type APDefaultsForm = {
  ap_defaults: {
    led_brightness: number | string | null
    hostname_from_name: boolean
    keep_wlans_up_on_uplink_down: boolean
    poe_constrained: 'keep_radios' | 'disable_radios'
    wired_client_stats: boolean
    eth1: Eth1Form
    config_recovery: { enabled: boolean; minutes: number | string | null }
    ble_enabled: boolean
  }
}

export function APDefaultsSettings({ siteId, site }: { siteId: string; site: SiteConfigView }) {
  const toValues = (s: SiteConfigView): APDefaultsForm => ({
    ap_defaults: {
      led_brightness: s.ap_defaults.led_brightness,
      hostname_from_name: s.ap_defaults.hostname_from_name,
      keep_wlans_up_on_uplink_down: s.ap_defaults.keep_wlans_up_on_uplink_down,
      poe_constrained: s.ap_defaults.poe_constrained,
      wired_client_stats: s.ap_defaults.wired_client_stats,
      eth1: eth1ToForm(s.ap_defaults.eth1, false),
      config_recovery: { enabled: Boolean(s.ap_defaults.config_recovery?.enabled), minutes: s.ap_defaults.config_recovery?.minutes ?? 5 },
      ble_enabled: !s.ap_defaults.ble_disabled,
    },
  })
  const values = useMemo(() => toValues(site), [site])
  const schema = z.object({
    ap_defaults: z
      .object({
        led_brightness: leafSchema('site.ap_defaults.led_brightness'),
        hostname_from_name: z.boolean(),
        keep_wlans_up_on_uplink_down: z.boolean(),
        poe_constrained: leafSchema('site.ap_defaults.poe_constrained'),
        wired_client_stats: z.boolean(),
        eth1: z.any(),
        config_recovery: z.object({ enabled: z.boolean(), minutes: z.unknown() }),
        ble_enabled: z.boolean(),
      })
      .superRefine((v, ctx) => {
        const e = eth1Error(v.eth1 as Eth1Form)
        if (e) ctx.addIssue({ code: 'custom', path: ['eth1', e.path], message: e.message })
        const m = v.config_recovery.minutes
        if (v.config_recovery.enabled && !(Number.isInteger(m) && (m as number) >= 1 && (m as number) <= 60)) ctx.addIssue({ code: 'custom', path: ['config_recovery', 'minutes'], message: '1 to 60 minutes.' })
      }),
  })
  return (
    <SiteSectionForm
      siteId={siteId}
      site={site}
      formKey="ap-defaults"
      values={values}
      schema={schema}
      toValues={toValues}
      toBody={(v, patch) => {
        const changed = (patch.ap_defaults ?? {}) as Record<string, unknown>
        const d = v.ap_defaults
        const out: Record<string, unknown> = {}
        for (const key of Object.keys(changed)) {
          if (key === 'eth1') out.eth1 = eth1FromForm(d.eth1)
          else if (key === 'config_recovery') out.config_recovery = d.config_recovery.enabled ? { enabled: true, minutes: Number(d.config_recovery.minutes) } : null
          else if (key === 'ble_enabled') out.ble_disabled = !d.ble_enabled
          else if (key === 'led_brightness') out.led_brightness = Number(d.led_brightness)
          else out[key] = d[key as keyof typeof d]
        }
        return { ap_defaults: out }
      }}
    >
      <Section id="ap-defaults" title="AP defaults" description="Every AP in the site uses these unless its own settings override them.">
        <OptionField k="site.ap_defaults.led_brightness" />
        <OptionField k="site.ap_defaults.hostname_from_name" />
        <OptionField k="site.ap_defaults.keep_wlans_up_on_uplink_down" />
        <OptionField k="site.ap_defaults.poe_constrained" />
        <OptionField k="site.ap_defaults.wired_client_stats" />
        <OptionField k="site.ap_defaults.ble_enabled" />
        <Eth1Field optionKey="site.ap_defaults.eth1" name="ap_defaults.eth1" />
        <ToggleWithNumbers
          optionKey="site.ap_defaults.config_recovery"
          enabledName="ap_defaults.config_recovery.enabled"
          label="Revert to the last good config when the controller is lost"
          numbers={[{ name: 'ap_defaults.config_recovery.minutes', label: 'After', min: 1, max: 60, unit: 'min' }]}
        />
      </Section>
    </SiteSectionForm>
  )
}

// ---- Protection ----

type ProtectionForm = {
  protection: {
    blocked_clients: BlockedRow[]
    proxy_arp: boolean
    arp_spoof_check: boolean
    penalty_box: { enabled: boolean; threshold: number | string | null; window_sec: number | string | null }
    dhcp_fingerprinting: boolean
  }
}

export function ProtectionSettings({ siteId, site }: { siteId: string; site: SiteConfigView }) {
  const toValues = (s: SiteConfigView): ProtectionForm => ({
    protection: {
      blocked_clients: s.protection.blocked_clients.map((b) => ({ ...b })),
      proxy_arp: s.protection.proxy_arp,
      arp_spoof_check: s.protection.arp_spoof_check,
      penalty_box: { enabled: Boolean(s.protection.penalty_box), threshold: s.protection.penalty_box?.threshold ?? 5, window_sec: s.protection.penalty_box?.window_sec ?? 60 },
      dhcp_fingerprinting: s.protection.dhcp_fingerprinting,
    },
  })
  const values = useMemo(() => toValues(site), [site])
  const schema = z.object({
    protection: z
      .object({
        blocked_clients: z.array(z.object({ mac: z.string(), note: z.string().max(64, { error: 'At most 64 characters.' }) })),
        proxy_arp: z.boolean(),
        arp_spoof_check: z.boolean(),
        penalty_box: z.object({ enabled: z.boolean(), threshold: z.unknown(), window_sec: z.unknown() }),
        dhcp_fingerprinting: z.boolean(),
      })
      .superRefine((v, ctx) => {
        if (v.arp_spoof_check && !v.proxy_arp) ctx.addIssue({ code: 'custom', path: ['arp_spoof_check'], message: 'The ARP spoof check needs proxy ARP.' })
        const pb = v.penalty_box
        if (pb.enabled) {
          if (!(Number.isInteger(pb.threshold) && (pb.threshold as number) >= 1 && (pb.threshold as number) <= 100)) ctx.addIssue({ code: 'custom', path: ['penalty_box', 'threshold'], message: '1 to 100 failures.' })
          if (!(Number.isInteger(pb.window_sec) && (pb.window_sec as number) >= 10 && (pb.window_sec as number) <= 3600)) ctx.addIssue({ code: 'custom', path: ['penalty_box', 'window_sec'], message: '10 to 3600 seconds.' })
        }
      }),
  })
  return (
    <SiteSectionForm
      siteId={siteId}
      site={site}
      formKey="protection"
      values={values}
      schema={schema}
      toValues={toValues}
      toBody={(v, patch) => {
        const changed = (patch.protection ?? {}) as Record<string, unknown>
        const p = v.protection
        const out: Record<string, unknown> = {}
        for (const key of Object.keys(changed)) {
          if (key === 'penalty_box') out.penalty_box = p.penalty_box.enabled ? { threshold: Number(p.penalty_box.threshold), window_sec: Number(p.penalty_box.window_sec) } : null
          else out[key] = p[key as keyof typeof p]
        }
        return { protection: out }
      }}
    >
      <Section id="protection" title="Protection">
        <MACListField />
        <OptionField k="site.protection.proxy_arp" />
        <ArpSpoof />
        <ToggleWithNumbers
          optionKey="site.protection.penalty_box"
          enabledName="protection.penalty_box.enabled"
          label="Block a client after repeated authentication failures"
          numbers={[
            { name: 'protection.penalty_box.threshold', label: 'Failures', min: 1, max: 100 },
            { name: 'protection.penalty_box.window_sec', label: 'Within', min: 10, max: 3600, unit: 's' },
          ]}
        />
        <OptionField k="site.protection.dhcp_fingerprinting" />
      </Section>
    </SiteSectionForm>
  )
}

function ArpSpoof() {
  const { control } = useFormContext<ProtectionForm>()
  const proxy = useWatch({ control, name: 'protection.proxy_arp' })
  return <OptionField k="site.protection.arp_spoof_check" disabled={!proxy} />
}

// ---- Advanced ----

export function AdvancedSettings({ siteId, site }: { siteId: string; site: SiteConfigView }) {
  const { typed } = useTypedEditing()
  const expert = useQuery(siteExpertQuery(siteId))
  const recommendations = useQuery(recommendationsQuery(siteId))
  const queryClient = useQueryClient()
  return (
    <div className="grid gap-5">
      <ExperimentalToggle siteId={siteId} site={site} />
      <section aria-labelledby="site-expert" className="grid gap-3 rounded-xl border bg-card px-4 py-4 md:px-5">
        {expert.isError ? (
          <>
            <h2 id="site-expert" className="text-base font-semibold">Expert patch for site {site.name}</h2>
            <ErrorState error={expert.error} title="Could not load the expert patch" onRetry={() => void expert.refetch()} />
          </>
        ) : !expert.data ? (
          <SkeletonRows rows={2} />
        ) : (
          <ExpertPatchEditor
            scope="site"
            heading={`Expert patch for site ${site.name}`}
            view={expert.data}
            siteId={siteId}
            legacy={!typed}
            save={(patch, options) => putSiteExpert(siteId, { patch }, options)}
            onSaved={(response) => {
              queryClient.setQueryData(siteExpertQuery(siteId).queryKey, response.object)
              void queryClient.invalidateQueries({ queryKey: keys.site(siteId), exact: true })
            }}
          />
        )}
      </section>
      <section aria-labelledby="site-recs" className="grid gap-3">
        <h2 id="site-recs" className="text-lg font-semibold">
          Recommendations
        </h2>
        {recommendations.isError ? (
          <ErrorState error={recommendations.error} title="Could not load recommendations" onRetry={() => void recommendations.refetch()} />
        ) : !recommendations.data ? (
          <SkeletonRows rows={2} />
        ) : (
          <RecommendationList siteId={siteId} items={recommendations.data} readOnly={!typed} />
        )}
      </section>
      <section aria-labelledby="telemetry-intervals" className="grid gap-3 rounded-xl border bg-card px-4 py-4 md:px-5">
        <h2 id="telemetry-intervals" className="text-base font-semibold">
          Telemetry intervals
        </h2>
        <p className="text-sm text-muted-foreground">Fixed: dismist depends on them for live status and statistics.</p>
        <KV
          items={[
            { label: 'Status', value: 'every 2 s' },
            { label: 'Full statistics', value: 'every 60 s' },
            { label: 'Memory', value: 'every 600 s' },
          ]}
        />
      </section>
      <RoamingKey siteId={siteId} site={site} />
    </div>
  )
}

function ExperimentalToggle({ siteId, site }: { siteId: string; site: SiteConfigView }) {
  const values = useMemo(() => ({ experimental: site.experimental }), [site.experimental])
  const inUse = site.experimental_in_use ?? []
  return (
    <SiteSectionForm
      siteId={siteId}
      site={site}
      formKey="experimental"
      values={values}
      schema={z.object({ experimental: z.boolean() })}
      toValues={(s) => ({ experimental: s.experimental })}
      toBody={(v) => ({ experimental: v.experimental })}
      confirm={(v) =>
        v.experimental
          ? { title: 'Allow experimental options on this site?', description: `${EXPERIMENTAL_HELP} After each push with an experimental value, dismist checks what the AP applied and offers a one-click revert.`, action: 'Allow experimental options' }
          : null
      }
    >
      <Section id="experimental" title="Experimental options" badges={<FlaskConical aria-hidden className="size-4 text-info" />}>
        <OptionField k="site.experimental" label="Allow experimental options" />
        <div className="grid gap-1 text-sm">
          <span className="font-medium">Experimental values in use</span>
          {inUse.length ? (
            <ul className="flex flex-wrap gap-2">
              {inUse.map((key) => (
                <li key={key}>
                  <ExperimentalBadge label={hasOption(key.split('=')[0]) ? getOption(key.split('=')[0]).label : key} />
                </li>
              ))}
            </ul>
          ) : (
            <span className="text-muted-foreground">None. Turning the switch off hides experimental controls again; stored values are kept.</span>
          )}
        </div>
      </Section>
    </SiteSectionForm>
  )
}

function RoamingKey({ siteId, site }: { siteId: string; site: SiteConfigView }) {
  const networks = useQuery(networksQuery(siteId))
  const users = (networks.data ?? []).filter((n) => n.roaming?.ap_key_cache)
  const [open, setOpen] = useState(false)
  const [preview, setPreview] = useState<MutationResponse<SiteConfigView> | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const queryClient = useQueryClient()
  if (!users.length && !site.roaming?.in_use) return null
  const clients = users.reduce((sum, n) => sum + n.live.clients, 0)

  async function start() {
    setOpen(true)
    setError(null)
    try {
      setPreview(await patchSite(siteId, { roaming: { rotate_key: true } }, { dryRun: true, ifMatch: site.updated_rev }))
    } catch (e) {
      setError(describeError(e).message)
    }
  }
  async function rotate() {
    setBusy(true)
    try {
      const response = await patchSite(siteId, { roaming: { rotate_key: true } }, { ifMatch: site.updated_rev })
      queryClient.setQueryData(keys.site(siteId), response.object)
      toast.success('Roaming key rotated', { description: impactLine(response.impact) })
      setOpen(false)
    } catch (e) {
      setError(describeError(e).message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <section aria-labelledby="roaming-key" className="grid gap-3 rounded-xl border bg-card px-4 py-4 md:px-5">
      <h2 id="roaming-key" className="text-base font-semibold">
        Site roaming key
      </h2>
      <p className="text-sm text-muted-foreground">
        AP-to-AP key caching uses one secret key for the whole site ({users.map((n) => n.ssid).join(', ') || 'no WLAN'}). It is never shown.
      </p>
      <Button variant="outline" className="justify-self-start" onClick={() => void start()}>
        <RotateCcw /> Rotate roaming key
      </Button>
      <ConfirmDialog
        open={open}
        onOpenChange={setOpen}
        title="Rotate the roaming key?"
        description={`Every WLAN that uses it restarts; about ${clients} client${clients === 1 ? '' : 's'} reconnect. ${preview ? impactLine(preview.impact) : ''}`}
        action="Rotate key"
        destructive
        pending={busy || (!preview && !error)}
        onConfirm={() => void rotate()}
      >
        {error ? (
          <p role="alert" className="text-sm font-medium text-destructive">
            {error}
          </p>
        ) : null}
      </ConfirmDialog>
    </section>
  )
}

// ---- Danger ----

export function DangerSettings({ siteId, site }: { siteId: string; site: SiteConfigView }) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const aps = site.counts.aps

  async function remove() {
    setBusy(true)
    setError(null)
    try {
      await deleteSite(siteId, { ifMatch: site.updated_rev })
      forgetLastSite(siteId)
      toast.success(`Site ${site.name} deleted`)
      setOpen(false)
      queryClient.removeQueries({ queryKey: keys.site(siteId) })
      await queryClient.invalidateQueries({ queryKey: keys.sites })
      void navigate({ to: '/sites' as never, replace: true })
    } catch (e) {
      setError(describeError(e).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <section aria-labelledby="danger-heading" className="grid gap-3 rounded-xl border border-destructive/40 bg-card px-4 py-4 md:px-5">
      <h2 id="danger-heading" className="text-base font-semibold">
        Delete site
      </h2>
      <p className="text-sm text-muted-foreground">Its WLANs, radio profile and change history are deleted with it.</p>
      {aps > 0 ? (
        <Alert variant="warning">
          <AlertTitle>
            Move or delete its {aps} AP{aps === 1 ? '' : 's'} first.
          </AlertTitle>
          <AlertDescription>A site with APs cannot be deleted, so no AP is ever left without a config.</AlertDescription>
        </Alert>
      ) : null}
      <Button variant="destructive" className="justify-self-start" disabled={aps > 0} onClick={() => setOpen(true)}>
        <Trash2 /> Delete {site.name}
      </Button>
      <ConfirmDialog
        open={open}
        onOpenChange={setOpen}
        title={`Delete site ${site.name}?`}
        description="This cannot be undone."
        action="Delete site"
        destructive
        pending={busy}
        confirmText={site.name}
        onConfirm={() => void remove()}
      >
        {error ? (
          <p role="alert" className="text-sm font-medium text-destructive">
            {error}
          </p>
        ) : null}
        {busy ? <Loader2 aria-label="Deleting" className="size-4 motion-safe:animate-spin" /> : null}
      </ConfirmDialog>
    </section>
  )
}
