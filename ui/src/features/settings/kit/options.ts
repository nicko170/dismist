import { z } from 'zod'
import { optionMeta, type EnumValueMeta, type OptionMeta } from '@/gen/options.gen'
import { utf8Length } from '@/lib/format'
import { provisionalOptions } from './provisional-options'

// Option metadata access (design §4.2, §7.4). Forms never hard-code labels,
// bounds, enums or experimental flags: they come from the registry that
// workstream A generates into gen/options.gen.ts. Until an option is
// generated, the provisional transcription of the design tables stands in.

export function getOption(key: string): OptionMeta {
  const meta = optionMeta[key] ?? provisionalOptions[key]
  if (!meta) throw new Error(`unknown option ${key}`)
  return meta
}

export function hasOption(key: string): boolean {
  return Boolean(optionMeta[key] ?? provisionalOptions[key])
}

/** The form field name of an option: its key without the scope prefix. */
export function fieldName(meta: OptionMeta): string {
  return meta.key.slice(meta.scope.length + 1)
}

/** "needs-live-test" on the option or on the chosen enum value (§4.2). */
export function isExperimentalValue(meta: OptionMeta, value: unknown): boolean {
  if (meta.experimental) return true
  if (!meta.enum.length) return false
  const chosen = meta.enum.find((v) => v.value === String(value))
  return Boolean(chosen?.experimental)
}

/** Enum values offered: experimental ones only when the site allows them, or already chosen. */
export function visibleEnum(meta: OptionMeta, experimentalAllowed: boolean, current: unknown): EnumValueMeta[] {
  return meta.enum.filter((v) => !v.experimental || experimentalAllowed || v.value === String(current))
}

function unitSuffix(meta: OptionMeta) {
  return meta.unit ? ` ${meta.unit}` : ''
}

export function intMessage(meta: OptionMeta) {
  if (meta.min != null && meta.max != null) return `Enter a whole number from ${meta.min} to ${meta.max}${unitSuffix(meta)}.`
  if (meta.min != null) return `Enter a whole number of at least ${meta.min}${unitSuffix(meta)}.`
  if (meta.max != null) return `Enter a whole number up to ${meta.max}${unitSuffix(meta)}.`
  return 'Enter a whole number.'
}

/** An integer within the option's bounds. Number fields pass raw text when it is not a number. */
export function intSchema(meta: OptionMeta) {
  const message = intMessage(meta)
  let schema = z.number({ error: message }).int({ error: message })
  if (meta.min != null) schema = schema.min(meta.min, { error: message })
  if (meta.max != null) schema = schema.max(meta.max, { error: message })
  return schema
}

export function bytesSchema(meta: OptionMeta, required: boolean) {
  const max = meta.maxBytes ?? meta.max ?? null
  let schema = z.string()
  if (required) schema = schema.min(1, { error: `Enter the ${meta.label.toLowerCase()}.` })
  if (max != null) {
    schema = schema.refine((v) => utf8Length(v) <= max, {
      error: meta.maxBytes ? `At most ${max} bytes.` : `At most ${max} characters.`,
    })
  }
  return schema
}

export function enumSchema(meta: OptionMeta) {
  const values = meta.enum.map((v) => v.value)
  return z.string().refine((v) => values.includes(v), { error: `Choose one of the ${meta.label.toLowerCase()} options.` })
}

/**
 * The zod schema for one leaf option, from its metadata. Composite controls
 * (servers, power, channels…) get their schema in the feature's schema.ts.
 */
export function leafSchema(key: string, options: { nullable?: boolean; required?: boolean } = {}) {
  const meta = getOption(key)
  let schema: z.ZodType
  switch (meta.type) {
    case 'bool':
      schema = z.boolean()
      break
    case 'int':
      schema = meta.enum.length ? z.number().refine((v) => meta.enum.some((e) => e.value === String(v)), { error: `Choose a ${meta.label.toLowerCase()}.` }) : intSchema(meta)
      break
    case 'string':
      schema = meta.enum.length ? enumSchema(meta) : bytesSchema(meta, options.required ?? false)
      break
    case '[]string':
      schema = meta.enum.length
        ? z.array(z.string()).refine((list) => list.every((v) => meta.enum.some((e) => e.value === v)))
        : z.array(z.string())
      if (meta.max != null) schema = (schema as z.ZodArray<z.ZodString>).max(meta.max, { error: `At most ${meta.max}.` })
      break
    case '[]int':
      schema = z.array(z.number().int())
      break
    default:
      schema = z.unknown()
  }
  return options.nullable ? schema.nullable() : schema
}

/** Default values of a scope's options for a new object (§4.6: new objects get research defaults). */
export function defaultsFor(keys: string[]): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const key of keys) {
    const meta = getOption(key)
    if (meta.default === null || meta.default === undefined) continue
    setPath(out, fieldName(meta).split('.'), structuredClone(meta.default))
  }
  return out
}

function setPath(target: Record<string, unknown>, path: string[], value: unknown) {
  let node = target
  path.slice(0, -1).forEach((part) => {
    if (typeof node[part] !== 'object' || node[part] === null) node[part] = {}
    node = node[part] as Record<string, unknown>
  })
  node[path[path.length - 1]] = value
}

export function getPath(source: unknown, path: string): unknown {
  return path.split('.').reduce<unknown>((node, part) => (node && typeof node === 'object' ? (node as Record<string, unknown>)[part] : undefined), source)
}

/** True when `visibleWhen` conditions hold for the form values (dependency keys use the same scope). */
export function conditionsHold(meta: OptionMeta, values: unknown): boolean {
  return meta.visibleWhen.every((condition) => {
    const dep = condition.key.startsWith(`${meta.scope}.`) ? condition.key.slice(meta.scope.length + 1) : condition.key
    const current = getPath(values, dep)
    return condition.in.includes(String(current))
  })
}

export const disruptionLabel: Record<string, string> = {
  none: 'No disruption',
  wlan_restart: 'WLAN restarts',
  radio_bounce: 'Radio restarts',
  all_radios_bounce: 'Every radio restarts',
  stranding_risk: 'May strand the AP',
}
