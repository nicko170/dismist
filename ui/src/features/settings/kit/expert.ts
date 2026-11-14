import { optionMeta } from '@/gen/options.gen'
import type { ExpertScope } from '@/api/queries/sites'
import { provisionalOptions } from './provisional-options'

// Client-side mirror of the expert-patch rules (design §4.4 "Forbidden in
// expert patches", "Key folding"). The server is the authority; this only
// shows problems while typing. Keys are compared case-insensitively, as the
// AP decodes them.

const ENVELOPE = ['ID', 'UUID', 'SiteID', 'OrgID', 'Timestamp', 'NotModified', 'FailedRetryAfter', 'Stats.Interval', 'Stats.FullInterval', 'Stats.MemInterval']
const NEVER = [
  'Ipsec', 'Calea', 'SiteString', 'PAPIOpaque', 'DeIdent', 'AVS', 'BootConfig', 'Firmware', 'MapID', 'XYZ', 'Default',
  'LicenseExpired', 'RegulatoryOrg', 'RadioSwap', 'IndoorUse', 'DirAntenna', 'ManagementTunnels', 'Monitor.PcapWLAN',
]

export const FORBIDDEN: Record<ExpertScope, string[]> = {
  site: ['WLANs', ...ENVELOPE, ...NEVER],
  ap: ['WLANs', ...ENVELOPE, ...NEVER],
  wlan: [
    'ID', 'SSID', 'Enabled', 'Auth.PSK', 'Auth.MultiPSK.PSKs', 'KeyCache.KeyEncryptKey', 'BandConfigs[].BSSIDIndex', 'WLCmds',
    'Wxlan.Portal*', 'ISE', 'AirWatch', 'DPI', 'COA.CloudEnabled', 'BlackList.Cloud', 'Auth.WEP', 'Auth.SecondaryType',
    'Auth.AKM.WPA_PSK', 'Auth.AKM.WPA_EAP', 'Auth.Pairwise.TKIP', 'Auth.Pairwise.WPA1', 'Mesh', 'ForMesh',
  ],
}

const WARN: Record<string, string> = {
  'cloud.proxy.url': 'Cloud.Proxy.URL can redirect the AP’s controller connection.',
}

export type PatchProblem = { pointer: string; path: string; key: string; message: string; kind: 'forbidden' | 'duplicate' | 'warning' }

/** A key path is forbidden when it equals a pattern (case-insensitive) or extends a "Prefix*" one. */
function matches(pattern: string, path: string) {
  const p = pattern.toLowerCase()
  const q = path.toLowerCase()
  return p.endsWith('*') ? q.startsWith(p.slice(0, -1)) : q === p
}

/**
 * Every forbidden path, duplicate folded key and warning in a parsed patch.
 * serverForbidden are the server's patterns (GET /api/options/expert), on
 * top of the built-in list, so the editor matches what the save enforces.
 */
export function checkPatch(scope: ExpertScope, patch: unknown, serverForbidden: string[] = []): PatchProblem[] {
  const patterns = [...FORBIDDEN[scope], ...serverForbidden.filter((p) => !FORBIDDEN[scope].includes(p))]
  const problems: PatchProblem[] = []
  const walk = (node: unknown, path: string, pointer: string) => {
    if (Array.isArray(node)) {
      node.forEach((child, i) => walk(child, `${path}[]`, `${pointer}/${i}`))
      return
    }
    if (!node || typeof node !== 'object') return
    const seen = new Map<string, string>()
    for (const [key, child] of Object.entries(node as Record<string, unknown>)) {
      const folded = key.toLowerCase()
      const childPath = path ? `${path}.${key}` : key
      const childPointer = `${pointer}/${key}`
      if (seen.has(folded)) {
        problems.push({ pointer: childPointer, path: childPath, key, kind: 'duplicate', message: `“${key}” and “${seen.get(folded)}” are the same key to the AP. Keep one.` })
      }
      seen.set(folded, key)
      const forbidden = patterns.find((pattern) => matches(pattern, childPath))
      if (forbidden) {
        problems.push({ pointer: childPointer, path: childPath, key, kind: 'forbidden', message: forbiddenMessage(scope, forbidden) })
        continue
      }
      const warning = WARN[childPath.toLowerCase()]
      if (warning) problems.push({ pointer: childPointer, path: childPath, key, kind: 'warning', message: warning })
      walk(child, childPath, childPointer)
    }
  }
  walk(patch, '', '')
  return problems
}

function forbiddenMessage(scope: ExpertScope, pattern: string) {
  if (pattern === 'WLANs') return 'WLANs cannot be patched here; use the WLAN’s own expert patch.'
  if (scope === 'wlan' && ['ID', 'SSID', 'Enabled', 'Auth.PSK'].includes(pattern)) return `${pattern} is set by the WLAN form, not the expert patch.`
  return `${pattern.replace('*', '…')} cannot be set in an expert patch.`
}

/** Text ranges of each problem's key, for editor diagnostics. */
export function problemRanges(text: string, problems: PatchProblem[]) {
  return problems
    .filter((p) => p.kind !== 'warning')
    .map((p) => {
      const needle = `"${p.key}"`
      const from = Math.max(0, text.indexOf(needle))
      return { from, to: from + needle.length, message: p.message }
    })
}

/**
 * Firmware key names for autocomplete: the scope's 0.15 config paths from
 * the server (GET /api/options/expert) when it has sent them, plus the
 * option registry's firmware paths.
 */
export function keyCompletions(scope: ExpertScope, serverPaths: string[] = []): string[] {
  const words = new Set<string>()
  for (const path of serverPaths) {
    for (const segment of path.split('.')) {
      const name = segment.replace(/\[[^\]]*\]$/, '').replace(/\{\}$/, '')
      if (name && /^[A-Za-z_$][\w$]*$/.test(name)) words.add(name)
    }
  }
  for (const meta of [...Object.values(provisionalOptions), ...Object.values(optionMeta)]) {
    for (const path of meta.firmwarePaths) {
      const isWlan = path.startsWith('WLANs[]')
      if ((scope === 'wlan') !== isWlan) continue
      const rest = isWlan ? path.slice('WLANs[].'.length) : path
      for (const segment of rest.split('.')) {
        const name = segment.replace(/\[[^\]]*\]$/, '')
        if (name) words.add(name)
      }
    }
  }
  for (const extra of scope === 'wlan' ? ['Band', 'RateSets', 'BandConfigs'] : ['Band', 'Radios', 'SwitchPorts', 'SVIs', 'Name', 'Vlan_ID', '$remove']) words.add(extra)
  return [...words].toSorted()
}

export type ParseResult = { ok: true; value: Record<string, unknown> } | { ok: false; message: string }

export function parsePatch(text: string): ParseResult {
  if (!text.trim()) return { ok: true, value: {} }
  try {
    const value = JSON.parse(text) as unknown
    if (!value || typeof value !== 'object' || Array.isArray(value)) return { ok: false, message: 'An expert patch is a JSON object: { … }.' }
    return { ok: true, value: value as Record<string, unknown> }
  } catch (error) {
    return { ok: false, message: error instanceof Error ? error.message : 'Not valid JSON.' }
  }
}
