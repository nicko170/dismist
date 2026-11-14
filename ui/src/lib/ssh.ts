// OpenSSH public keys (site SSH keys, design §3.11 Access): parse the
// "type base64 comment" line and compute the SHA256 fingerprint the way
// ssh-keygen -l prints it, so operators can match keys by eye.

const TYPES = ['ssh-ed25519', 'ssh-rsa', 'ecdsa-sha2-nistp256', 'ecdsa-sha2-nistp384', 'ecdsa-sha2-nistp521', 'sk-ssh-ed25519@openssh.com', 'sk-ecdsa-sha2-nistp256@openssh.com']

export type SSHKey = { type: string; blob: string; comment: string; line: string }

export function parseSSHKey(line: string): SSHKey | null {
  const parts = line.trim().split(/\s+/)
  if (parts.length < 2) return null
  const [type, blob, ...rest] = parts
  if (!TYPES.includes(type) || !/^[A-Za-z0-9+/]+={0,3}$/.test(blob)) return null
  try {
    // The blob starts with the key type as a length-prefixed string.
    const bytes = Uint8Array.from(atob(blob), (c) => c.charCodeAt(0))
    const length = (bytes[0] << 24) | (bytes[1] << 16) | (bytes[2] << 8) | bytes[3]
    const embedded = new TextDecoder().decode(bytes.slice(4, 4 + length))
    if (embedded !== type) return null
  } catch {
    return null
  }
  return { type, blob, comment: rest.join(' '), line: `${type} ${blob}${rest.length ? ` ${rest.join(' ')}` : ''}` }
}

/** "SHA256:…" (base64 without padding), as ssh-keygen -l shows it. */
export async function sshFingerprint(blob: string): Promise<string> {
  const bytes = Uint8Array.from(atob(blob), (c) => c.charCodeAt(0))
  const digest = new Uint8Array(await crypto.subtle.digest('SHA-256', bytes))
  let binary = ''
  for (const byte of digest) binary += String.fromCharCode(byte)
  return `SHA256:${btoa(binary).replace(/=+$/, '')}`
}

/** Short type names for display ("ed25519", "rsa", "ecdsa-256"). */
export function sshTypeLabel(type: string): string {
  if (type.includes('ed25519')) return type.startsWith('sk-') ? 'ed25519-sk' : 'ed25519'
  if (type === 'ssh-rsa') return 'rsa'
  const ecdsa = type.match(/nistp(\d+)/)
  return ecdsa ? `ecdsa-${ecdsa[1]}${type.startsWith('sk-') ? '-sk' : ''}` : type
}
