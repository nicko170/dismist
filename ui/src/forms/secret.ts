// Secrets in forms (design §7.4 "Secrets", §3.6 AP secret): passphrases are
// write-only (empty means keep), generated from the browser's CSPRNG, and
// AP secrets are 32 bytes pasted as hex or base64 or read from secret.bin.

// No 0/O, 1/l/I: easy to read aloud and type on a TV remote.
const ALPHABET = 'abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789'

/** A random passphrase of `length` characters (rejection sampling, no modulo bias). */
export function generatePassphrase(length = 20, random: (bytes: Uint8Array<ArrayBuffer>) => Uint8Array = (b) => crypto.getRandomValues(b)): string {
  const limit = 256 - (256 % ALPHABET.length)
  let out = ''
  while (out.length < length) {
    for (const byte of random(new Uint8Array(length * 2))) {
      if (byte < limit) out += ALPHABET[byte % ALPHABET.length]
      if (out.length === length) break
    }
  }
  return out
}

/** Bytes encoded by a pasted secret (hex, base64 or base64url), or null when it is neither. */
export function secretByteLength(value: string): number | null {
  const compact = value.replace(/[\s:]/g, '')
  if (!compact) return null
  if (/^[0-9a-fA-F]+$/.test(compact) && compact.length % 2 === 0) return compact.length / 2
  if (!/^[A-Za-z0-9+/_-]+={0,2}$/.test(compact)) return null
  try {
    const normalised = compact.replace(/-/g, '+').replace(/_/g, '/').replace(/=+$/, '')
    return atob(normalised + '='.repeat((4 - (normalised.length % 4)) % 4)).length
  } catch {
    return null
  }
}

/** Reads a binary file (secret.bin) as base64 for the API. */
export async function fileToBase64(file: Blob): Promise<string> {
  const bytes = new Uint8Array(await file.arrayBuffer())
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary)
}

/** WPA passphrase rules: 8 to 63 printable ASCII characters, or exactly 64 hex digits. */
export function passphraseProblem(value: string): string | null {
  if (/^[0-9a-fA-F]{64}$/.test(value)) return null
  if (value.length < 8) return 'Use at least 8 characters.'
  if (value.length > 63) return 'Use at most 63 characters (or exactly 64 hex digits).'
  if (!/^[\x20-\x7e]+$/.test(value)) return 'Use printable ASCII characters only (no emoji or accented letters).'
  return null
}
