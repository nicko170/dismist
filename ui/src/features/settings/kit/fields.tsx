import { useId, useState, type ReactNode } from 'react'
import { useController, useFormContext, useWatch, type FieldValues } from 'react-hook-form'
import { Eye, EyeOff, KeyRound, Minus, Plus, RefreshCw, Trash2 } from 'lucide-react'
import type { OptionMeta } from '@/gen/options.gen'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { utf8Length } from '@/lib/format'
import { cn } from '@/lib/utils'
import { CrossScopeBadge, ExperimentalBadge, ShadowedBadge } from './badges'
import { isFieldEditable, shadowFor, useConfigFormContext, type ShadowState } from './context'
import { conditionsHold, fieldName, getOption, isExperimentalValue, visibleEnum } from './options'
import { NativeSelect, Segmented, Slider, Switch } from './primitives'
import { deepEqual } from './patch'

// Field components bound to option metadata (design §7.4). Every field
// renders its label and help from the registry, an inline error with
// aria-invalid/aria-describedby, experimental and shadowed badges, and a
// dirty marker. Controls follow §7.7: 16 px on touch, 44 px targets, the
// right keypad for the bounds.

// ---- shell ----

export function FieldShell({
  id,
  label,
  help,
  error,
  badges,
  dirty,
  inline,
  children,
  className,
  as = 'div',
  labelAs = 'label',
}: {
  id: string
  label: ReactNode
  help?: ReactNode
  error?: string
  badges?: ReactNode
  dirty?: boolean
  /** Label and control on one row (switches). */
  inline?: boolean
  children: ReactNode
  className?: string
  as?: 'div' | 'fieldset'
  /** "legend" for fieldsets of several controls; "span" when the control labels itself. */
  labelAs?: 'label' | 'legend' | 'span'
}) {
  const Wrapper = as
  const labelNode =
    labelAs === 'label' ? (
      <label htmlFor={id} className="text-sm font-medium">
        {label}
      </label>
    ) : labelAs === 'legend' ? (
      <legend className="float-left mb-1 w-full text-sm font-medium">{label}</legend>
    ) : (
      <span id={`${id}-label`} className="text-sm font-medium">
        {label}
      </span>
    )
  const meta = (
    <>
      {help ? (
        <p id={`${id}-help`} className="text-sm text-muted-foreground">
          {help}
        </p>
      ) : null}
      {error ? (
        <p id={`${id}-error`} role="alert" className="text-sm font-medium text-destructive">
          {error}
        </p>
      ) : null}
    </>
  )
  const head = (
    <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
      {labelNode}
      {dirty ? (
        <span className="inline-flex items-center gap-1 text-xs text-brand">
          <span aria-hidden className="size-1.5 rounded-full bg-brand" />
          Changed
        </span>
      ) : null}
      {badges}
    </div>
  )
  if (inline) {
    return (
      <Wrapper className={cn('grid min-w-0 gap-1', as === 'fieldset' && 'border-0 p-0', className)} data-field={id}>
        <div className="flex items-start justify-between gap-4">
          <div className="grid min-w-0 gap-1">
            {head}
            {meta}
          </div>
          <div className="shrink-0 pt-0.5">{children}</div>
        </div>
      </Wrapper>
    )
  }
  return (
    <Wrapper className={cn('grid min-w-0 gap-2', as === 'fieldset' && 'border-0 p-0', className)} data-field={id}>
      {head}
      {children}
      {meta}
    </Wrapper>
  )
}

export function describedBy(id: string, help?: unknown, error?: unknown) {
  return [help ? `${id}-help` : null, error ? `${id}-error` : null].filter(Boolean).join(' ') || undefined
}

// ---- option state shared by simple and composite fields ----

export type OptionState = {
  meta: OptionMeta
  name: string
  /** The field is not shown at all (visibility rule, or experimental and unused). */
  hidden: boolean
  editable: boolean
  experimental: boolean
  shadow: ShadowState | null
  badges: ReactNode
  /** Why the field cannot be edited, as help text. */
  lockReason: string | null
}

/**
 * Whether an option holds nothing worth showing: empty, its default, false,
 * an object switched off ({enabled: false, …}) or one whose every leaf is
 * empty. Experimental options that are unset stay hidden on sites without
 * experimental options (§4.2).
 */
export function isUnset(value: unknown, fallback: unknown): boolean {
  if (value === null || value === undefined || value === '' || value === false) return true
  if (fallback !== null && fallback !== undefined && deepEqual(value, fallback)) return true
  if (Array.isArray(value)) return value.every((v) => isUnset(v, undefined))
  if (typeof value === 'object') {
    const record = value as Record<string, unknown>
    if (record.enabled === false) return true
    return Object.values(record).every((v) => isUnset(v, undefined))
  }
  return false
}

export function useOptionState(key: string, options: { disabled?: boolean; value?: unknown } = {}): OptionState {
  const meta = getOption(key)
  const name = fieldName(meta)
  const ctx = useConfigFormContext()
  const { control } = useFormContext()
  const values = useWatch({ control }) as FieldValues
  const value = options.value !== undefined ? options.value : name.split('.').reduce<unknown>((n, p) => (n as Record<string, unknown> | undefined)?.[p], values)
  const visible = conditionsHold(meta, values)
  const experimental = isExperimentalValue(meta, value)
  const optionLocked = meta.experimental && !ctx.experimentalAllowed
  const hidden = !visible || (optionLocked && isUnset(value, meta.default))
  const shadow = shadowFor(ctx.expert, key)
  let lockReason: string | null = null
  if (!isFieldEditable(ctx, key)) lockReason = null
  else if (shadow?.full) lockReason = 'Remove this key from the expert patch to edit it here.'
  else if (optionLocked) lockReason = 'Turn on experimental options in Settings › Advanced to change this.'
  const editable = isFieldEditable(ctx, key) && !shadow?.full && !optionLocked && !options.disabled
  const badges = (
    <>
      {experimental ? <ExperimentalBadge /> : null}
      {shadow && (shadow.full || shadow.paths.length) ? (
        <ShadowedBadge partial={!shadow.full} paths={shadow.paths} to={ctx.expertLink?.to} search={ctx.expertLink?.search} />
      ) : null}
      {shadow?.crossScope ? <CrossScopeBadge shadow={shadow.crossScope} siteId={ctx.siteId} /> : null}
    </>
  )
  return { meta, name, hidden, editable, experimental, shadow, badges, lockReason }
}

function helpText(meta: OptionMeta, override: ReactNode | undefined, lockReason: string | null): ReactNode {
  const base = override ?? meta.help
  if (!lockReason) return base || undefined
  return (
    <>
      {base ? <>{base} </> : null}
      <span className="text-foreground">{lockReason}</span>
    </>
  )
}

// ---- the generic option field ----

type OptionFieldProps = {
  k: string
  label?: ReactNode
  help?: ReactNode
  disabled?: boolean
  /** Write-only secret already stored (passphrase placeholder). */
  saved?: boolean
  placeholder?: string
  /** Override the control (e.g. "segmented" for a short select). */
  control?: string
  /** Options for chips/select when they are not in the registry (channels). */
  choices?: { value: string; label: string; disabled?: boolean; hint?: string }[]
  className?: string
  /** Extra help rendered under the control (warnings). */
  footer?: ReactNode
}

/** `<OptionField k="wlan.hidden" />`: picks the control from the option's metadata. */
export function OptionField(props: OptionFieldProps) {
  const state = useOptionState(props.k, { disabled: props.disabled })
  const { experimentalAllowed } = useConfigFormContext()
  const { control } = useFormContext()
  const {
    field: { ref: fieldRef, value, onChange, onBlur, name },
    fieldState,
  } = useController({ name: state.name, control })
  const id = useId()
  if (state.hidden) return null
  const { meta, editable, badges, lockReason } = state
  const label = props.label ?? meta.label
  const help = helpText(meta, props.help, lockReason)
  const error = fieldState.error?.message
  const kind = props.control ?? meta.control
  const common = {
    id,
    'aria-invalid': error ? true : undefined,
    'aria-describedby': describedBy(id, help, error),
    disabled: !editable,
  } as const
  const shell = (children: ReactNode, extra: { inline?: boolean; labelAs?: 'label' | 'legend' | 'span'; as?: 'div' | 'fieldset' } = {}) => (
    <FieldShell id={id} label={label} help={help} error={error} badges={badges} dirty={fieldState.isDirty} className={props.className} {...extra}>
      {children}
      {props.footer}
    </FieldShell>
  )

  switch (kind) {
    case 'switch':
      return shell(
        <Switch
          {...common}
          checked={Boolean(value)}
          onCheckedChange={(checked) => onChange(checked)}
          onBlur={onBlur}
          ref={fieldRef}
        />,
        { inline: true },
      )
    case 'select': {
      const choices: { value: string; label: string; disabled?: boolean }[] =
        props.choices ?? visibleEnum(meta, experimentalAllowed, value).map((v) => ({ value: v.value, label: v.experimental ? `${v.label} (experimental)` : v.label }))
      const numeric = meta.type === 'int'
      return shell(
        <NativeSelect
          {...common}
          name={name}
          ref={fieldRef}
          value={value == null ? '' : String(value)}
          onBlur={onBlur}
          onChange={(e) => onChange(numeric ? Number(e.target.value) : e.target.value)}
        >
          {choices.map((c) => (
            <option key={c.value} value={c.value} disabled={c.disabled}>
              {c.label}
            </option>
          ))}
        </NativeSelect>,
      )
    }
    case 'segmented': {
      const choices = props.choices ?? visibleEnum(meta, experimentalAllowed, value).map((v) => ({ value: v.value, label: v.label }))
      return shell(
        <Segmented
          aria-labelledby={`${id}-label`}
          aria-describedby={common['aria-describedby']}
          value={value == null ? '' : String(value)}
          onValueChange={(v) => onChange(meta.type === 'int' ? Number(v) : v)}
          disabled={!editable}
          options={choices}
        />,
        { labelAs: 'span' },
      )
    }
    case 'chips': {
      const choices = props.choices ?? meta.enum.map((v) => ({ value: v.value, label: v.label }))
      return shell(
        <ChipGroup
          labelledBy={`${id}-label`}
          describedBy={common['aria-describedby']}
          value={(value as (string | number)[] | undefined) ?? []}
          numeric={meta.type === '[]int'}
          onChange={onChange}
          choices={choices}
          disabled={!editable}
        />,
        { labelAs: 'span' },
      )
    }
    case 'number':
      return shell(
        <NumberInput
          {...common}
          name={name}
          inputRef={fieldRef}
          value={value as number | string | null}
          onChange={onChange}
          onBlur={onBlur}
          min={meta.min}
          max={meta.max}
          unit={meta.unit}
          placeholder={props.placeholder}
        />,
      )
    case 'slider':
      return shell(
        <SliderInput
          id={id}
          value={value as number | null}
          onChange={onChange}
          min={meta.min ?? 0}
          max={meta.max ?? 100}
          disabled={!editable}
          describedBy={common['aria-describedby']}
          label={typeof label === 'string' ? label : meta.label}
          offAtZero
        />,
        { labelAs: 'span' },
      )
    case 'password':
      return shell(
        <PasswordInput
          {...common}
          name={name}
          inputRef={fieldRef}
          value={(value as string | undefined) ?? ''}
          onChange={onChange}
          onBlur={onBlur}
          saved={props.saved}
        />,
      )
    case 'list':
      return shell(
        <ListInput
          id={id}
          value={(value as string[] | undefined) ?? []}
          onChange={onChange}
          max={meta.max}
          disabled={!editable}
          describedBy={common['aria-describedby']}
          itemLabel={meta.label.replace(/s$/, '')}
          placeholder={props.placeholder}
        />,
        { labelAs: 'span' },
      )
    case 'ssid':
    case 'text':
    case 'ip':
    default: {
      const maxBytes = meta.maxBytes
      const length = typeof value === 'string' ? utf8Length(value) : 0
      return shell(
        <div className="grid gap-1">
          <Input
            {...common}
            name={name}
            ref={fieldRef}
            value={(value as string | undefined) ?? ''}
            onChange={(e) => onChange(e.target.value)}
            onBlur={onBlur}
            placeholder={props.placeholder}
            inputMode={kind === 'ip' ? 'decimal' : undefined}
            autoCapitalize="none"
            autoCorrect="off"
            spellCheck={false}
            autoComplete="off"
          />
          {kind === 'ssid' && maxBytes ? (
            <p className={cn('justify-self-end text-xs tabular-nums', length > maxBytes ? 'text-destructive' : 'text-muted-foreground')} aria-live="polite">
              {length}/{maxBytes} bytes
            </p>
          ) : null}
        </div>,
      )
    }
  }
}

// ---- controls ----

/** Toggle chips (channels, bands): aria-pressed buttons. */
export function ChipGroup({
  value,
  onChange,
  choices,
  disabled,
  numeric,
  labelledBy,
  describedBy: describedByIds,
}: {
  value: (string | number)[]
  onChange: (value: (string | number)[]) => void
  choices: { value: string; label: string; disabled?: boolean; hint?: string }[]
  disabled?: boolean
  numeric?: boolean
  labelledBy?: string
  describedBy?: string
}) {
  const selected = new Set(value.map(String))
  return (
    <div role="group" aria-labelledby={labelledBy} aria-describedby={describedByIds} className="flex flex-wrap gap-2">
      {choices.map((choice) => {
        const on = selected.has(choice.value)
        return (
          <button
            key={choice.value}
            type="button"
            aria-pressed={on}
            title={choice.hint}
            disabled={disabled || (choice.disabled && !on)}
            onClick={() => {
              const next = on ? value.filter((v) => String(v) !== choice.value) : [...value, numeric ? Number(choice.value) : choice.value]
              onChange(numeric ? (next as number[]).toSorted((a, b) => Number(a) - Number(b)) : next)
            }}
            className={cn(
              'inline-flex min-h-11 min-w-11 cursor-pointer items-center justify-center gap-1 rounded-full border px-3.5 text-sm font-medium tabular-nums transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/30 disabled:cursor-not-allowed disabled:opacity-50 fine:md:min-h-8',
              on ? 'border-brand bg-brand-soft text-foreground' : 'border-input/60 bg-card text-muted-foreground hover:bg-accent',
            )}
          >
            {choice.label}
            {choice.hint ? <span className="text-xs font-normal text-muted-foreground">{choice.hint}</span> : null}
          </button>
        )
      })}
    </div>
  )
}

function numberText(value: number | string | null | undefined, negativeOnly: boolean): string {
  if (value == null || value === '') return ''
  return negativeOnly && typeof value === 'number' ? String(-value) : String(value)
}

/** Text to a number (negated for negative-only ranges), the raw text when not a whole number, or null when empty. */
export function parseNumber(text: string, negativeOnly: boolean): number | string | null {
  const trimmed = text.trim()
  if (!trimmed) return null
  if (!/^-?\d+$/.test(trimmed)) return trimmed
  const n = Number(trimmed)
  return negativeOnly ? -n : n
}

/**
 * Number entry with string state (§7.4 NumberField). The keypad follows the
 * bounds: digits for non-negative ranges; a fixed "−" prefix for entirely
 * negative ranges (RSSI thresholds), typed as a magnitude; a ± button when
 * the range crosses zero. Empty is null (for "empty = off" options).
 */
export function NumberInput({
  id,
  value,
  onChange,
  onBlur,
  min,
  max,
  unit,
  disabled,
  placeholder,
  inputRef,
  name,
  ...aria
}: {
  id: string
  value: number | string | null | undefined
  onChange: (value: number | string | null) => void
  onBlur?: () => void
  min: number | null
  max: number | null
  unit?: string
  disabled?: boolean
  placeholder?: string
  inputRef?: React.Ref<HTMLInputElement>
  name?: string
  'aria-invalid'?: boolean
  'aria-describedby'?: string
  'aria-label'?: string
}) {
  const negativeOnly = max != null && max < 0
  const signed = !negativeOnly && min != null && min < 0
  const [text, setText] = useState(() => numberText(value, negativeOnly))
  // Adopt outside changes (reset, restore) during render, not in an effect.
  const [seen, setSeen] = useState(value)
  if (seen !== value) {
    setSeen(value)
    if (parseNumber(text, negativeOnly) !== value) setText(numberText(value, negativeOnly))
  }

  const update = (t: string) => {
    setText(t)
    const parsed = parseNumber(t, negativeOnly)
    setSeen(parsed)
    onChange(parsed)
  }

  return (
    <div className="flex items-center gap-2">
      <div className="relative min-w-0 flex-1 md:max-w-56">
        {negativeOnly ? (
          <span aria-hidden className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-base text-muted-foreground fine:md:text-sm">
            −
          </span>
        ) : null}
        <Input
          id={id}
          name={name}
          ref={inputRef}
          value={text}
          disabled={disabled}
          placeholder={placeholder}
          onChange={(e) => update(e.target.value)}
          onBlur={onBlur}
          inputMode={signed ? 'text' : 'numeric'}
          pattern={signed ? '-?[0-9]*' : '[0-9]*'}
          autoComplete="off"
          className={cn('tabular-nums', negativeOnly && 'pl-7')}
          aria-invalid={aria['aria-invalid']}
          aria-describedby={aria['aria-describedby']}
          aria-label={aria['aria-label']}
        />
      </div>
      {signed ? (
        <Button
          variant="outline"
          size="icon-sm"
          aria-label="Change sign"
          disabled={disabled}
          onClick={() => update(text.startsWith('-') ? text.slice(1) : `-${text}`)}
        >
          ±
        </Button>
      ) : null}
      {unit ? <span className="text-sm text-muted-foreground">{unit}</span> : null}
    </div>
  )
}

/** A slider with a numeric input beside it; 0 reads "Off" (LED brightness). */
export function SliderInput({
  id,
  value,
  onChange,
  min,
  max,
  disabled,
  describedBy: describedByIds,
  label,
  offAtZero,
}: {
  id: string
  value: number | null
  onChange: (value: number | string | null) => void
  min: number
  max: number
  disabled?: boolean
  describedBy?: string
  label: string
  offAtZero?: boolean
}) {
  const numeric = typeof value === 'number' ? value : min
  return (
    <div className="grid gap-2 sm:grid-cols-[1fr_auto] sm:items-center sm:gap-4">
      <div className="grid gap-1">
        <Slider
          aria-label={label}
          aria-describedby={describedByIds}
          min={min}
          max={max}
          step={1}
          value={[numeric]}
          disabled={disabled}
          onValueChange={([v]) => onChange(v)}
        />
        <div className="flex justify-between text-xs text-muted-foreground" aria-hidden>
          <span>{offAtZero && min === 0 ? 'Off' : min}</span>
          <span>{max}</span>
        </div>
      </div>
      <div className="flex items-center gap-2">
        <NumberInput id={id} value={value} onChange={onChange} min={min} max={max} disabled={disabled} aria-label={label} aria-describedby={describedByIds} />
        {offAtZero && value === 0 ? <span className="text-sm text-muted-foreground">Off</span> : null}
      </div>
    </div>
  )
}

const UNAMBIGUOUS = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789'

/** 20 characters from an unambiguous ASCII set (§3.9 Passphrase "Generate"). */
export function generatePassphrase(length = 20): string {
  const bytes = new Uint32Array(length)
  crypto.getRandomValues(bytes)
  return Array.from(bytes, (b) => UNAMBIGUOUS[b % UNAMBIGUOUS.length]).join('')
}

/**
 * A write-only secret: show/hide, Generate and a length counter. When a value
 * is already stored, a "Change passphrase" button reveals the field (§7.4).
 */
export function PasswordInput({
  id,
  value,
  onChange,
  onBlur,
  saved,
  disabled,
  inputRef,
  name,
  generate = true,
  changeLabel = 'Change passphrase',
  ...aria
}: {
  id: string
  value: string
  onChange: (value: string) => void
  onBlur?: () => void
  saved?: boolean
  disabled?: boolean
  inputRef?: React.Ref<HTMLInputElement>
  name?: string
  generate?: boolean
  changeLabel?: string
  'aria-invalid'?: boolean
  'aria-describedby'?: string
}) {
  const [shown, setShown] = useState(false)
  const [changing, setChanging] = useState(false)
  if (saved && !changing && !value) {
    return (
      <div className="flex flex-wrap items-center gap-3">
        <span className="inline-flex items-center gap-2 text-sm text-muted-foreground">
          <KeyRound aria-hidden className="size-4" />
          Saved
        </span>
        <Button variant="outline" size="sm" disabled={disabled} onClick={() => setChanging(true)} aria-describedby={aria['aria-describedby']}>
          {changeLabel}
        </Button>
      </div>
    )
  }
  return (
    <div className="grid gap-1">
      <div className="flex items-center gap-2">
        <div className="relative min-w-0 flex-1">
          <Input
            id={id}
            name={name}
            ref={inputRef}
            type={shown ? 'text' : 'password'}
            value={value}
            disabled={disabled}
            onChange={(e) => onChange(e.target.value)}
            onBlur={onBlur}
            placeholder={saved ? 'Saved. Leave empty to keep it' : undefined}
            autoComplete="new-password"
            autoCapitalize="none"
            autoCorrect="off"
            spellCheck={false}
            className="pr-12 font-mono"
            aria-invalid={aria['aria-invalid']}
            aria-describedby={aria['aria-describedby']}
          />
          <Button
            variant="ghost"
            size="icon-sm"
            className="absolute top-1/2 right-0 -translate-y-1/2"
            aria-label={shown ? 'Hide passphrase' : 'Show passphrase'}
            aria-pressed={shown}
            onClick={() => setShown((s) => !s)}
          >
            {shown ? <EyeOff /> : <Eye />}
          </Button>
        </div>
        {generate ? (
          <Button
            variant="outline"
            disabled={disabled}
            onClick={() => {
              onChange(generatePassphrase())
              setShown(true)
            }}
          >
            <RefreshCw /> Generate
          </Button>
        ) : null}
      </div>
      <p className="justify-self-end text-xs text-muted-foreground tabular-nums" aria-live="polite">
        {value.length} characters
      </p>
    </div>
  )
}

/** A list of short strings (NTP, DNS, VLAN IDs). */
export function ListInput({
  id,
  value,
  onChange,
  max,
  disabled,
  describedBy: describedByIds,
  itemLabel,
  placeholder,
  inputMode,
}: {
  id: string
  value: string[]
  onChange: (value: string[]) => void
  max: number | null
  disabled?: boolean
  describedBy?: string
  itemLabel: string
  placeholder?: string
  inputMode?: React.HTMLAttributes<HTMLInputElement>['inputMode']
}) {
  const full = max != null && value.length >= max
  return (
    <div className="grid gap-2" aria-labelledby={`${id}-label`} role="group" aria-describedby={describedByIds}>
      {value.map((item, index) => (
        <div key={index} className="flex items-center gap-2">
          <Input
            id={index === 0 ? id : undefined}
            aria-label={`${itemLabel} ${index + 1}`}
            value={item}
            disabled={disabled}
            placeholder={placeholder}
            inputMode={inputMode}
            autoCapitalize="none"
            autoCorrect="off"
            spellCheck={false}
            onChange={(e) => onChange(value.map((v, i) => (i === index ? e.target.value : v)))}
          />
          <Button variant="ghost" size="icon-sm" aria-label={`Remove ${itemLabel} ${index + 1}`} disabled={disabled} onClick={() => onChange(value.filter((_, i) => i !== index))}>
            <Trash2 />
          </Button>
        </div>
      ))}
      <Button variant="outline" size="sm" className="justify-self-start" disabled={disabled || full} onClick={() => onChange([...value, ''])}>
        <Plus /> Add {itemLabel.toLowerCase()}
      </Button>
      {full ? <p className="text-xs text-muted-foreground">At most {max}.</p> : null}
    </div>
  )
}

/** Small step buttons used by compact numeric controls. */
export function Stepper({ onStep, disabled, label }: { onStep: (delta: number) => void; disabled?: boolean; label: string }) {
  return (
    <div className="flex gap-1">
      <Button variant="outline" size="icon-sm" aria-label={`Decrease ${label}`} disabled={disabled} onClick={() => onStep(-1)}>
        <Minus />
      </Button>
      <Button variant="outline" size="icon-sm" aria-label={`Increase ${label}`} disabled={disabled} onClick={() => onStep(1)}>
        <Plus />
      </Button>
    </div>
  )
}

/**
 * A switch bound to a form path that is part of a composite option
 * ("mpsk.enabled" of wlan.mpsk): the option supplies badges and locking.
 */
export function BoundSwitch({ name, optionKey, label, help }: { name: string; optionKey: string; label: ReactNode; help?: ReactNode }) {
  const state = useOptionState(optionKey)
  const { control } = useFormContext()
  const { field, fieldState } = useController({ name, control })
  const id = useId()
  const text = helpText(state.meta, help, state.lockReason)
  return (
    <FieldShell id={id} label={label} help={text} error={fieldState.error?.message} badges={state.badges} dirty={fieldState.isDirty} inline>
      <Switch
        id={id}
        checked={Boolean(field.value)}
        onCheckedChange={(checked) => field.onChange(checked)}
        onBlur={field.onBlur}
        disabled={!state.editable}
        aria-describedby={describedBy(id, text, fieldState.error)}
        aria-invalid={fieldState.error ? true : undefined}
      />
    </FieldShell>
  )
}

/** A visible label tied to its control by id (`children` receives the id). */
export function Labeled({ label, children, className, labelClassName = 'text-muted-foreground' }: { label: ReactNode; children: (id: string) => ReactNode; className?: string; labelClassName?: string }) {
  const id = useId()
  return (
    <div className={cn('grid gap-1 text-sm', className)}>
      <label htmlFor={id} className={labelClassName}>
        {label}
      </label>
      {children(id)}
    </div>
  )
}

/** A label and a switch on one 44 px row. */
export function SwitchRow({
  label,
  checked,
  onCheckedChange,
  disabled,
  className,
  reverse,
}: {
  label: ReactNode
  checked: boolean
  onCheckedChange: (checked: boolean) => void
  disabled?: boolean
  className?: string
  /** Switch first, then the label. */
  reverse?: boolean
}) {
  const id = useId()
  const control = <Switch id={id} checked={checked} onCheckedChange={onCheckedChange} disabled={disabled} />
  return (
    <div className={cn('flex min-h-11 items-center gap-3 text-sm', reverse ? '' : 'justify-between font-medium', className)}>
      {reverse ? control : null}
      <label htmlFor={id}>{label}</label>
      {reverse ? null : control}
    </div>
  )
}
