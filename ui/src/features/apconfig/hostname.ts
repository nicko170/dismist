// Hostname rule (design §4.4 "Hostname"): with hostname_from_name on,
// sanitise(name) = ASCII transliteration, lower case, [^a-z0-9-] → "-",
// repeated "-" collapsed, leading/trailing "-" trimmed, at most 63
// characters; empty becomes ap-<mac12>. With it off, the raw name (legacy).

export function sanitiseHostname(name: string, apId: string): string {
  const ascii = name.normalize('NFKD').replace(/[̀-ͯ]/g, '')
  const cleaned = ascii
    .toLowerCase()
    .replace(/[^a-z0-9-]/g, '-')
    .replace(/-+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 63)
    .replace(/-+$/g, '')
  return cleaned || `ap-${apId.replace(/[^0-9a-f]/gi, '').toLowerCase()}`
}

export function hostnamePreview(name: string, apId: string, fromName: boolean): string {
  return fromName ? sanitiseHostname(name, apId) : name
}
