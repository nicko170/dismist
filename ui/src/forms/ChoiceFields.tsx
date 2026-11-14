import { useState, type ReactNode } from 'react'
import { useController } from 'react-hook-form'
import { Check, ChevronsUpDown } from 'lucide-react'
import { ExperimentalBadge } from '@/components/app/badges'
import { Command, CommandEmpty, CommandInput, CommandItem, CommandList } from '@/components/ui/command'
import { inputClass } from '@/components/ui/input'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Segmented, SegmentedItem } from '@/components/ui/segmented'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Sheet, SheetContent } from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'
import { useMediaQuery } from '@/features/shell/hooks'
import { cn } from '@/lib/utils'
import { errorText, type BoundField } from './common'
import { FieldShell } from './Field'
import { useInherit } from './inherit'

// Choice controls bound to react-hook-form (design §7.4): switch, select,
// segmented, chips (multi-select) and a searchable combobox for long lists
// (a sheet on phones, a popover with a mouse).

export type Choice<V extends string | number = string> = {
  value: V
  label: ReactNode
  /** Plain text for search and screen readers when `label` is not a string. */
  text?: string
  hint?: ReactNode
  disabled?: boolean
  experimental?: boolean
}

function choiceText(choice: Choice<string | number>): string {
  return choice.text ?? (typeof choice.label === 'string' ? choice.label : String(choice.value))
}

export function SwitchField({
  name,
  disabled,
  markDirty = true,
  inherit,
  invert = false,
  ...chrome
}: BoundField & {
  /** Store the opposite of what the switch shows (a "Disabled" field shown as "Enabled"). */
  invert?: boolean
}) {
  const { field, fieldState } = useController({ name, disabled })
  const { slot, locked } = useInherit(name, inherit, disabled)
  const raw = locked ? inherit?.value : field.value
  const checked = invert ? !raw : Boolean(raw)
  return (
    <FieldShell {...chrome} layout="inline" dirty={markDirty && fieldState.isDirty} error={errorText(fieldState.error)} inherit={slot}>
      {(ids) => (
        <Switch
          id={ids.id}
          ref={field.ref}
          name={field.name}
          aria-describedby={ids.describedBy}
          aria-invalid={ids.invalid || undefined}
          checked={checked}
          disabled={field.disabled || locked}
          onCheckedChange={(on) => field.onChange(invert ? !on : on)}
          onBlur={field.onBlur}
        />
      )}
    </FieldShell>
  )
}

/** Hide experimental choices unless the site allows them, but keep a stored one visible. */
export function visibleChoices<V extends string | number>(choices: readonly Choice<V>[], experimentalAllowed: boolean, current: unknown): Choice<V>[] {
  return choices.filter((choice) => experimentalAllowed || !choice.experimental || choice.value === current)
}

export function SelectField<V extends string | number = string>({
  name,
  disabled,
  markDirty = true,
  inherit,
  choices,
  placeholder = 'Choose…',
  experimentalAllowed = true,
  ...chrome
}: BoundField & { choices: readonly Choice<V>[]; placeholder?: string; experimentalAllowed?: boolean }) {
  const { field, fieldState } = useController({ name, disabled })
  const { slot, locked } = useInherit(name, inherit, disabled)
  const shown = visibleChoices(choices, experimentalAllowed, field.value)
  const current = locked ? inherit?.value : field.value
  const numeric = typeof choices[0]?.value === 'number'
  return (
    <FieldShell {...chrome} dirty={markDirty && fieldState.isDirty} error={errorText(fieldState.error)} inherit={slot}>
      {(ids) => (
        <Select
          name={field.name}
          value={current === null || current === undefined ? '' : String(current)}
          disabled={field.disabled || locked}
          onValueChange={(next) => field.onChange(numeric ? Number(next) : next)}
        >
          <SelectTrigger id={ids.id} ref={field.ref} aria-invalid={ids.invalid || undefined} aria-describedby={ids.describedBy} onBlur={field.onBlur}>
            <SelectValue placeholder={placeholder} />
          </SelectTrigger>
          <SelectContent>
            {shown.map((choice) => (
              <SelectItem key={String(choice.value)} value={String(choice.value)} disabled={choice.disabled}>
                <span className="flex items-center gap-2">
                  {choice.label}
                  {choice.experimental ? <span className="text-xs text-brand">(experimental)</span> : null}
                </span>
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      )}
    </FieldShell>
  )
}

export function SegmentedField<V extends string | number = string>({
  name,
  disabled,
  markDirty = true,
  inherit,
  choices,
  experimentalAllowed = true,
  ...chrome
}: BoundField & { choices: readonly Choice<V>[]; experimentalAllowed?: boolean }) {
  const { field, fieldState } = useController({ name, disabled })
  const { slot, locked } = useInherit(name, inherit, disabled)
  const shown = visibleChoices(choices, experimentalAllowed, field.value)
  const current = locked ? inherit?.value : field.value
  const numeric = typeof choices[0]?.value === 'number'
  const selected = shown.find((choice) => String(choice.value) === String(current))
  return (
    <FieldShell {...chrome} labelFor={false} dirty={markDirty && fieldState.isDirty} error={errorText(fieldState.error)} inherit={slot}>
      {(ids) => (
        <div className="grid gap-1.5">
          <Segmented
            ref={field.ref}
            aria-labelledby={ids.labelId}
            aria-describedby={ids.describedBy}
            aria-invalid={ids.invalid || undefined}
            value={current === null || current === undefined ? '' : String(current)}
            disabled={field.disabled || locked}
            onValueChange={(next) => field.onChange(numeric ? Number(next) : next)}
            onBlur={field.onBlur}
            className="w-full sm:w-auto"
          >
            {shown.map((choice) => (
              <SegmentedItem key={String(choice.value)} value={String(choice.value)} disabled={choice.disabled}>
                {choice.label}
              </SegmentedItem>
            ))}
          </Segmented>
          {selected?.experimental ? <ExperimentalBadge /> : null}
          {selected?.hint ? <p className="text-sm text-muted-foreground">{selected.hint}</p> : null}
        </div>
      )}
    </FieldShell>
  )
}

/**
 * Several of a few (channels, bands): toggle chips with aria-pressed. The
 * value is an array in the order of `choices`, so the stored list is stable.
 */
export function ChipsField<V extends string | number = string>({
  name,
  disabled,
  markDirty = true,
  inherit,
  choices,
  min,
  ...chrome
}: BoundField & { choices: readonly Choice<V>[]; /** Keep at least this many selected. */ min?: number }) {
  const { field, fieldState } = useController({ name, disabled })
  const { slot, locked } = useInherit(name, inherit, disabled)
  const raw = (locked ? inherit?.value : field.value) as V[] | null | undefined
  const selected = new Set((raw ?? []).map(String))
  const toggle = (value: V) => {
    const next = new Set(selected)
    if (next.has(String(value))) {
      if (min !== undefined && next.size <= min) return
      next.delete(String(value))
    } else next.add(String(value))
    field.onChange(choices.filter((choice) => next.has(String(choice.value))).map((choice) => choice.value))
  }
  return (
    <FieldShell {...chrome} labelFor={false} dirty={markDirty && fieldState.isDirty} error={errorText(fieldState.error)} inherit={slot}>
      {(ids) => (
        <ul className="flex flex-wrap gap-2" aria-describedby={ids.describedBy}>
          {choices.map((choice) => {
            const on = selected.has(String(choice.value))
            return (
              <li key={String(choice.value)}>
                <button
                  type="button"
                  aria-pressed={on}
                  disabled={field.disabled || locked || choice.disabled}
                  onClick={() => toggle(choice.value)}
                  onBlur={field.onBlur}
                  className={cn(
                    'inline-flex h-11 min-w-11 cursor-pointer items-center justify-center gap-1 rounded-md border px-3 text-sm tabular-nums transition-colors duration-150 outline-none fine:md:h-8 fine:md:min-w-9',
                    'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring disabled:cursor-not-allowed disabled:opacity-50',
                    on ? 'border-brand bg-brand-soft font-medium text-foreground' : 'border-input/60 bg-card text-muted-foreground hover:text-foreground',
                  )}
                >
                  {on ? <Check aria-hidden className="size-3.5 text-brand" /> : null}
                  {choice.label}
                  {choice.hint ? <span className="text-xs text-muted-foreground">{choice.hint}</span> : null}
                </button>
              </li>
            )
          })}
        </ul>
      )}
    </FieldShell>
  )
}

/** Long lists (timezones, countries): searchable; a sheet on phones and touch screens. */
export function ComboboxField({
  name,
  disabled,
  markDirty = true,
  inherit,
  choices,
  placeholder = 'Choose…',
  searchPlaceholder = 'Search',
  ...chrome
}: BoundField & { choices: readonly Choice<string>[]; placeholder?: string; searchPlaceholder?: string }) {
  const { field, fieldState } = useController({ name, disabled })
  const { slot, locked } = useInherit(name, inherit, disabled)
  const [open, setOpen] = useState(false)
  const desktop = useMediaQuery('(min-width: 48rem) and (pointer: fine)')
  const current = (locked ? inherit?.value : field.value) as string | null | undefined
  const selected = choices.find((choice) => choice.value === current)
  const pick = (value: string) => {
    field.onChange(value)
    setOpen(false)
    field.onBlur()
  }
  const list = (
    <Command
      filter={(value, search, keywords) => {
        const hay = `${value} ${(keywords ?? []).join(' ')}`.toLowerCase()
        return hay.includes(search.toLowerCase()) ? 1 : 0
      }}
    >
      <CommandInput placeholder={searchPlaceholder} autoCapitalize="none" autoCorrect="off" spellCheck={false} />
      <CommandList className={cn(!desktop && 'max-h-[60dvh]')}>
        <CommandEmpty>No match.</CommandEmpty>
        {choices.map((choice) => (
          <CommandItem key={choice.value} value={choice.value} keywords={[choiceText(choice)]} disabled={choice.disabled} onSelect={() => pick(choice.value)}>
            <Check aria-hidden className={cn('size-4 shrink-0 text-brand', choice.value !== current && 'invisible')} />
            <span className="grid min-w-0 flex-1">
              <span className="truncate">{choice.label}</span>
              {choice.hint ? <span className="text-xs text-muted-foreground">{choice.hint}</span> : null}
            </span>
          </CommandItem>
        ))}
      </CommandList>
    </Command>
  )
  return (
    <FieldShell {...chrome} dirty={markDirty && fieldState.isDirty} error={errorText(fieldState.error)} inherit={slot}>
      {(ids) => {
        const trigger = (
          <button
            type="button"
            id={ids.id}
            ref={field.ref}
            aria-haspopup="dialog"
            aria-expanded={open}
            aria-describedby={ids.describedBy}
            disabled={field.disabled || locked}
            onClick={desktop ? undefined : () => setOpen(true)}
            className={cn(inputClass, 'cursor-pointer items-center justify-between gap-2 text-left')}
          >
            <span className={cn('truncate', !selected && 'text-muted-foreground')}>{selected ? choiceText(selected) : (current ?? placeholder)}</span>
            <ChevronsUpDown aria-hidden className="size-4 shrink-0 text-muted-foreground" />
          </button>
        )
        if (desktop) {
          return (
            <Popover open={open} onOpenChange={setOpen}>
              <PopoverTrigger asChild>{trigger}</PopoverTrigger>
              <PopoverContent className="w-[var(--radix-popover-trigger-width)] min-w-64 p-0">{list}</PopoverContent>
            </Popover>
          )
        }
        return (
          <>
            {trigger}
            <Sheet open={open} onOpenChange={setOpen}>
              <SheetContent title={typeof chrome.label === 'string' ? chrome.label : 'Choose'}>{list}</SheetContent>
            </Sheet>
          </>
        )
      }}
    </FieldShell>
  )
}

/** A single checkbox with its label beside it (acknowledgements, "Also delete history"). */
export function CheckboxField({ name, disabled, label, help }: { name: string; disabled?: boolean; label: ReactNode; help?: ReactNode }) {
  const { field, fieldState } = useController({ name, disabled })
  const error = errorText(fieldState.error)
  return (
    <div className="grid gap-1">
      <label className="flex min-h-11 cursor-pointer items-start gap-3 py-1.5 text-sm fine:md:min-h-0">
        <input
          type="checkbox"
          ref={(element) => field.ref(element)}
          name={field.name}
          checked={Boolean(field.value)}
          onChange={(event) => field.onChange(event.target.checked)}
          onBlur={field.onBlur}
          disabled={field.disabled}
          aria-invalid={error ? true : undefined}
          className="mt-0.5 size-5 shrink-0 cursor-pointer accent-brand fine:md:size-4"
        />
        <span className="grid gap-0.5">
          <span>{label}</span>
          {help ? <span className="text-muted-foreground">{help}</span> : null}
        </span>
      </label>
      {error ? (
        <p role="alert" className="text-sm font-medium text-destructive">
          {error}
        </p>
      ) : null}
    </div>
  )
}
