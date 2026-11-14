import type { ReactNode } from 'react'
import { useWatch } from 'react-hook-form'
import type { OptionMeta } from '@/gen/options.gen'
import { ComboboxField, ChipsField, SegmentedField, SelectField, SwitchField, type Choice } from './ChoiceFields'
import type { ShadowInfo } from './Field'
import type { Inherit } from './inherit'
import { ListField, MACListField, SSHKeysField } from './ListFields'
import { NumberField, SliderField } from './NumberField'
import { PasswordField, SecretField, SSIDField, TextareaField, TextField } from './TextFields'

// A field from the option registry's metadata (design §7.4, D3, D4):
// `<OptionField option={optionMeta['wlan.hidden']} />` picks the control,
// label, help, bounds, enum values, experimental gating and visibility rule
// from gen/options.gen.ts. Controls by `control` (falling back on `type`):
//   text, ssid, password, secret, textarea, number, slider, switch, select,
//   segmented, chips, combobox, list, maclist, sshkeys.

const numericTypes = new Set(['int', 'int8', 'int16', 'int32', 'int64', 'uint', 'uint8', 'uint16', 'uint32', 'uint64', 'number', 'float', 'float64'])

export function optionChoices(option: OptionMeta): Choice<string | number>[] {
  const numeric = numericTypes.has(option.type) || option.type === '[]int'
  return option.enum.map((value) => ({
    value: numeric ? Number(value.value) : value.value,
    label: value.label || value.value,
    hint: value.help || undefined,
    experimental: value.experimental,
  }))
}

/** Whether `visibleWhen` holds for the current values (every rule must). */
export function optionVisible(option: OptionMeta, values: Record<string, unknown>): boolean {
  return option.visibleWhen.every((rule) => rule.in.includes(String(values[rule.key])))
}

export function OptionField({
  option,
  name = option.key,
  experimentalAllowed = false,
  inherit,
  shadowed,
  disabled,
  stored,
  label,
  help,
  fieldName = (key) => key,
}: {
  option: OptionMeta
  /** Form path; defaults to the option key. */
  name?: string
  /** The site's experimental switch (design P4): hides experimental options and values when off. */
  experimentalAllowed?: boolean
  inherit?: Inherit
  shadowed?: ShadowInfo
  disabled?: boolean
  /** Password and secret controls: a value is stored already. */
  stored?: boolean
  label?: ReactNode
  help?: ReactNode
  /** Maps option keys in `visibleWhen` to form paths. */
  fieldName?: (key: string) => string
}) {
  const watched = useWatch({ name: option.visibleWhen.map((rule) => fieldName(rule.key)) }) as unknown[]
  const values = Object.fromEntries(option.visibleWhen.map((rule, index) => [rule.key, watched?.[index]]))
  if (!optionVisible(option, values)) return null
  if (option.experimental && !experimentalAllowed) return null
  if (option.tier === 'readonly') disabled = true

  const common = {
    name,
    label: label ?? option.label,
    help: help ?? (option.help || undefined),
    experimental: option.experimental,
    shadowed,
    disabled: disabled || shadowed?.full,
  }
  const control = option.control || (option.type === 'bool' ? 'switch' : numericTypes.has(option.type) ? 'number' : 'text')
  const choices = optionChoices(option)
  switch (control) {
    case 'switch':
      return <SwitchField {...common} inherit={inherit} />
    case 'number':
      return <NumberField {...common} inherit={inherit} min={option.min} max={option.max} unit={option.unit || undefined} />
    case 'slider':
      return <SliderField {...common} inherit={inherit} min={option.min ?? 0} max={option.max ?? 100} unit={option.unit || undefined} offLabel={option.min === 0 ? 'Off' : undefined} />
    case 'select':
      return <SelectField {...common} inherit={inherit} choices={choices} experimentalAllowed={experimentalAllowed} />
    case 'segmented':
      return <SegmentedField {...common} inherit={inherit} choices={choices} experimentalAllowed={experimentalAllowed} />
    case 'chips':
      return <ChipsField {...common} inherit={inherit} choices={choices.filter((c) => experimentalAllowed || !c.experimental)} />
    case 'combobox':
      return <ComboboxField {...common} inherit={inherit} choices={choices.map((c) => ({ ...c, value: String(c.value) }))} />
    case 'ssid':
      return <SSIDField {...common} inherit={inherit} />
    case 'password':
      return <PasswordField {...common} stored={stored} />
    case 'secret':
      return <SecretField {...common} stored={stored} />
    case 'textarea':
      return <TextareaField {...common} inherit={inherit} />
    case 'list':
      return <ListField {...common} inherit={inherit} max={option.max ?? undefined} />
    case 'maclist':
      return <MACListField {...common} inherit={inherit} max={option.max ?? undefined} />
    case 'sshkeys':
      return <SSHKeysField {...common} />
    default:
      return <TextField {...common} inherit={inherit} maxLength={option.maxBytes ?? undefined} />
  }
}
