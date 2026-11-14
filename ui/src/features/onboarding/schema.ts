import { z } from 'zod'
import { normaliseMac } from '@/lib/mac'
import type { APInput } from '@/gen/api.gen'

// The Register form (design §3.15): ID in any MAC spelling, a name, a site,
// the 32-byte EEPROM secret as hex or base64 (or secret.bin), and the
// optional source pin, which DNS mode requires. The server runs the same
// checks; these only save a round trip.

export const SECRET_BYTES = 32

/** Decodes a pasted secret: hex (spaces and colons allowed) or base64 (standard or URL-safe, padding optional). */
export function decodeSecret(value: string): Uint8Array | null {
  const compact = value.replace(/[\s:]/g, '')
  if (!compact) return null
  if (/^[0-9a-fA-F]+$/.test(compact) && compact.length % 2 === 0 && compact.length === SECRET_BYTES * 2) {
    const bytes = new Uint8Array(compact.length / 2)
    for (let i = 0; i < bytes.length; i++) bytes[i] = parseInt(compact.slice(i * 2, i * 2 + 2), 16)
    return bytes
  }
  if (!/^[A-Za-z0-9+/_-]+={0,2}$/.test(compact)) return null
  const standard = compact.replace(/-/g, '+').replace(/_/g, '/').replace(/=+$/, '')
  if (standard.length % 4 === 1) return null
  try {
    const binary = atob(standard + '='.repeat((4 - (standard.length % 4)) % 4))
    return Uint8Array.from(binary, (c) => c.charCodeAt(0))
  } catch {
    return null
  }
}

export function toHex(bytes: Uint8Array): string {
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
}

/** An IPv4 or IPv6 literal (no brackets, no zone). */
export function isIPLiteral(value: string): boolean {
  const v = value.trim()
  const v4 = v.match(/^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/)
  if (v4) return v4.slice(1).every((octet) => Number(octet) <= 255 && String(Number(octet)) === octet)
  if (!v.includes(':') || /[^0-9a-fA-F:.]/.test(v)) return false
  try {
    return new URL(`http://[${v}]/`).hostname.length > 2
  } catch {
    return false
  }
}

export type RegisterContext = {
  /** The controller runs DNS mode: the pin is required (design §3.15). */
  dnsMode: boolean
  /** IDs that already have a record. */
  registered: ReadonlySet<string>
}

export function registerSchema({ dnsMode, registered }: RegisterContext) {
  return z.object({
    id: z
      .string()
      .trim()
      .min(1, 'Enter the AP’s ID (its eth0 MAC address).')
      .refine((value) => normaliseMac(value) !== null, {
        message: 'Use 12 hex digits, such as aa:bb:cc:dd:ee:ff, AA-BB-CC-DD-EE-FF or aabbccddeeff.',
      })
      .refine((value) => !registered.has(normaliseMac(value) ?? ''), { message: 'An AP with this ID is already registered.' }),
    name: z.string().trim().min(1, 'Give the AP a name.').max(64, 'Use at most 64 characters.'),
    site_id: z.string().min(1, 'Choose a site.'),
    secret: z
      .string()
      .trim()
      .min(1, 'Paste the secret or upload secret.bin.')
      .refine((value) => decodeSecret(value)?.length === SECRET_BYTES, {
        message: `The secret must be exactly ${SECRET_BYTES} bytes, as hex (64 characters) or base64.`,
      }),
    pinned_ip: z
      .string()
      .trim()
      .refine((value) => value === '' || isIPLiteral(value), { message: 'Enter an IP address, or leave it empty.' })
      .refine((value) => !dnsMode || value !== '', {
        message: 'Required in DNS mode: the controller answers DNS only for pinned addresses.',
      }),
  })
}

export type RegisterValues = z.infer<ReturnType<typeof registerSchema>>

/** The POST /api/aps body. The secret is sent as lower-case hex. */
export function toAPInput(values: RegisterValues): APInput {
  const secret = decodeSecret(values.secret)
  const input: APInput = {
    id: normaliseMac(values.id) ?? values.id.trim(),
    name: values.name.trim(),
    site_id: values.site_id,
    secret: secret ? toHex(secret) : values.secret.trim(),
  }
  if (values.pinned_ip.trim()) input.management_ip = values.pinned_ip.trim()
  return input
}

/** Server field names to form field names (POST /api/aps answers on management_ip). */
export function formField(field: string): keyof RegisterValues | null {
  switch (field) {
    case 'id':
    case 'name':
    case 'site_id':
    case 'secret':
      return field
    case 'management_ip':
    case 'pinned_ip':
      return 'pinned_ip'
    default:
      return null
  }
}
