import { useEffect, useId, useMemo, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useController, useFormContext, useWatch } from 'react-hook-form'
import { toast } from 'sonner'
import { AudioWaveform, Loader2, Radar, Sparkles, TriangleAlert } from 'lucide-react'
import { describeError } from '@/api/errors'
import { keys } from '@/api/keys'
import { putRF, rfQuery, type PlanRow, type RFProfileView } from '@/api/queries/rf'
import { siteQuery } from '@/api/queries/sites'
import type { Band, MutationResponse } from '@/gen/api.gen'
import { PageHeader } from '@/components/app/PageHeader'
import { ErrorState, SkeletonRows } from '@/components/app/states'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button, buttonVariants } from '@/components/ui/button'
import { RecommendationList } from '@/features/settings/Recommendations'
import { ConfigForm, ImpactDiff, impactLine } from '@/features/settings/kit/ConfigForm'
import { ChipGroup, describedBy, FieldShell, NumberInput, OptionField, useOptionState } from '@/features/settings/kit/fields'
import { getOption } from '@/features/settings/kit/options'
import { UnsavedChangesGuard } from '@/features/settings/kit/guard'
import { Dialog, Segmented } from '@/features/settings/kit/primitives'
import { Section } from '@/features/settings/kit/Section'
import { LegacyModeBanner, useSiteFormContext } from '@/features/settings/kit/site'
import { useConfigForm } from '@/features/settings/kit/useConfigForm'
import { cn } from '@/lib/utils'
import { channelChips, fallbackChoices, POWER_WARN, remapChannels, WIDTHS } from './channels'
import { BAND_KEY, rfSchema, toRFForm, toRFInput, type RFForm } from './form'
import { PlanDialog, PlanTable } from './Plan'

// Site RF profile and channel plan (design §3.10).

export function RFPage({ siteId }: { siteId: string }) {
  const rf = useQuery(rfQuery(siteId))
  return (
    <div className="grid gap-5">
      <PageHeader
        title="Radio settings"
        actions={
          <Link to={`/sites/${encodeURIComponent(siteId)}/rf/neighbours` as never} className={buttonVariants({ variant: 'outline' })}>
            <Radar /> Neighbours
          </Link>
        }
      />
      <LegacyModeBanner />
      {rf.isError ? (
        <ErrorState error={rf.error} title="Could not load the radio settings" onRetry={() => void rf.refetch()} />
      ) : !rf.data ? (
        <SkeletonRows rows={4} />
      ) : (
        <RFContent siteId={siteId} view={rf.data} />
      )}
      <UnsavedChangesGuard />
    </div>
  )
}

function RFContent({ siteId, view }: { siteId: string; view: RFProfileView }) {
  const queryClient = useQueryClient()
  const site = useQuery(siteQuery(siteId))
  const context = useSiteFormContext(siteId, {
    expert: site.data?.expert ?? null,
    expertLink: { to: `/sites/${encodeURIComponent(siteId)}/settings/advanced`, search: undefined },
  })
  const values = useMemo(() => toRFForm(view), [view])
  const schema = useMemo(() => rfSchema(view), [view])
  const [planOpen, setPlanOpen] = useState(false)
  const controller = useConfigForm<RFForm, RFProfileView>({
    formKey: `rf:${siteId}`,
    scope: 'site.rf',
    values,
    schema,
    siteId,
    ifMatch: view.updated_rev,
    readOnly: context.readOnly,
    submit: (body, options) => putRF(siteId, body as never, options),
    toBody: (_patch, vals) => toRFInput(vals, view),
    toValues: toRFForm,
    successMessage: () => 'Radio settings saved',
    confirm: (vals, base) => {
      const off = (['2.4', '5'] as Band[]).filter((b) => base[BAND_KEY[b]].enabled && !vals[BAND_KEY[b]].enabled)
      if (!off.length) return null
      const aps = new Set(view.plan.map((p) => p.ap_id)).size
      return {
        title: `Turn off the ${off.map((b) => `${b} GHz`).join(' and ')} radio?`,
        description: `Every client on ${off.length > 1 ? 'those bands' : 'that band'} is disconnected from ${aps} AP${aps === 1 ? '' : 's'}.`,
        action: 'Turn off and save',
        destructive: true,
      }
    },
    onSaved: (response: MutationResponse<RFProfileView>) => {
      queryClient.setQueryData(keys.siteRF(siteId), response.object)
      void queryClient.invalidateQueries({ queryKey: keys.siteAPs(siteId) })
    },
  })

  return (
    <>
      {!view.managed ? <UnmanagedBanner siteId={siteId} view={view} readOnly={context.readOnly} /> : null}
      <Section id="regulatory" title="Regulatory">
        <div className="grid gap-1 text-sm">
          <p>
            Country <span className="font-medium">{view.country || 'not set'}</span> · ccrev{' '}
            <span className="font-mono font-medium">{view.ccrev ?? 'none'}</span>
          </p>
          <p className="text-muted-foreground">
            Channels and power limits come from the ccrev, not from the country code. Change the country in{' '}
            <Link to={`/sites/${encodeURIComponent(siteId)}/settings/general` as never} className="font-medium text-brand underline-offset-2 hover:underline">
              Settings › General
            </Link>
            ; that restarts every radio on every AP.
          </p>
        </div>
      </Section>
      <ConfigForm controller={controller} context={{ ...context, readOnly: context.readOnly || !view.managed }}>
        <div className="grid gap-4 lg:grid-cols-2">
          <BandCard band="2.4" view={view} />
          <BandCard band="5" view={view} />
        </div>
        <Section id="site-radio" title="Scanning radio" collapsible defaultOpen={false} summary="Neighbour and utilisation scans">
          <OptionField k="site.rf.scanning_radio" />
        </Section>
      </ConfigForm>

      <section aria-labelledby="plan-heading" className="grid gap-3">
        <div className="flex flex-wrap items-end justify-between gap-3">
          <div className="grid gap-1">
            <h2 id="plan-heading" className="text-lg font-semibold">
              Channel plan
            </h2>
            <p className="text-sm text-muted-foreground">The AP does not choose its own channel; dismist plans Auto radios. Nothing is re-planned automatically.</p>
          </div>
          <Button onClick={() => setPlanOpen(true)} disabled={context.readOnly || !view.managed}>
            <Sparkles /> Plan channels
          </Button>
        </div>
        <PlanTable siteId={siteId} rows={view.plan} />
      </section>

      <section aria-labelledby="rf-recs-heading" className="grid gap-3">
        <h2 id="rf-recs-heading" className="text-lg font-semibold">
          Recommendations
        </h2>
        <RecommendationList siteId={siteId} items={view.recommendations} readOnly={context.readOnly} emptyText="No radio recommendations." />
      </section>
      {planOpen ? <PlanDialog siteId={siteId} onClose={() => setPlanOpen(false)} /> : null}
    </>
  )
}

function UnmanagedBanner({ siteId, view, readOnly }: { siteId: string; view: RFProfileView; readOnly: boolean }) {
  const queryClient = useQueryClient()
  const [preview, setPreview] = useState<MutationResponse<RFProfileView> | null>(null)
  const [open, setOpen] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const body = toRFInput({ ...toRFForm(view), managed: true }, view)

  async function previewManage() {
    setOpen(true)
    setError(null)
    try {
      setPreview(await putRF(siteId, body, { dryRun: true, ifMatch: view.updated_rev }))
    } catch (e) {
      setError(describeError(e).message)
    }
  }

  async function apply() {
    setBusy(true)
    try {
      const response = await putRF(siteId, body, { ifMatch: view.updated_rev })
      queryClient.setQueryData(keys.siteRF(siteId), response.object)
      toast.success('dismist now manages RF', { description: impactLine(response.impact) })
      setOpen(false)
    } catch (e) {
      setError(describeError(e).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <Alert variant="warning">
        <TriangleAlert />
        <AlertTitle>RF is not managed by dismist.</AlertTitle>
        <AlertDescription className="grid gap-2">
          <p>Your APs use the firmware defaults: channel 36/20 and 1/20, US/0 regulatory rules, and a 0 dBm power target.</p>
          <Button variant="outline" size="sm" className="justify-self-start" disabled={readOnly} onClick={() => void previewManage()}>
            Manage RF…
          </Button>
        </AlertDescription>
      </Alert>
      <Dialog
        open={open}
        onOpenChange={setOpen}
        title="Manage RF with dismist"
        description={preview ? impactLine(preview.impact) : 'Checking what changes…'}
        wide
        footer={
          <>
            <Button variant="outline" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button onClick={() => void apply()} disabled={!preview || busy}>
              {busy ? <Loader2 className="motion-safe:animate-spin" /> : null}
              Manage RF
            </Button>
          </>
        }
      >
        {error ? (
          <p role="alert" className="text-sm font-medium text-destructive">
            {error}
          </p>
        ) : preview ? (
          <ImpactDiff impact={preview.impact} />
        ) : (
          <Loader2 aria-label="Loading" className="size-5 motion-safe:animate-spin" />
        )}
      </Dialog>
    </>
  )
}

function liveLine(rows: PlanRow[]) {
  return rows
    .filter((r) => r.actual)
    .map((r) => `${r.ap_name}: ch ${r.actual!.channel}/${r.actual!.width} · ${r.actual!.max_power_dbm} dBm`)
    .join(' · ')
}

function BandCard({ band, view }: { band: Band; view: RFProfileView }) {
  const key = BAND_KEY[band]
  const { control, setValue, getValues } = useFormContext<RFForm>()
  const width = useWatch({ control, name: `${key}.width` })
  const dfs = useWatch({ control, name: `${key}.dfs` })
  const enabled = useWatch({ control, name: `${key}.enabled` })
  const valid = view[key].valid_channels
  const rows = view.plan.filter((p) => p.band === band)

  // A width change re-maps the Auto list to the new blocks and drops a fallback that no longer fits.
  useEffect(() => {
    const current = getValues(`${key}.channels`)
    const remapped = remapChannels(band, current, width, valid)
    if (remapped.join() !== current.join()) setValue(`${key}.channels`, remapped, { shouldDirty: true, shouldValidate: true })
    const fallback = getValues(`${key}.dfs_fallback`)
    if (band === '5' && fallback && !fallbackChoices(width, valid).includes(fallback)) setValue(`${key}.dfs_fallback`, 0, { shouldDirty: true })
  }, [band, getValues, key, setValue, valid, width])

  const advancedTitle = band === '2.4' ? '2.4 GHz advanced' : '5 GHz advanced'
  return (
    <section aria-labelledby={`${key}-heading`} className="grid content-start gap-5 rounded-xl border bg-card px-4 pt-4 pb-5 md:px-5">
      <div className="grid gap-1">
        <h2 id={`${key}-heading`} className="flex items-center gap-2 text-base font-semibold">
          <AudioWaveform aria-hidden className={cn('size-4', band === '2.4' ? 'text-band-24' : 'text-band-5')} />
          {band} GHz
        </h2>
        {rows.some((r) => r.actual) ? <p className="text-xs text-muted-foreground">Now: {liveLine(rows)}</p> : null}
      </div>
      <OptionField k={`site.rf.${key}.enabled`} help={enabled ? undefined : 'Clients on this band are disconnected while it is off.'} />
      {band === '2.4' ? (
        <div className="grid gap-1">
          <span className="text-sm font-medium">Channel width</span>
          <span className="text-sm">20 MHz</span>
          <span className="text-sm text-muted-foreground">40 MHz on 2.4 GHz is only available through the expert patch.</span>
        </div>
      ) : (
        <OptionField
          k="site.rf.band_5.width"
          // AP33 has no 160 MHz chanspec under AU/890 (live `wl chanspecs`): never offered, whatever the registry lists.
          choices={getOption('site.rf.band_5.width')
            .enum.filter((e) => WIDTHS['5'].includes(Number(e.value)))
            .map((e) => ({ value: e.value, label: e.label }))}
        />
      )}
      {band === '5' ? <OptionField k="site.rf.band_5.dfs" /> : null}
      <ChannelsField band={band} valid={valid} width={width} dfs={dfs} />
      <PowerField band={band} />
      <Section id={`${key}-advanced`} title={advancedTitle} collapsible defaultOpen={false} className="border-dashed bg-transparent" summary="Preamble, airtime fairness, TWT, antennas">
        {band === '2.4' ? <OptionField k="site.rf.band_24.preamble" /> : <FallbackField width={width} valid={valid} />}
        <OptionField k={`site.rf.${key}.advanced.taf`} />
        <OptionField k={`site.rf.${key}.advanced.twt`} />
        <OptionField k={`site.rf.${key}.advanced.antenna`} />
        <OptionField k={`site.rf.${key}.advanced.min_probe_rssi`} placeholder="Off" />
        <OptionField k={`site.rf.${key}.advanced.mu_mimo`} />
        <OptionField k={`site.rf.${key}.advanced.txbf`} />
      </Section>
    </section>
  )
}

function ChannelsField({ band, valid, width, dfs }: { band: Band; valid: RFProfileView['band_5']['valid_channels']; width: number; dfs: boolean }) {
  const key = BAND_KEY[band]
  const state = useOptionState(`site.rf.${key}.channels`)
  const { control } = useFormContext<RFForm>()
  const { field, fieldState } = useController({ control, name: `${key}.channels` })
  const id = useId()
  const chips = channelChips(band, width, valid)
  const groups = [...new Set(chips.map((c) => c.group))]
  const help = band === '5' ? `${state.meta.help} At ${width} MHz each chip is one ${width} MHz block.` : state.meta.help
  return (
    <FieldShell id={id} label={state.meta.label} help={help} error={fieldState.error?.message} badges={state.badges} dirty={fieldState.isDirty} labelAs="span">
      <div className="grid gap-3" aria-describedby={describedBy(id, help, fieldState.error)}>
        {groups.map((group) => (
          <div key={group} className="grid gap-1.5">
            {band === '5' ? <span className="text-xs font-medium text-muted-foreground">{group}</span> : null}
            <ChipGroup
              labelledBy={`${id}-label`}
              value={field.value}
              numeric
              onChange={(next) => field.onChange(next)}
              disabled={!state.editable}
              choices={chips
                .filter((c) => c.group === group)
                .map((c) => ({ value: String(c.value), label: c.label, disabled: c.dfs && !dfs, hint: c.dfs ? 'DFS' : undefined }))}
            />
          </div>
        ))}
      </div>
    </FieldShell>
  )
}

function FallbackField({ width, valid }: { width: number; valid: RFProfileView['band_5']['valid_channels'] }) {
  const choices = [{ value: '0', label: 'Auto (best non-DFS channel)' }, ...fallbackChoices(width, valid).map((c) => ({ value: String(c), label: `Channel ${c}` }))]
  return <OptionField k="site.rf.band_5.dfs_fallback" choices={choices} />
}

function PowerField({ band }: { band: Band }) {
  const key = BAND_KEY[band]
  const state = useOptionState(`site.rf.${key}.power`)
  const { control, setValue, formState } = useFormContext<RFForm>()
  const power = useWatch({ control, name: `${key}.power` })
  const { field: dbm } = useController({ control, name: `${key}.power.dbm` })
  const { field: min } = useController({ control, name: `${key}.power.min` })
  const { field: max } = useController({ control, name: `${key}.power.max` })
  const id = useId()
  const errors = (formState.errors[key] as { power?: Record<string, { message?: string }> } | undefined)?.power
  const high = [power.dbm, power.max].some((v) => typeof v === 'number' && v > POWER_WARN && (power.mode === 'fixed' ? v === power.dbm : v === power.max))
  return (
    <FieldShell id={id} label={state.meta.label} help={state.meta.help} badges={state.badges} labelAs="span">
      <Segmented
        aria-labelledby={`${id}-label`}
        value={power.mode}
        onValueChange={(v) => setValue(`${key}.power.mode`, v as 'auto' | 'fixed', { shouldDirty: true, shouldValidate: true })}
        disabled={!state.editable}
        options={[
          { value: 'auto', label: 'Auto' },
          { value: 'fixed', label: 'Fixed' },
        ]}
      />
      {power.mode === 'fixed' ? (
        <PowerNumber id={`${id}-dbm`} label="Power" field={dbm} error={errors?.dbm?.message} disabled={!state.editable} />
      ) : (
        <div className="grid gap-3 sm:grid-cols-2">
          <PowerNumber id={`${id}-min`} label="Minimum" field={min} error={errors?.min?.message} disabled={!state.editable} />
          <PowerNumber id={`${id}-max`} label="Maximum" field={max} error={errors?.max?.message} disabled={!state.editable} />
        </div>
      )}
      {high ? (
        <p className="flex items-center gap-1.5 text-sm text-warning">
          <TriangleAlert aria-hidden className="size-4" /> Above {POWER_WARN} dBm is rarely useful indoors and may not be allowed on every channel.
        </p>
      ) : null}
    </FieldShell>
  )
}

function PowerNumber({ id, label, field, error, disabled }: { id: string; label: string; field: { value: unknown; onChange: (v: unknown) => void; onBlur: () => void; name: string; ref: React.Ref<HTMLInputElement> }; error?: string; disabled: boolean }) {
  return (
    <div className="grid gap-1">
      <label htmlFor={id} className="text-sm text-muted-foreground">
        {label}
      </label>
      <NumberInput id={id} name={field.name} inputRef={field.ref} value={field.value as number | null} onChange={field.onChange} onBlur={field.onBlur} min={1} max={25} unit="dBm" disabled={disabled} aria-invalid={error ? true : undefined} aria-describedby={error ? `${id}-error` : undefined} />
      {error ? (
        <p id={`${id}-error`} role="alert" className="text-sm font-medium text-destructive">
          {error}
        </p>
      ) : null}
    </div>
  )
}
