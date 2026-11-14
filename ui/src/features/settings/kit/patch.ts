// JSON merge patch helpers (RFC 7386, design §4.8 "PATCH semantics"): the
// form sends only what changed. Arrays are compared and replaced whole.

type Json = unknown

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

export function deepEqual(a: Json, b: Json): boolean {
  if (a === b) return true
  if (Array.isArray(a) && Array.isArray(b)) return a.length === b.length && a.every((v, i) => deepEqual(v, b[i]))
  if (isPlainObject(a) && isPlainObject(b)) {
    const keys = new Set([...Object.keys(a), ...Object.keys(b)])
    for (const key of keys) if (!deepEqual(a[key], b[key])) return false
    return true
  }
  return false
}

/**
 * The merge patch that turns `base` into `next`: changed leaves, arrays
 * whole, removed keys as null. Returns undefined when nothing changed.
 */
export function mergePatch(base: Json, next: Json): Json | undefined {
  if (deepEqual(base, next)) return undefined
  if (isPlainObject(base) && isPlainObject(next)) {
    const out: Record<string, unknown> = {}
    for (const key of new Set([...Object.keys(base), ...Object.keys(next)])) {
      if (!(key in next)) {
        out[key] = null
        continue
      }
      const child = key in base ? mergePatch(base[key], next[key]) : next[key]
      if (child !== undefined) out[key] = child
    }
    return Object.keys(out).length ? out : undefined
  }
  return next === undefined ? null : next
}

/** Dotted paths of every changed leaf (arrays count once). */
export function changedPaths(base: Json, next: Json, prefix = ''): string[] {
  if (deepEqual(base, next)) return []
  if (isPlainObject(base) && isPlainObject(next)) {
    return [...new Set([...Object.keys(base), ...Object.keys(next)])].flatMap((key) =>
      changedPaths(base[key], next[key], prefix ? `${prefix}.${key}` : key),
    )
  }
  return [prefix || '(root)']
}

/** Removes keys whose value is undefined, recursively (forms leave some unset). */
export function compact<T>(value: T): T {
  if (Array.isArray(value)) return value.map(compact) as T
  if (isPlainObject(value)) {
    return Object.fromEntries(
      Object.entries(value)
        .filter(([, v]) => v !== undefined)
        .map(([k, v]) => [k, compact(v)]),
    ) as T
  }
  return value
}
