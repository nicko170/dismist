// MACs and AP IDs use the lower-case dashed form everywhere: in URLs, in the
// API and on screen (design §2.3).

const HEX_PAIRS = /^[0-9a-f]{12}$/

/** Normalises "AA:BB:CC:DD:EE:FF", "aabb.ccdd.eeff" and friends to "aa-bb-cc-dd-ee-ff". */
export function normaliseMac(value: string): string | null {
  const compact = value.trim().toLowerCase().replace(/[\s:.-]/g, '')
  if (!HEX_PAIRS.test(compact)) return null
  return compact.match(/../g)!.join('-')
}

export function isCanonicalMac(value: string): boolean {
  return normaliseMac(value) === value
}

/** A locally administered (randomised, "private") address has bit 1 of the first octet set. */
export function isPrivateMac(value: string): boolean {
  const mac = normaliseMac(value)
  if (!mac) return false
  return (parseInt(mac.slice(0, 2), 16) & 0x02) !== 0
}
