// Search-param validation for the config routes (design §2.5): unknown or
// invalid values are dropped instead of crashing a screen. Plain functions,
// not zod, so validateSearch (which stays in the entry chunk) adds no weight
// to the initial bundle (§7.9 budget).

export function oneOf<T extends string>(value: unknown, allowed: readonly T[]): T | undefined {
  return typeof value === 'string' && (allowed as readonly string[]).includes(value) ? (value as T) : undefined
}

export function text(value: unknown): string | undefined {
  return typeof value === 'string' && value.length > 0 && value.length <= 128 ? value : typeof value === 'number' ? String(value) : undefined
}
