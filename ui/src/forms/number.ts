// NumberField's pure half (design §7.4, §7.7 rule 6). The field keeps a
// string while editing and stores a number. The keypad follows the bounds,
// because the iOS digit pad has no minus sign:
//   unsigned  (min >= 0):        inputMode numeric, pattern [0-9]*
//   negative  (max <= 0, min < 0): a fixed "−" prefix; the magnitude is typed
//                                  on the digit pad and stored negated
//   signed    (crosses zero):     inputMode text, pattern -?[0-9]*, plus a ± button
// Decimal options use inputMode decimal in the unsigned and negative cases.

export type NumberMode = 'unsigned' | 'negative' | 'signed'

export function numberMode(min: number | null | undefined, max: number | null | undefined): NumberMode {
  if (min == null || min >= 0) return 'unsigned'
  if (max != null && max <= 0) return 'negative'
  return 'signed'
}

export function inputAttributes(mode: NumberMode, decimal = false): { inputMode: 'numeric' | 'decimal' | 'text'; pattern: string } {
  if (mode === 'signed') return { inputMode: 'text', pattern: decimal ? '-?[0-9]*[.,]?[0-9]*' : '-?[0-9]*' }
  return decimal ? { inputMode: 'decimal', pattern: '[0-9]*[.,]?[0-9]*' } : { inputMode: 'numeric', pattern: '[0-9]*' }
}

/** Text shown in the field for a stored value. Negative mode shows the magnitude. */
export function formatNumberInput(value: number | null | undefined, mode: NumberMode): string {
  if (value == null || !Number.isFinite(value)) return ''
  if (mode === 'negative') return String(Math.abs(value))
  return String(value)
}

/**
 * The stored value for what was typed: null for empty, NaN for text that is
 * not a number (the schema reports it). Accepts the typographic minus and a
 * decimal comma.
 */
export function parseNumberInput(text: string, mode: NumberMode, decimal = false): number | null {
  const cleaned = text.trim().replace(/−/g, '-').replace(',', '.')
  if (cleaned === '' || cleaned === '-') return null
  const shape = decimal ? /^-?\d*\.?\d+$|^-?\d+\.$/ : /^-?\d+$/
  if (!shape.test(cleaned)) return Number.NaN
  const value = Number(cleaned)
  if (mode === 'negative') return value === 0 ? 0 : -Math.abs(value)
  if (mode === 'unsigned' && value < 0) return Number.NaN
  return value
}

/** Keeps only what the mode can type (drops letters as they are entered). */
export function sanitiseNumberText(text: string, mode: NumberMode, decimal = false): string {
  let out = text.replace(/−/g, '-')
  out = out.replace(decimal ? /[^0-9.,-]/g : /[^0-9-]/g, '')
  if (mode !== 'signed') out = out.replace(/-/g, '')
  else out = out.replace(/(?!^)-/g, '')
  return out
}

/** The ± button: flips the sign of the text ("12" ⇄ "-12"). */
export function toggleSign(text: string): string {
  const trimmed = text.trim()
  if (trimmed.startsWith('-')) return trimmed.slice(1)
  return `-${trimmed}`
}

/** "−95 to −50 dBm" for help text and errors. */
export function describeRange(min: number | null | undefined, max: number | null | undefined, unit = ''): string {
  const f = (n: number) => (n < 0 ? `−${Math.abs(n)}` : String(n))
  const suffix = unit ? ` ${unit}` : ''
  if (min != null && max != null) return `${f(min)} to ${f(max)}${suffix}`
  if (min != null) return `at least ${f(min)}${suffix}`
  if (max != null) return `at most ${f(max)}${suffix}`
  return ''
}
