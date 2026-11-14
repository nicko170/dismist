// Small validators shared by the config forms. The server runs the same
// rules (§4.4); these give inline feedback.

export function isIPv4(value: string): boolean {
  const parts = value.split('.')
  return parts.length === 4 && parts.every((p) => /^\d{1,3}$/.test(p) && Number(p) <= 255 && (p === '0' || !p.startsWith('0')))
}

export function isIPv6(value: string): boolean {
  if (!value.includes(':') || !/^[0-9a-fA-F:.]+$/.test(value)) return false
  const halves = value.split('::')
  if (halves.length > 2) return false
  const split = (part: string) => (part === '' ? [] : part.split(':'))
  const groups = [...split(halves[0]), ...(halves.length === 2 ? split(halves[1]) : [])]
  let count = groups.length
  const last = groups.at(-1)
  if (last?.includes('.')) {
    if (!isIPv4(last)) return false
    groups.pop()
    count += 1
  }
  if (!groups.every((g) => /^[0-9a-fA-F]{1,4}$/.test(g))) return false
  return halves.length === 2 ? count < 8 : count === 8
}

export function isIP(value: string): boolean {
  return isIPv4(value) || isIPv6(value)
}

/** A DNS name: labels of [A-Za-z0-9-], not starting or ending with "-", at most 253 characters. */
export function isHostname(value: string): boolean {
  if (!value || value.length > 253) return false
  return value
    .replace(/\.$/, '')
    .split('.')
    .every((label) => /^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$/.test(label))
}

export function isHostOrIP(value: string): boolean {
  return isIP(value) || isHostname(value)
}

/** NTP: hostname or IP, and not a broadcast or multicast address (§3.11 Network). */
export function isNTPServer(value: string): boolean {
  if (isIPv4(value)) {
    const first = Number(value.split('.')[0])
    return value !== '255.255.255.255' && !(first >= 224 && first <= 239)
  }
  if (isIPv6(value)) return !value.toLowerCase().startsWith('ff')
  return isHostname(value)
}

/** Printable ASCII, 0x20 to 0x7E. */
export function isPrintableASCII(value: string): boolean {
  return /^[\x20-\x7e]*$/.test(value)
}

// ---- SSH keys (§3.11 Access) ----

const KEY_TYPES = new Set([
  'ssh-ed25519',
  'ssh-rsa',
  'ecdsa-sha2-nistp256',
  'ecdsa-sha2-nistp384',
  'ecdsa-sha2-nistp521',
  'sk-ssh-ed25519@openssh.com',
  'sk-ecdsa-sha2-nistp256@openssh.com',
])

export type ParsedKey = { type: string; blob: string; comment: string; key: string }
export type KeyParse = { ok: true; key: ParsedKey } | { ok: false; error: string; line: string }

function base64Bytes(value: string): Uint8Array | null {
  try {
    const raw = atob(value)
    return Uint8Array.from(raw, (c) => c.charCodeAt(0))
  } catch {
    return null
  }
}

/** One authorized_keys line (options are not accepted; the comment is kept). */
export function parseSSHKey(line: string): KeyParse {
  const trimmed = line.trim()
  if (/PRIVATE KEY/.test(trimmed)) return { ok: false, error: 'This is a private key. Paste the public key (the .pub file) instead.', line: trimmed }
  const [type, blob, ...rest] = trimmed.split(/\s+/)
  if (!type || !blob) return { ok: false, error: 'Not an OpenSSH public key.', line: trimmed }
  if (!KEY_TYPES.has(type)) return { ok: false, error: `Unsupported key type “${type.slice(0, 32)}”.`, line: trimmed }
  const bytes = base64Bytes(blob)
  if (!bytes || bytes.length < 8) return { ok: false, error: 'The key data is not valid base64.', line: trimmed }
  const nameLength = (bytes[0] << 24) | (bytes[1] << 16) | (bytes[2] << 8) | bytes[3]
  const embedded = new TextDecoder().decode(bytes.slice(4, 4 + nameLength))
  if (embedded !== type) return { ok: false, error: 'The key data does not match its type.', line: trimmed }
  const comment = rest.join(' ')
  return { ok: true, key: { type, blob, comment, key: `${type} ${blob}` } }
}

/** Parses pasted text (several lines). Blank lines and # comments are skipped. */
export function parseSSHKeys(text: string): { keys: ParsedKey[]; errors: { line: string; error: string }[] } {
  const keys: ParsedKey[] = []
  const errors: { line: string; error: string }[] = []
  for (const line of text.split(/\r?\n/)) {
    if (!line.trim() || line.trim().startsWith('#')) continue
    const parsed = parseSSHKey(line)
    if (parsed.ok) keys.push(parsed.key)
    else errors.push({ line: parsed.line.length > 48 ? `${parsed.line.slice(0, 45)}…` : parsed.line, error: parsed.error })
  }
  return { keys, errors }
}

/** "SHA256:…" as ssh-keygen -l prints it. */
export async function sshFingerprint(blob: string): Promise<string> {
  const bytes = base64Bytes(blob)
  if (!bytes || !globalThis.crypto?.subtle) return ''
  const digest = new Uint8Array(await crypto.subtle.digest('SHA-256', bytes as Uint8Array<ArrayBuffer>))
  const b64 = btoa(String.fromCharCode(...digest)).replace(/=+$/, '')
  return `SHA256:${b64}`
}

// ---- secrets ----

/** The AP secret: exactly 32 bytes as hex or base64 (§3.6 General). */
export function secretByteLength(value: string): number | null {
  const compact = value.replace(/[\s:]/g, '')
  if (/^[0-9a-fA-F]+$/.test(compact) && compact.length % 2 === 0) return compact.length / 2
  const normalised = compact.replace(/-/g, '+').replace(/_/g, '/')
  const bytes = base64Bytes(normalised + '='.repeat((4 - (normalised.length % 4)) % 4))
  return bytes ? bytes.length : null
}

/** File bytes to base64 (secret.bin upload). */
export function bytesToBase64(buffer: ArrayBuffer): string {
  return btoa(String.fromCharCode(...new Uint8Array(buffer)))
}

// ---- times ----

/** "08:00-17:30" with start before end. */
export function isTimeRange(value: string): boolean {
  const match = value.trim().match(/^([01]\d|2[0-3]):([0-5]\d)-([01]\d|2[0-4]):([0-5]\d)$/)
  if (!match) return false
  const start = Number(match[1]) * 60 + Number(match[2])
  const end = Number(match[3]) * 60 + Number(match[4])
  return end > start && end <= 24 * 60
}
