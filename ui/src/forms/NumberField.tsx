import { useEffect, useRef, useState, type ReactNode } from 'react'
import { useController } from 'react-hook-form'
import { PlusMinus } from './PlusMinus'
import { inputClass } from '@/components/ui/input'
import { cn } from '@/lib/utils'
import { errorText, type BoundField } from './common'
import { FieldShell, controlProps, type FieldIds } from './Field'
import { useInherit } from './inherit'
import { describeRange, formatNumberInput, inputAttributes, numberMode, parseNumberInput, sanitiseNumberText, toggleSign, type NumberMode } from './number'

// Numbers on any keyboard (design §7.4 NumberField, §7.7 rule 6). The text
// is local state while typing; the form holds a number (null when empty,
// NaN when it is not a number, which the schema reports).

export type NumberInputProps = {
  value: number | null | undefined
  onChange: (value: number | null) => void
  onBlur?: () => void
  min?: number | null
  max?: number | null
  decimal?: boolean
  unit?: ReactNode
  disabled?: boolean
  placeholder?: string
  name?: string
  inputRef?: (element: HTMLInputElement | null) => void
  ids?: Partial<FieldIds>
  className?: string
}

/** The bare control (also used inside SliderField). */
export function NumberInput({ value, onChange, onBlur, min, max, decimal = false, unit, disabled, placeholder, name, inputRef, ids, className }: NumberInputProps) {
  const mode: NumberMode = numberMode(min, max)
  const [text, setText] = useState(() => formatNumberInput(value, mode))
  const focused = useRef(false)
  // Follow outside changes (reset, Override, slider) unless the operator is typing.
  useEffect(() => {
    if (focused.current) return
    const parsed = parseNumberInput(text, mode, decimal)
    if (!(parsed === value || (Number.isNaN(parsed) && Number.isNaN(value as number)))) setText(formatNumberInput(value, mode))
    // text is deliberately not a dependency: this syncs from the value only.
    // oxlint-disable-next-line react-hooks/exhaustive-deps
  }, [value, mode, decimal])

  const attrs = inputAttributes(mode, decimal)
  const update = (next: string) => {
    const clean = sanitiseNumberText(next, mode, decimal)
    setText(clean)
    onChange(parseNumberInput(clean, mode, decimal))
  }
  return (
    <div className={cn('flex min-w-0 gap-2', className)}>
      <div className="relative min-w-0 flex-1">
        {mode === 'negative' ? (
          <span aria-hidden className="pointer-events-none absolute inset-y-0 left-3 flex items-center text-base text-muted-foreground fine:md:text-sm">
            −
          </span>
        ) : null}
        <input
          id={ids?.id}
          aria-invalid={ids?.invalid || undefined}
          aria-describedby={[ids?.describedBy, mode === 'negative' && ids?.id ? `${ids.id}-sign` : null].filter(Boolean).join(' ') || undefined}
          ref={inputRef}
          name={name}
          type="text"
          inputMode={attrs.inputMode}
          pattern={attrs.pattern}
          autoComplete="off"
          autoCorrect="off"
          spellCheck={false}
          enterKeyHint="done"
          value={text}
          placeholder={placeholder}
          disabled={disabled}
          onFocus={() => {
            focused.current = true
          }}
          onBlur={() => {
            focused.current = false
            setText(formatNumberInput(parseNumberInput(text, mode, decimal) ?? null, mode) || (text.trim() === '' ? '' : text))
            onBlur?.()
          }}
          onChange={(event) => update(event.target.value)}
          className={cn(inputClass, 'tabular-nums', mode === 'negative' && 'pl-7', unit ? 'pr-14' : undefined)}
        />
        {mode === 'negative' && ids?.id ? (
          <span id={`${ids.id}-sign`} className="sr-only">
            Negative number: type the digits only.
          </span>
        ) : null}
        {unit ? (
          <span aria-hidden className="pointer-events-none absolute inset-y-0 right-3 flex items-center text-sm text-muted-foreground">
            {unit}
          </span>
        ) : null}
      </div>
      {mode === 'signed' ? (
        <PlusMinus
          disabled={disabled}
          onClick={() => update(toggleSign(text))}
        />
      ) : null}
    </div>
  )
}

export function NumberField({
  name,
  disabled,
  markDirty = true,
  inherit,
  min,
  max,
  decimal,
  unit,
  placeholder,
  help,
  showRange = true,
  ...chrome
}: BoundField & {
  min?: number | null
  max?: number | null
  decimal?: boolean
  /** Unit text inside the field ("dBm"); also used in the range help. */
  unit?: string
  placeholder?: string
  /** Append "−95 to −50 dBm." to the help text. */
  showRange?: boolean
}) {
  const { field, fieldState } = useController({ name, disabled })
  const { slot, locked } = useInherit(name, inherit, disabled)
  const range = showRange ? describeRange(min, max, unit) : ''
  const fullHelp = range ? (
    <>
      {help}
      {help ? ' ' : null}
      <span className="whitespace-nowrap">Range {range}.</span>
    </>
  ) : (
    help
  )
  return (
    <FieldShell {...chrome} help={fullHelp} dirty={markDirty && fieldState.isDirty} error={errorText(fieldState.error)} inherit={slot}>
      {(ids) => (
        <NumberInput
          ids={{ ...controlProps(ids), id: ids.id, describedBy: ids.describedBy, invalid: ids.invalid }}
          name={field.name}
          inputRef={field.ref}
          value={locked ? null : (field.value as number | null)}
          onChange={field.onChange}
          onBlur={field.onBlur}
          min={min}
          max={max}
          decimal={decimal}
          unit={unit}
          disabled={field.disabled || locked}
          placeholder={locked ? String(inherit?.display ?? inherit?.value ?? '') : placeholder}
        />
      )}
    </FieldShell>
  )
}

/** A slider with a numeric input beside it (design §7.4; LED brightness with an "Off" stop). */
export function SliderField({
  name,
  disabled,
  markDirty = true,
  inherit,
  min,
  max,
  step = 1,
  unit,
  offLabel,
  ...chrome
}: BoundField & { min: number; max: number; step?: number; unit?: string; /** Label for the minimum ("Off"). */ offLabel?: string }) {
  const { field, fieldState } = useController({ name, disabled })
  const { slot, locked } = useInherit(name, inherit, disabled)
  const value = locked ? Number(inherit?.value ?? min) : typeof field.value === 'number' && Number.isFinite(field.value) ? field.value : min
  return (
    <FieldShell {...chrome} dirty={markDirty && fieldState.isDirty} error={errorText(fieldState.error)} inherit={slot}>
      {(ids) => (
        <div className="flex items-center gap-3">
          <div className="grid min-w-0 flex-1 gap-0.5">
            <input
              type="range"
              aria-labelledby={ids.labelId}
              aria-describedby={ids.describedBy}
              aria-valuetext={offLabel && value === min ? offLabel : `${value}${unit ? ` ${unit}` : ''}`}
              min={min}
              max={max}
              step={step}
              value={value}
              disabled={field.disabled || locked}
              onChange={(event) => field.onChange(Number(event.target.value))}
              onBlur={field.onBlur}
              className="h-11 w-full cursor-pointer accent-brand disabled:cursor-not-allowed disabled:opacity-50 fine:md:h-6"
            />
            <div aria-hidden className="flex justify-between text-xs text-muted-foreground tabular-nums">
              <span>{offLabel ?? min}</span>
              <span>{max}</span>
            </div>
          </div>
          <NumberInput
            ids={{ id: ids.id, describedBy: ids.describedBy, invalid: ids.invalid }}
            name={field.name}
            inputRef={field.ref}
            value={value}
            onChange={field.onChange}
            onBlur={field.onBlur}
            min={min}
            max={max}
            unit={unit}
            disabled={field.disabled || locked}
            className="w-28 shrink-0"
          />
        </div>
      )}
    </FieldShell>
  )
}
