import { useId } from 'react'
import { useController, useFieldArray, useFormContext, useWatch } from 'react-hook-form'
import { RadioGroup } from 'radix-ui'
import { Plus, Trash2 } from 'lucide-react'
import type { Security } from '@/api/queries/networks'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ExperimentalBadge } from '@/features/settings/kit/badges'
import { useConfigFormContext } from '@/features/settings/kit/context'
import { describedBy, FieldShell, Labeled, NumberInput, PasswordInput, useOptionState } from '@/features/settings/kit/fields'
import { visibleEnum } from '@/features/settings/kit/options'
import { NativeSelect, Segmented } from '@/features/settings/kit/primitives'
import { cn } from '@/lib/utils'
import { DAYS, type Day, type WlanForm } from './form'

// Composite WLAN fields (design §3.9): the security radio cards, VLAN,
// per-band minimum RSSI, rate limits, RADIUS server rows, per-device keys and
// the schedule. Each reads its option metadata for labels, help, experimental
// and shadowed badges.

function fieldError(errors: unknown, path: string): string | undefined {
  const node = path.split('.').reduce<unknown>((n, p) => (n as Record<string, unknown> | undefined)?.[p], errors)
  return (node as { message?: string } | undefined)?.message
}

export function SecurityField() {
  const state = useOptionState('wlan.security')
  const { experimentalAllowed } = useConfigFormContext()
  const { control } = useFormContext<WlanForm>()
  const { field, fieldState } = useController({ control, name: 'security' })
  const id = useId()
  const choices = visibleEnum(state.meta, experimentalAllowed, field.value)
  return (
    <FieldShell id={id} label={state.meta.label} error={fieldState.error?.message} badges={state.badges} dirty={fieldState.isDirty} labelAs="span" help={state.lockReason ?? undefined}>
      <RadioGroup.Root
        aria-labelledby={`${id}-label`}
        aria-describedby={describedBy(id, state.lockReason, fieldState.error)}
        value={field.value}
        onValueChange={(v) => field.onChange(v as Security)}
        disabled={!state.editable}
        className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3"
      >
        {choices.map((choice) => (
          <RadioGroup.Item
            key={choice.value}
            value={choice.value}
            className={cn(
              'grid min-h-11 cursor-pointer content-start gap-0.5 rounded-lg border bg-card p-3 text-left outline-none transition-colors focus-visible:ring-3 focus-visible:ring-ring/30 disabled:cursor-not-allowed disabled:opacity-60',
              'data-[state=checked]:border-brand data-[state=checked]:bg-brand-soft hover:bg-accent/60',
            )}
          >
            <span className="flex flex-wrap items-center gap-2 text-sm font-medium">
              <span aria-hidden className="grid size-4 shrink-0 place-items-center rounded-full border border-input">
                <RadioGroup.Indicator className="size-2 rounded-full bg-brand" />
              </span>
              {choice.label}
              {choice.experimental ? <ExperimentalBadge /> : null}
            </span>
            {choice.help ? <span className="pl-6 text-xs text-muted-foreground">{choice.help}</span> : null}
          </RadioGroup.Item>
        ))}
      </RadioGroup.Root>
    </FieldShell>
  )
}

export function PassphraseField({ saved, help }: { saved: boolean; help: string }) {
  const state = useOptionState('wlan.psk')
  const { control } = useFormContext<WlanForm>()
  const { field, fieldState } = useController({ control, name: 'psk' })
  const id = useId()
  if (state.hidden) return null
  return (
    <FieldShell id={id} label={state.meta.label} help={help} error={fieldState.error?.message} badges={state.badges} dirty={Boolean(field.value)}>
      <PasswordInput
        id={id}
        name={field.name}
        inputRef={field.ref}
        value={field.value}
        onChange={field.onChange}
        onBlur={field.onBlur}
        saved={saved}
        disabled={!state.editable}
        aria-invalid={fieldState.error ? true : undefined}
        aria-describedby={describedBy(id, help, fieldState.error)}
      />
    </FieldShell>
  )
}

export function VlanField() {
  const state = useOptionState('wlan.vlan_id')
  const { control, setValue, formState } = useFormContext<WlanForm>()
  const vlan = useWatch({ control, name: 'vlan' })
  const { field: idField, fieldState } = useController({ control, name: 'vlan.id' })
  const id = useId()
  const error = fieldError(formState.errors, 'vlan.id') ?? fieldState.error?.message
  return (
    <FieldShell id={id} label={state.meta.label} help={state.meta.help} error={error} badges={state.badges} dirty={fieldState.isDirty || formState.dirtyFields.vlan?.tagged} labelAs="span">
      <div className="flex flex-wrap items-center gap-3">
        <Segmented
          aria-labelledby={`${id}-label`}
          value={vlan.tagged ? 'tagged' : 'untagged'}
          onValueChange={(v) => setValue('vlan.tagged', v === 'tagged', { shouldDirty: true, shouldValidate: true })}
          disabled={!state.editable}
          options={[
            { value: 'untagged', label: 'Untagged' },
            { value: 'tagged', label: 'Tagged' },
          ]}
        />
        {vlan.tagged ? (
          <div className="flex items-center gap-2">
            <label htmlFor={id} className="text-sm text-muted-foreground">
              VLAN ID
            </label>
            <NumberInput
              id={id}
              name={idField.name}
              inputRef={idField.ref}
              value={idField.value as number | string | null}
              onChange={idField.onChange}
              onBlur={idField.onBlur}
              min={2}
              max={4094}
              disabled={!state.editable}
              aria-invalid={error ? true : undefined}
              aria-describedby={describedBy(id, state.meta.help, error)}
            />
          </div>
        ) : null}
      </div>
    </FieldShell>
  )
}

export function MinRSSIField() {
  const state = useOptionState('wlan.min_rssi')
  const { control, formState } = useFormContext<WlanForm>()
  const bands = useWatch({ control, name: 'bands' })
  const id = useId()
  if (state.hidden) return null
  return (
    <FieldShell as="fieldset" labelAs="legend" id={id} label={state.meta.label} help={state.meta.help ?? undefined} badges={state.badges}>
      <div className="grid gap-3 sm:grid-cols-2">
        {(['2.4', '5'] as const).map((band) => (
          <BandNumber key={band} name={`min_rssi.${band === '2.4' ? 'b24' : 'b5'}`} label={`${band} GHz`} disabled={!state.editable || !bands.includes(band)} min={-95} max={-50} unit="dBm" error={fieldError(formState.errors, `min_rssi.${band === '2.4' ? 'b24' : 'b5'}`)} />
        ))}
      </div>
    </FieldShell>
  )
}

function BandNumber({ name, label, disabled, min, max, unit, error }: { name: string; label: string; disabled: boolean; min: number; max: number; unit: string; error?: string }) {
  const { control } = useFormContext()
  const { field } = useController({ control, name })
  const id = useId()
  return (
    <div className="grid gap-1">
      <label htmlFor={id} className="text-sm text-muted-foreground">
        {label}
      </label>
      <NumberInput
        id={id}
        name={field.name}
        inputRef={field.ref}
        value={field.value as number | string | null}
        onChange={field.onChange}
        onBlur={field.onBlur}
        min={min}
        max={max}
        unit={unit}
        disabled={disabled}
        placeholder="Off"
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? `${id}-error` : undefined}
      />
      {error ? (
        <p id={`${id}-error`} role="alert" className="text-sm font-medium text-destructive">
          {error}
        </p>
      ) : null}
    </div>
  )
}

export function RateLimitField() {
  const state = useOptionState('wlan.rate_limit')
  const { formState } = useFormContext<WlanForm>()
  const id = useId()
  if (state.hidden) return null
  const rows = [
    ['wlan_up', 'Whole WLAN, upload'],
    ['wlan_down', 'Whole WLAN, download'],
    ['client_up', 'Each client, upload'],
    ['client_down', 'Each client, download'],
  ] as const
  return (
    <FieldShell as="fieldset" labelAs="legend" id={id} label={state.meta.label} help={state.meta.help} badges={state.badges}>
      <div className="grid gap-3 sm:grid-cols-2">
        {rows.map(([key, label]) => (
          <BandNumber key={key} name={`rate_limit.${key}`} label={label} disabled={!state.editable} min={1} max={999000} unit="kbps" error={fieldError(formState.errors, `rate_limit.${key}`)} />
        ))}
      </div>
    </FieldShell>
  )
}

export function ServerRows({ name, optionKey, defaultPort, sameAs }: { name: 'radius.auth' | 'radius.acct'; optionKey: string; defaultPort: number; sameAs?: () => void }) {
  const state = useOptionState(optionKey)
  const { control, register, formState } = useFormContext<WlanForm>()
  const { fields, append, remove } = useFieldArray({ control, name })
  const id = useId()
  if (state.hidden) return null
  const max = state.meta.max ?? 2
  const listError = fieldError(formState.errors, name)
  return (
    <FieldShell as="fieldset" labelAs="legend" id={id} label={state.meta.label} help={state.meta.help} error={listError} badges={state.badges}>
      <div className="grid gap-3">
        {fields.map((row, index) => {
          const base = `${name}.${index}` as const
          const hostError = fieldError(formState.errors, `${base}.host`)
          const portError = fieldError(formState.errors, `${base}.port`)
          const secretError = fieldError(formState.errors, `${base}.secret`)
          return (
            <div key={row.id} className="grid gap-2 rounded-lg border p-3 md:grid-cols-[minmax(0,2fr)_7rem_minmax(0,2fr)_auto] md:items-start">
              <label className="grid gap-1 text-sm">
                <span className="text-muted-foreground">Server {index + 1}</span>
                <Input {...register(`${base}.host`)} disabled={!state.editable} autoCapitalize="none" autoCorrect="off" spellCheck={false} inputMode="url" aria-invalid={hostError ? true : undefined} />
                {hostError ? <span role="alert" className="text-destructive">{hostError}</span> : null}
              </label>
              <PortInput name={`${base}.port`} label="Port" disabled={!state.editable} error={portError} />
              <SecretInput name={`${base}.secret`} hasSecretName={`${base}.has_secret`} label="Shared secret" disabled={!state.editable} error={secretError} />
              <Button variant="ghost" size="icon-sm" className="md:mt-6" aria-label={`Remove server ${index + 1}`} disabled={!state.editable} onClick={() => remove(index)}>
                <Trash2 />
              </Button>
            </div>
          )
        })}
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" disabled={!state.editable || fields.length >= max} onClick={() => append({ host: '', port: defaultPort, secret: '', has_secret: false })}>
            <Plus /> Add server
          </Button>
          {sameAs ? (
            <Button variant="ghost" size="sm" disabled={!state.editable} onClick={sameAs}>
              Same as authentication servers
            </Button>
          ) : null}
        </div>
      </div>
    </FieldShell>
  )
}

function PortInput({ name, label, disabled, error }: { name: string; label: string; disabled: boolean; error?: string }) {
  const { control } = useFormContext()
  const { field } = useController({ control, name })
  const id = useId()
  return (
    <div className="grid gap-1 text-sm">
      <label htmlFor={id} className="text-muted-foreground">
        {label}
      </label>
      <NumberInput id={id} name={field.name} inputRef={field.ref} value={field.value as number | null} onChange={field.onChange} onBlur={field.onBlur} min={1} max={65535} disabled={disabled} aria-invalid={error ? true : undefined} />
      {error ? <span role="alert" className="text-destructive">{error}</span> : null}
    </div>
  )
}

function SecretInput({ name, hasSecretName, label, disabled, error }: { name: string; hasSecretName: string; label: string; disabled: boolean; error?: string }) {
  const { control } = useFormContext()
  const { field } = useController({ control, name })
  const saved = Boolean(useWatch({ control, name: hasSecretName }))
  const id = useId()
  return (
    <div className="grid gap-1 text-sm">
      <label htmlFor={id} className="text-muted-foreground">
        {label}
      </label>
      <PasswordInput
        id={id}
        name={field.name}
        inputRef={field.ref}
        value={(field.value as string) ?? ''}
        onChange={field.onChange}
        onBlur={field.onBlur}
        saved={saved}
        generate={false}
        changeLabel="Change secret"
        disabled={disabled}
        aria-invalid={error ? true : undefined}
      />
      {error ? <span role="alert" className="text-destructive">{error}</span> : null}
    </div>
  )
}

export function CoARows() {
  const state = useOptionState('wlan.radius.coa')
  const { control, register, formState } = useFormContext<WlanForm>()
  const enabled = useWatch({ control, name: 'radius.coa.enabled' })
  const { fields, append, remove } = useFieldArray({ control, name: 'radius.coa.servers' })
  const { field: enabledField } = useController({ control, name: 'radius.coa.enabled' })
  const { field: tsField } = useController({ control, name: 'radius.coa.event_timestamp_optional' })
  const id = useId()
  if (state.hidden) return null
  return (
    <FieldShell as="fieldset" labelAs="legend" id={id} label={state.meta.label} help="Lets a RADIUS server change or end a client’s session (port 3799)." badges={state.badges}>
      <label className="flex min-h-11 items-center gap-3 text-sm">
        <input type="checkbox" className="size-5 accent-brand" checked={Boolean(enabledField.value)} onChange={(e) => enabledField.onChange(e.target.checked)} disabled={!state.editable} />
        Accept Change of Authorisation
      </label>
      {enabled ? (
        <div className="grid gap-3">
          {fields.map((row, index) => (
            <div key={row.id} className="grid gap-2 rounded-lg border p-3 md:grid-cols-[minmax(0,2fr)_7rem_minmax(0,2fr)_auto] md:items-start">
              <label className="grid gap-1 text-sm">
                <span className="text-muted-foreground">Client {index + 1}</span>
                <Input {...register(`radius.coa.servers.${index}.host`)} disabled={!state.editable} autoCapitalize="none" autoCorrect="off" spellCheck={false} />
                {fieldError(formState.errors, `radius.coa.servers.${index}.host`) ? <span role="alert" className="text-destructive">{fieldError(formState.errors, `radius.coa.servers.${index}.host`)}</span> : null}
              </label>
              <PortInput name={`radius.coa.servers.${index}.local_port`} label="Local port" disabled={!state.editable} error={fieldError(formState.errors, `radius.coa.servers.${index}.local_port`)} />
              <SecretInput name={`radius.coa.servers.${index}.secret`} hasSecretName={`radius.coa.servers.${index}.has_secret`} label="Shared secret" disabled={!state.editable} error={fieldError(formState.errors, `radius.coa.servers.${index}.secret`)} />
              <Button variant="ghost" size="icon-sm" className="md:mt-6" aria-label={`Remove CoA client ${index + 1}`} onClick={() => remove(index)} disabled={!state.editable}>
                <Trash2 />
              </Button>
            </div>
          ))}
          <Button variant="outline" size="sm" className="justify-self-start" disabled={!state.editable || fields.length >= 2} onClick={() => append({ host: '', local_port: 3799, secret: '', has_secret: false })}>
            <Plus /> Add CoA client
          </Button>
          <label className="flex min-h-11 items-center gap-3 text-sm">
            <input type="checkbox" className="size-5 accent-brand" checked={Boolean(tsField.value)} onChange={(e) => tsField.onChange(e.target.checked)} disabled={!state.editable} />
            Accept CoA without Event-Timestamp
          </label>
        </div>
      ) : null}
    </FieldShell>
  )
}

export function DynamicVlanField() {
  const state = useOptionState('wlan.radius.dynamic_vlan')
  const { control, register, formState } = useFormContext<WlanForm>()
  const enabled = useWatch({ control, name: 'radius.dynamic_vlan.enabled' })
  const { field: enabledField } = useController({ control, name: 'radius.dynamic_vlan.enabled' })
  const { field: typeField } = useController({ control, name: 'radius.dynamic_vlan.type' })
  const id = useId()
  if (state.hidden) return null
  const error = fieldError(formState.errors, 'radius.dynamic_vlan.vlan_ids')
  return (
    <FieldShell as="fieldset" labelAs="legend" id={id} label={state.meta.label} help="The RADIUS server picks the client’s VLAN. Each VLAN is added to the uplink trunk." badges={state.badges}>
      <label className="flex min-h-11 items-center gap-3 text-sm">
        <input type="checkbox" className="size-5 accent-brand" checked={Boolean(enabledField.value)} onChange={(e) => enabledField.onChange(e.target.checked)} disabled={!state.editable} />
        Assign VLANs from RADIUS
      </label>
      {enabled ? (
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="grid gap-1 text-sm">
            <span className="text-muted-foreground">VLANs</span>
            <Input {...register('radius.dynamic_vlan.vlan_ids')} placeholder="30, 40" inputMode="numeric" disabled={!state.editable} aria-invalid={error ? true : undefined} />
            {error ? <span role="alert" className="text-destructive">{error}</span> : null}
          </label>
          <Labeled label="Attribute">
            {(selectId) => (
              <NativeSelect id={selectId} value={typeField.value} onChange={(e) => typeField.onChange(e.target.value)} disabled={!state.editable}>
                <option value="standard">Standard (Tunnel-Private-Group-ID)</option>
                <option value="airespace-interface-name">Airespace interface name</option>
              </NativeSelect>
            )}
          </Labeled>
        </div>
      ) : null}
    </FieldShell>
  )
}

export function MPSKKeys() {
  const state = useOptionState('wlan.mpsk')
  const { control, register, formState } = useFormContext<WlanForm>()
  const { fields, append, remove } = useFieldArray({ control, name: 'mpsk.keys' })
  const id = useId()
  return (
    <FieldShell as="fieldset" labelAs="legend" id={id} label="Keys" help="Each device (or group) gets its own passphrase, optionally its own VLAN, a bound MAC and an expiry." error={fieldError(formState.errors, 'mpsk.keys')}>
      <div className="grid gap-3">
        {fields.map((row, index) => {
          const base = `mpsk.keys.${index}` as const
          const err = (leaf: string) => fieldError(formState.errors, `${base}.${leaf}`)
          return (
            <div key={row.id} className="grid gap-2 rounded-lg border p-3 md:grid-cols-2 xl:grid-cols-[minmax(0,1fr)_minmax(0,1.5fr)_7rem_minmax(0,1fr)_9rem_auto] xl:items-start">
              <label className="grid gap-1 text-sm">
                <span className="text-muted-foreground">Name</span>
                <Input {...register(`${base}.name`)} disabled={!state.editable} aria-invalid={err('name') ? true : undefined} />
                {err('name') ? <span role="alert" className="text-destructive">{err('name')}</span> : null}
              </label>
              <KeyPassphrase index={index} disabled={!state.editable} error={err('passphrase')} />
              <PortlessNumber name={`${base}.vlan_id`} label="VLAN" placeholder="WLAN" disabled={!state.editable} error={err('vlan_id')} />
              <label className="grid gap-1 text-sm">
                <span className="text-muted-foreground">Bound MAC</span>
                <Input {...register(`${base}.mac`)} placeholder="Any device" disabled={!state.editable} autoCapitalize="none" autoCorrect="off" spellCheck={false} aria-invalid={err('mac') ? true : undefined} />
                {err('mac') ? <span role="alert" className="text-destructive">{err('mac')}</span> : null}
              </label>
              <Labeled label="Expires">{(dateId) => <Input id={dateId} type="date" {...register(`${base}.expires`)} disabled={!state.editable} />}</Labeled>
              <Button variant="ghost" size="icon-sm" className="xl:mt-6" aria-label={`Remove key ${index + 1}`} onClick={() => remove(index)} disabled={!state.editable}>
                <Trash2 />
              </Button>
            </div>
          )
        })}
        <Button
          variant="outline"
          size="sm"
          className="justify-self-start"
          disabled={!state.editable || fields.length >= (state.meta.max ?? 32)}
          onClick={() => append({ id: '', name: '', passphrase: '', has_passphrase: false, vlan_id: null, mac: '', expires: '' })}
        >
          <Plus /> Add key
        </Button>
      </div>
    </FieldShell>
  )
}

function KeyPassphrase({ index, disabled, error }: { index: number; disabled: boolean; error?: string }) {
  const { control } = useFormContext<WlanForm>()
  const { field } = useController({ control, name: `mpsk.keys.${index}.passphrase` })
  const saved = useWatch({ control, name: `mpsk.keys.${index}.has_passphrase` })
  const id = useId()
  return (
    <div className="grid gap-1 text-sm">
      <label htmlFor={id} className="text-muted-foreground">
        Passphrase
      </label>
      <PasswordInput id={id} name={field.name} inputRef={field.ref} value={field.value} onChange={field.onChange} onBlur={field.onBlur} saved={saved} disabled={disabled} aria-invalid={error ? true : undefined} />
      {error ? <span role="alert" className="text-destructive">{error}</span> : null}
    </div>
  )
}

function PortlessNumber({ name, label, placeholder, disabled, error }: { name: string; label: string; placeholder?: string; disabled: boolean; error?: string }) {
  const { control } = useFormContext()
  const { field } = useController({ control, name })
  const id = useId()
  return (
    <div className="grid gap-1 text-sm">
      <label htmlFor={id} className="text-muted-foreground">
        {label}
      </label>
      <NumberInput id={id} name={field.name} inputRef={field.ref} value={field.value as number | null} onChange={field.onChange} onBlur={field.onBlur} min={2} max={4094} placeholder={placeholder} disabled={disabled} aria-invalid={error ? true : undefined} />
      {error ? <span role="alert" className="text-destructive">{error}</span> : null}
    </div>
  )
}

const DAY_LABEL: Record<Day, string> = { mon: 'Monday', tue: 'Tuesday', wed: 'Wednesday', thu: 'Thursday', fri: 'Friday', sat: 'Saturday', sun: 'Sunday' }

export function ScheduleDays({ disabled }: { disabled: boolean }) {
  const { register, formState } = useFormContext<WlanForm>()
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      {DAYS.map((day) => {
        const error = fieldError(formState.errors, `schedule.days.${day}`)
        return (
          <label key={day} className="grid gap-1 text-sm">
            <span className="text-muted-foreground">{DAY_LABEL[day]}</span>
            <Input {...register(`schedule.days.${day}`)} placeholder="Off all day" disabled={disabled} inputMode="text" autoCapitalize="none" aria-invalid={error ? true : undefined} />
            {error ? <span role="alert" className="text-destructive">{error}</span> : null}
          </label>
        )
      })}
    </div>
  )
}
