import type { Band } from '@/gen/api.gen'
import type { PMF, Security, WLANView } from '@/api/queries/networks'
import { isPrintableASCII } from '@/features/settings/kit/validators'

// The security composite (design §3.9 "Security composite: exact device
// mapping"). The server writes the firmware fields; the UI needs the labels,
// which fields each mode shows, the passphrase rules and the effective PMF.

type SecurityInfo = {
  label: string
  kind: 'personal' | 'enterprise' | 'open'
  /** Any SAE (WPA3) mode: passphrase 8 to 63 printable ASCII, no 64-hex. */
  sae: boolean
  /** PMF when the mode fixes it; null when the PMF option applies (WPA2 modes). */
  pmf: { value: PMF; label: string } | null
  /** WLAN slots per band (OWE transition adds an open companion). */
  slots: number
}

export const SECURITY: Record<Security, SecurityInfo> = {
  wpa2_personal: { label: 'WPA2-Personal', kind: 'personal', sae: false, pmf: null, slots: 1 },
  wpa2_wpa3_personal: { label: 'WPA2/WPA3-Personal', kind: 'personal', sae: true, pmf: { value: 'optional', label: 'Optional (set by WPA3)' }, slots: 1 },
  wpa3_personal: { label: 'WPA3-Personal', kind: 'personal', sae: true, pmf: { value: 'required', label: 'Required' }, slots: 1 },
  open: { label: 'Open', kind: 'open', sae: false, pmf: { value: 'disabled', label: 'Disabled' }, slots: 1 },
  owe: { label: 'Enhanced Open (OWE)', kind: 'open', sae: false, pmf: { value: 'required', label: 'Required' }, slots: 1 },
  owe_transition: { label: 'Enhanced Open + Open', kind: 'open', sae: false, pmf: { value: 'disabled', label: 'Disabled (open companion)' }, slots: 2 },
  wpa2_enterprise: { label: 'WPA2-Enterprise', kind: 'enterprise', sae: false, pmf: null, slots: 1 },
  wpa2_wpa3_enterprise: { label: 'WPA2/WPA3-Enterprise', kind: 'enterprise', sae: false, pmf: { value: 'optional', label: 'Optional (set by WPA3)' }, slots: 1 },
  wpa3_enterprise: { label: 'WPA3-Enterprise', kind: 'enterprise', sae: false, pmf: { value: 'required', label: 'Required' }, slots: 1 },
}

export function securityLabel(security: Security | string): string {
  return SECURITY[security as Security]?.label ?? security
}

export function isPersonal(security: Security | string) {
  return SECURITY[security as Security]?.kind === 'personal'
}

export function isEnterprise(security: Security | string) {
  return SECURITY[security as Security]?.kind === 'enterprise'
}

/** The PMF the AP runs: the option for WPA2 modes, fixed otherwise. */
export function effectivePMF(security: Security, pmf: PMF | null | undefined): { value: PMF; editable: boolean; label: string } {
  const fixed = SECURITY[security]?.pmf
  if (fixed) return { value: fixed.value, editable: false, label: fixed.label }
  const value = pmf ?? 'optional'
  return { value, editable: true, label: value === 'disabled' ? 'Disabled' : value === 'required' ? 'Required' : 'Optional' }
}

/**
 * Passphrase rules [WLAN §7.3]: WPA2-Personal takes 8 to 63 printable ASCII
 * or exactly 64 hex digits; any SAE mode takes 8 to 63 printable ASCII and
 * rejects 64-hex. Returns an error message, or null when valid.
 */
export function passphraseError(security: Security, psk: string): string | null {
  if (!isPersonal(security)) return null
  const sae = SECURITY[security].sae
  if (/^[0-9a-fA-F]{64}$/.test(psk)) {
    return sae ? 'WPA3 needs a passphrase of 8 to 63 characters; a 64-digit hex key only works with WPA2-Personal.' : null
  }
  if (!isPrintableASCII(psk)) return 'Use printable ASCII characters only (letters, digits, spaces and symbols; no emoji or accents).'
  if (psk.length < 8) return 'At least 8 characters.'
  if (psk.length > 63) return sae ? 'At most 63 characters.' : 'At most 63 characters, or exactly 64 hex digits.'
  return null
}

export function passphraseHelp(security: Security): string {
  return SECURITY[security]?.sae
    ? '8 to 63 printable characters.'
    : '8 to 63 printable characters, or exactly 64 hex digits.'
}

/** WLANs per band in use; OWE transition counts twice (§3.8 capacity line). */
export function slotsPerBand(networks: Pick<WLANView, 'bands' | 'security'>[]): Record<Band, number> {
  const out: Record<Band, number> = { '2.4': 0, '5': 0 }
  for (const network of networks) {
    for (const band of network.bands) out[band] += SECURITY[network.security]?.slots ?? 1
  }
  return out
}

/** The cap stays 8 per band until the lab confirms the firmware limit (§3.8). */
export const WLANS_PER_BAND = 8
