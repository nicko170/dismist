// Plain search-param validators (no zod in route files: validateSearch is
// part of the initial bundle, design §7.9). Unknown or invalid values are
// dropped, never thrown (§2.1), and defaults are omitted from the URL (§2.5).

export function pickEnum<T extends string>(value: unknown, allowed: readonly T[]): T | undefined {
  return typeof value === 'string' && (allowed as readonly string[]).includes(value) ? (value as T) : undefined
}

export function pickString(value: unknown, max = 200): string | undefined {
  if (typeof value === 'number') value = String(value)
  if (typeof value !== 'string') return undefined
  const trimmed = value.trim()
  return trimmed && trimmed.length <= max ? trimmed : undefined
}

/** An RFC 3339 cursor (`before=`), or nothing. */
export function pickTime(value: unknown): string | undefined {
  const text = pickString(value, 64)
  return text && !Number.isNaN(Date.parse(text)) ? text : undefined
}

/** Drops undefined keys so the URL never carries defaults. */
export function compact<T extends Record<string, unknown>>(search: T): T {
  const out: Record<string, unknown> = {}
  for (const [key, value] of Object.entries(search)) if (value !== undefined && value !== '') out[key] = value
  return out as T
}
