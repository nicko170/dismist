import { z } from 'zod'
import { utf8Length } from '@/lib/format'
import { normaliseMac } from '@/lib/mac'
import { passphraseProblem, secretByteLength } from './secret'

// zod building blocks shared by every form. Generated option schemas
// (gen/options.gen.ts) cover per-field bounds; these cover formats and the
// cross-field helpers feature schemas refine with (design §7.4).

const IPV4 = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/

export function isIPv4(value: string): boolean {
  return IPV4.test(value)
}

export function isIPv6(value: string): boolean {
  if (!value.includes(':') || !/^[0-9a-fA-F:.]+$/.test(value)) return false
  try {
    return new URL(`http://[${value}]/`).hostname.length > 0
  } catch {
    return false
  }
}

export function isHostname(value: string): boolean {
  if (value.length > 253) return false
  return value.split('.').every((label) => /^(?!-)[A-Za-z0-9-]{1,63}(?<!-)$/.test(label))
}

export const ipv4 = z.string().trim().refine(isIPv4, 'Enter an IPv4 address, like 192.0.2.10.')
export const ip = z.string().trim().refine((v) => isIPv4(v) || isIPv6(v), 'Enter an IPv4 or IPv6 address.')
export const host = z.string().trim().refine((v) => isIPv4(v) || isIPv6(v) || isHostname(v), 'Enter a hostname or an IP address.')
export const optionalIP = z.string().trim().refine((v) => v === '' || isIPv4(v) || isIPv6(v), 'Enter an IPv4 or IPv6 address, or leave it empty.')

/** A MAC in any common form; the output is the canonical aa-bb-cc-dd-ee-ff. */
export const mac = z
  .string()
  .trim()
  .transform((value, ctx) => {
    const canonical = normaliseMac(value)
    if (!canonical) {
      ctx.addIssue({ code: 'custom', message: 'Enter a MAC address, like 02-aa-00-00-00-01.' })
      return z.NEVER
    }
    return canonical
  })

export const vlanId = z.number({ error: 'Enter a VLAN ID.' }).int('Use a whole number.').min(1, 'VLAN IDs run from 1 to 4094.').max(4094, 'VLAN IDs run from 1 to 4094.')

/** SSIDs are 1 to 32 bytes of UTF-8, not 32 characters. */
export const ssid = z
  .string()
  .min(1, 'Enter a network name.')
  .refine((v) => utf8Length(v) <= 32, { message: 'Network names are at most 32 bytes (emoji and accented letters use more than one).' })

export const passphrase = z.string().superRefine((value, ctx) => {
  const problem = passphraseProblem(value)
  if (problem) ctx.addIssue({ code: 'custom', message: problem })
})

/** Write-only passphrase on an edit form: empty keeps the stored one. */
export const passphraseOrKeep = z.string().superRefine((value, ctx) => {
  if (value === '') return
  const problem = passphraseProblem(value)
  if (problem) ctx.addIssue({ code: 'custom', message: problem })
})

/** The AP's 32-byte EEPROM secret, pasted as hex or base64. */
export const apSecret = z
  .string()
  .trim()
  .refine((v) => secretByteLength(v) === 32, 'The secret must be 32 bytes, as hex (64 characters) or base64.')

const SSH_KEY = /^(ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp(256|384|521)|sk-ssh-ed25519@openssh\.com|sk-ecdsa-sha2-nistp256@openssh\.com) [A-Za-z0-9+/]+={0,3}( .*)?$/

export function isSSHPublicKey(value: string): boolean {
  return SSH_KEY.test(value.trim())
}

export const sshPublicKey = z.string().trim().refine(isSSHPublicKey, 'Paste an OpenSSH public key, like "ssh-ed25519 AAAA… you@laptop".')

/** A number from a NumberField: null when empty, NaN when not a number. */
export function numberInRange(min: number, max: number, unit = '') {
  const range = `${min} to ${max}${unit ? ` ${unit}` : ''}`.replace(/-/g, '\u2212')
  return z
    .number({ error: 'Enter a number.' })
    .refine((v) => !Number.isNaN(v), 'Enter a number.')
    .refine((v) => Number.isInteger(v), 'Use a whole number.')
    .refine((v) => v >= min && v <= max, `Use ${range}.`)
}
