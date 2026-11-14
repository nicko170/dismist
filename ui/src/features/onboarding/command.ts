// The "Prepare an AP with the CLI" command builder (design §3.15 card 2).

/** Quotes value for a POSIX shell, leaving plain words alone. */
export function shellQuote(value: string): string {
  if (/^[A-Za-z0-9_@%+=:,./-]+$/.test(value)) return value
  return `'${value.replace(/'/g, `'\\''`)}'`
}

const hostname = /^(?=.{1,253}$)([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*\.?$/

/** An AP address for SSH: [user@]host where host is a name, IPv4 or IPv6 literal. */
export function parseAPAddress(value: string): { user: string; host: string } | null {
  const trimmed = value.trim()
  if (!trimmed) return null
  const at = trimmed.lastIndexOf('@')
  const user = at >= 0 ? trimmed.slice(0, at) : 'root'
  let host = at >= 0 ? trimmed.slice(at + 1) : trimmed
  if (!/^[a-z_][a-z0-9_-]{0,31}$/i.test(user)) return null
  if (host.startsWith('[') && host.endsWith(']')) host = host.slice(1, -1)
  if (host.includes(':')) {
    if (!/^[0-9a-fA-F:.]+$/.test(host)) return null
    return { user, host }
  }
  if (/^[\d.]+$/.test(host)) {
    const octets = host.split('.')
    if (octets.length !== 4 || octets.some((o) => o === '' || Number(o) > 255)) return null
    return { user, host }
  }
  return hostname.test(host) ? { user, host } : null
}

export type CommandInput = {
  address: string
  controller: string
  site: string
  name: string
  tokenFile?: string
}

export const DEFAULT_TOKEN_FILE = '~/.config/dismist/token'

/** `dismist ap33 onboard root@<ap> --controller … --token-file … --site … --name …`, or null until the address is valid. */
export function buildOnboardCommand({ address, controller, site, name, tokenFile = DEFAULT_TOKEN_FILE }: CommandInput): string | null {
  const parsed = parseAPAddress(address)
  if (!parsed) return null
  const target = `${parsed.user}@${parsed.host}`
  // ~ must stay unquoted for the shell to expand it.
  const tokenArg = tokenFile.startsWith('~/') ? `~/${shellQuote(tokenFile.slice(2))}` : shellQuote(tokenFile)
  const parts = [
    'dismist ap33 onboard',
    shellQuote(target),
    `--controller ${shellQuote(controller.replace(/\/+$/, ''))}`,
    `--token-file ${tokenArg}`,
    `--site ${shellQuote(site || 'Home')}`,
    `--name ${shellQuote(name.trim() || 'AP')}`,
  ]
  return parts.join(' ')
}
