// Firmware key paths for the expert JSON editor (design §7.4): "LED.Brightness",
// "Radios[].RRM.Channel". A segment ending in "[]" is an array of objects.
// Matching is case-insensitive, like the firmware's JSON decoder and the
// server's forbidden-path check (design §4.4, D5).

export function splitPath(path: string): string[] {
  return path.split('.').filter(Boolean)
}

function canonical(path: string): string {
  return path.replaceAll('[]', '').toLowerCase()
}

/** The forbidden entry `path` falls under (itself or a descendant), or null. */
export function forbiddenMatch(path: string, forbidden: readonly string[]): string | null {
  const target = canonical(path)
  for (const entry of forbidden) {
    const rule = canonical(entry)
    if (target === rule || target.startsWith(`${rule}.`)) return entry
  }
  return null
}

/** Distinct next segments under `parent` ("" for the root), in list order. */
export function childKeys(paths: readonly string[], parent: string): string[] {
  const prefix = parent ? `${canonical(parent)}.` : ''
  const seen = new Map<string, string>()
  for (const path of paths) {
    const segments = splitPath(path)
    const lower = canonical(path)
    if (prefix && !lower.startsWith(prefix)) continue
    const depth = prefix ? splitPath(parent).length : 0
    const next = segments[depth]
    if (!next) continue
    const name = next.replace(/\[\]$/, '')
    if (!seen.has(name.toLowerCase())) seen.set(name.toLowerCase(), name)
  }
  return [...seen.values()]
}

/** Whether the segment after `parent` is an array of objects in the path list. */
export function isArraySegment(paths: readonly string[], parent: string, key: string): boolean {
  const wanted = canonical(parent ? `${parent}.${key}` : key)
  return paths.some((path) => {
    const segments = splitPath(path)
    for (let i = 0; i < segments.length; i++) {
      const head = segments.slice(0, i + 1).join('.')
      if (canonical(head) === wanted) return segments[i].endsWith('[]')
    }
    return false
  })
}

type JsonObject = Record<string, unknown>

function isObject(value: unknown): value is JsonObject {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

/** Finds a key case-insensitively, so inserting "led.brightness" reuses an existing "LED". */
function findKey(object: JsonObject, name: string): string | undefined {
  return Object.keys(object).find((key) => key.toLowerCase() === name.toLowerCase())
}

/**
 * Adds `path` to a JSON object document with a null leaf (the operator then
 * types the value), creating parents and a one-element array for "[]"
 * segments. Existing values are kept. Throws on a document that is not a
 * JSON object.
 */
export function insertPath(doc: string, path: string): string {
  const root: unknown = doc.trim() ? JSON.parse(doc) : {}
  if (!isObject(root)) throw new Error('The patch must be a JSON object.')
  const segments = splitPath(path)
  let node: JsonObject = root
  segments.forEach((segment, index) => {
    const array = segment.endsWith('[]')
    const name = segment.replace(/\[\]$/, '')
    const key = findKey(node, name) ?? name
    const last = index === segments.length - 1
    if (array) {
      if (!Array.isArray(node[key])) node[key] = [{}]
      const list = node[key] as unknown[]
      if (!isObject(list[0])) list[0] = {}
      node = list[0] as JsonObject
      return
    }
    if (last) {
      if (!(key in node)) node[key] = null
      return
    }
    if (!isObject(node[key])) node[key] = {}
    node = node[key] as JsonObject
  })
  return JSON.stringify(root, null, 2)
}

/** Every key path in a parsed JSON value ("Radios[].Channel"). */
export function keyPaths(value: unknown, parent = ''): string[] {
  const out: string[] = []
  if (Array.isArray(value)) {
    for (const item of value) out.push(...keyPaths(item, parent))
    return [...new Set(out)]
  }
  if (!isObject(value)) return out
  for (const [key, child] of Object.entries(value)) {
    const path = parent ? `${parent}.${key}` : key
    const segment = Array.isArray(child) ? `${path}[]` : path
    out.push(segment)
    out.push(...keyPaths(child, segment))
  }
  return [...new Set(out)]
}

/** Pretty JSON for editors and views (2-space indent, like the server's). */
export function prettyJSON(value: unknown): string {
  return JSON.stringify(value ?? {}, null, 2)
}
