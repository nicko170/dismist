import type { APDetail, WLANLive } from '@/gen/api.gen'

// The live Verify panel (design §3.15): "Waiting for hello" → "Authenticated
// (model, firmware)" → "Config delivered (rev N)" → "Config applied" →
// "Broadcasting <SSID> on 2.4 and 5 GHz", or the error that stops it.

export type StepState = 'done' | 'active' | 'waiting' | 'error'

export type VerifyStep = {
  id: 'registered' | 'hello' | 'delivered' | 'applied' | 'broadcasting'
  label: string
  state: StepState
  detail?: string
}

/** After this long with no hello, the panel explains what the controller cannot see (TLS happens before any hello). */
export const NO_HELLO_HINT_MS = 3 * 60_000

export type VerifyInput = Pick<
  APDetail,
  | 'id'
  | 'name'
  | 'status'
  | 'connected'
  | 'connected_at'
  | 'last_seen'
  | 'model'
  | 'firmware'
  | 'config_revision'
  | 'applied_revision'
  | 'last_error'
  | 'radios'
> &
  Partial<Pick<APDetail, 'wlans_live'>>

function bandList(bands: string[]): string {
  const unique = [...new Set(bands.filter(Boolean))].toSorted((a, b) => Number(a) - Number(b))
  if (!unique.length) return ''
  if (unique.length === 1) return `${unique[0]} GHz`
  return `${unique.slice(0, -1).join(', ')} and ${unique.at(-1)} GHz`
}

/** "Example on 2.4 and 5 GHz; Guest on 5 GHz". */
export function describeBroadcast(wlans: WLANLive[]): string {
  const bySSID = new Map<string, string[]>()
  for (const wlan of wlans) {
    const ssid = wlan.ssid || wlan.wlan_id || 'a WLAN'
    bySSID.set(ssid, [...(bySSID.get(ssid) ?? []), wlan.band])
  }
  return [...bySSID].map(([ssid, bands]) => (bandList(bands) ? `${ssid} on ${bandList(bands)}` : ssid)).join('; ')
}

export function verifySteps(ap: VerifyInput, waitingSinceMs = 0): VerifyStep[] {
  const steps: VerifyStep[] = [{ id: 'registered', label: `Registered as ${ap.name || ap.id}`, state: 'done' }]
  const seen = ap.connected || Boolean(ap.connected_at) || Boolean(ap.firmware)

  // 1. Hello and authentication.
  if (ap.status === 'auth_failing') {
    steps.push({
      id: 'hello',
      label: 'Authentication failed',
      state: 'error',
      detail: 'The AP said hello but its proof did not match: the stored secret is not this AP’s. Read it again from the AP’s EEPROM (offset 0x400) and update the AP.',
    })
  } else if (ap.status === 'source_mismatch') {
    steps.push({
      id: 'hello',
      label: 'Connected from an unexpected address',
      state: 'error',
      detail: 'The AP authenticated from an address other than its pinned source IP. Clear the pin, or correct it.',
    })
  } else if (!seen) {
    steps.push({
      id: 'hello',
      label: 'Waiting for hello',
      state: 'active',
      detail:
        waitingSinceMs >= NO_HELLO_HINT_MS
          ? 'Nothing yet. TLS failures never reach the controller: check the AP’s cloud.ini URL and that its CA bundle holds the roots that issue this controller’s certificate (dismist ap33 inspect shows both).'
          : 'The AP connects within about two minutes of booting with the new cloud.ini.',
    })
  } else {
    const identity = [ap.model, ap.firmware].filter(Boolean).join(', ')
    steps.push({ id: 'hello', label: identity ? `Authenticated (${identity})` : 'Authenticated', state: 'done' })
  }
  const helloDone = steps[1].state === 'done'

  // 2. Delivery and apply.
  const inSync = ap.config_revision > 0 && ap.applied_revision === ap.config_revision
  if (!helloDone) {
    steps.push({ id: 'delivered', label: 'Config delivered', state: 'waiting' })
    steps.push({ id: 'applied', label: 'Config applied', state: 'waiting' })
  } else if (ap.status === 'config_error') {
    steps.push({ id: 'delivered', label: 'Config could not be compiled', state: 'error', detail: ap.last_error || undefined })
    steps.push({ id: 'applied', label: 'Config applied', state: 'waiting' })
  } else if (ap.status === 'rejected') {
    steps.push({ id: 'delivered', label: `Config delivered (rev ${ap.config_revision})`, state: 'done' })
    steps.push({ id: 'applied', label: 'Config rejected by the AP', state: 'error', detail: ap.last_error || undefined })
  } else if (!ap.connected && !inSync) {
    steps.push({ id: 'delivered', label: 'Config delivered', state: 'active', detail: 'The AP disconnected; it fetches its config when it reconnects.' })
    steps.push({ id: 'applied', label: 'Config applied', state: 'waiting' })
  } else {
    steps.push({ id: 'delivered', label: `Config delivered (rev ${ap.config_revision})`, state: 'done' })
    steps.push(
      inSync
        ? { id: 'applied', label: `Config applied (rev ${ap.applied_revision})`, state: 'done' }
        : { id: 'applied', label: 'Applying config', state: 'active', detail: `Applied rev ${ap.applied_revision || 'none'} of ${ap.config_revision}.` },
    )
  }

  // 3. Broadcasting.
  const wlans = ap.wlans_live ?? []
  const applied = steps.at(-1)?.state === 'done'
  if (!applied) {
    steps.push({ id: 'broadcasting', label: 'Broadcasting', state: 'waiting' })
  } else if (wlans.length) {
    steps.push({ id: 'broadcasting', label: `Broadcasting ${describeBroadcast(wlans)}`, state: 'done' })
  } else {
    const radios = bandList((ap.radios ?? []).filter((radio) => radio.channel > 0).map((radio) => radio.band))
    steps.push({
      id: 'broadcasting',
      label: 'Waiting for the AP to report its WLANs',
      state: 'active',
      detail: radios
        ? `Radios are up on ${radios}. If the site has no enabled WLAN, there is nothing to broadcast yet.`
        : 'If the site has no enabled WLAN, there is nothing to broadcast yet.',
    })
  }
  return steps
}

/** The panel is finished (success or a stopping error). */
export function verifyFinished(steps: VerifyStep[]): 'ok' | 'error' | null {
  if (steps.some((step) => step.state === 'error')) return 'error'
  return steps.every((step) => step.state === 'done') ? 'ok' : null
}
