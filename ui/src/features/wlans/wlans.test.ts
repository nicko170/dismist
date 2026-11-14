import { describe, expect, it } from 'vitest'
import type { WLANView } from '@/api/queries/networks'
import { networks } from '@/features/settings/mocks/config-data'
import { mergePatch } from '@/features/settings/kit/patch'
import { newWlanForm, presetForm, toForm, toInput } from './form'
import { caseOnlyDuplicate, wlanSchema } from './schema'
import { effectivePMF, passphraseError, slotsPerBand } from './security'
import { wifiPayload } from './WifiQR'
// GET /api/sites/$id/networks/$id from dismistd on the v0.3.0 production
// fixture after the schema-2 migration (cmd/dismistd/testdata/v030-production).
import production from './testdata/production-wlan.json'

const example = networks()[0]

describe('passphrase rules (§3.9)', () => {
  it('accepts 8 to 63 printable ASCII for every personal mode', () => {
    for (const mode of ['wpa2_personal', 'wpa2_wpa3_personal', 'wpa3_personal'] as const) {
      expect(passphraseError(mode, 'eight888')).toBeNull()
      expect(passphraseError(mode, 'x'.repeat(63))).toBeNull()
      expect(passphraseError(mode, 'short')).toBe('At least 8 characters.')
      expect(passphraseError(mode, 'x'.repeat(64 + 1))).toMatch(/At most 63/)
    }
  })
  it('allows 64 hex only for WPA2-Personal', () => {
    const hex = 'ab'.repeat(32)
    expect(passphraseError('wpa2_personal', hex)).toBeNull()
    expect(passphraseError('wpa2_wpa3_personal', hex)).toMatch(/64-digit hex key only works with WPA2-Personal/)
    expect(passphraseError('wpa3_personal', hex)).toMatch(/64-digit hex/)
  })
  it('rejects non-ASCII', () => {
    expect(passphraseError('wpa2_personal', 'pässwörter')).toMatch(/printable ASCII/)
  })
  it('has no passphrase for open, OWE and enterprise', () => {
    expect(passphraseError('open', '')).toBeNull()
    expect(passphraseError('wpa2_enterprise', '')).toBeNull()
  })
})

describe('effective PMF', () => {
  it('is the option for WPA2 modes and fixed otherwise', () => {
    expect(effectivePMF('wpa2_personal', 'required')).toMatchObject({ value: 'required', editable: true })
    expect(effectivePMF('wpa2_personal', null)).toMatchObject({ value: 'optional', editable: true })
    expect(effectivePMF('wpa2_wpa3_personal', 'disabled')).toMatchObject({ value: 'optional', editable: false })
    expect(effectivePMF('wpa3_personal', null)).toMatchObject({ value: 'required', editable: false })
    expect(effectivePMF('open', null)).toMatchObject({ value: 'disabled', editable: false })
    expect(effectivePMF('owe', null)).toMatchObject({ value: 'required', editable: false })
  })
})

describe('capacity (§3.8)', () => {
  it('counts OWE transition as two WLANs per band', () => {
    expect(
      slotsPerBand([
        { bands: ['2.4', '5'], security: 'wpa2_wpa3_personal' },
        { bands: ['5'], security: 'owe_transition' },
      ]),
    ).toEqual({ '2.4': 1, '5': 3 })
  })
})

describe('WLAN form mapping', () => {
  it('round-trips a stored WLAN with no changes', () => {
    const form = toForm(example)
    expect(mergePatch(form, toForm(example))).toBeUndefined()
    expect(form.vlan).toEqual({ tagged: false, id: null })
    expect(form.min_rssi).toEqual({ b24: null, b5: null })
  })

  it('sends only what changed, and the passphrase only when typed', () => {
    const base = toForm(example)
    const next = { ...base, hidden: true }
    const body = toInput(mergePatch(base, next) as Record<string, unknown>, next, false)
    expect(body).toEqual({ hidden: true })
    const withPsk = { ...base, psk: 'a new passphrase' }
    expect(toInput(mergePatch(base, withPsk) as Record<string, unknown>, withPsk, false)).toEqual({ psk: 'a new passphrase' })
  })

  it('maps VLAN, per-band RSSI and rate limits to the API shapes', () => {
    const base = toForm(example)
    const next = { ...base, vlan: { tagged: true, id: 30 }, min_rssi: { b24: -75, b5: null }, rate_limit: { ...base.rate_limit, client_down: 20000 } }
    const body = toInput(mergePatch(base, next) as Record<string, unknown>, next, false)
    expect(body).toMatchObject({ vlan_id: 30, min_rssi: { '2.4': -75 }, rate_limit: { client_down: 20000, wlan_up: null } })
  })

  it('creates with every field and the new-object defaults (§4.6)', () => {
    const form = { ...newWlanForm(), ssid: 'Example', psk: 'a passphrase' }
    const body = toInput({}, form, true)
    expect(body).toMatchObject({ ssid: 'Example', security: 'wpa2_wpa3_personal', band_steering: false, idle_timeout_sec: 1800, vlan_id: 1, psk: 'a passphrase' })
    expect(body).not.toHaveProperty('pmf')
  })

  it('applies presets', () => {
    expect(presetForm('iot', false)).toMatchObject({ security: 'wpa2_personal', bands: ['2.4'], band_steering: false })
    expect(presetForm('guest', false).isolation).toBe('clients_and_lan')
    expect(presetForm('guest', false).rate_limit.client_down).toBeNull()
    expect(presetForm('guest', true).rate_limit.client_down).toBe(20000)
  })
})

describe('WLAN schema', () => {
  const others = networks().filter((n) => n.site_id === 'home')

  it('requires a passphrase on create and validates it by mode', () => {
    const schema = wlanSchema({ create: true, others })
    const result = schema.safeParse({ ...newWlanForm(), ssid: 'New' })
    expect(result.success).toBe(false)
    expect(result.error?.issues.find((i) => i.path.join('.') === 'psk')?.message).toBe('Enter a passphrase.')
    expect(schema.safeParse({ ...newWlanForm(), ssid: 'New', psk: 'long enough' }).success).toBe(true)
  })

  it('edits a WLAN whose stored QoS class is "" (migrated or created through the API)', () => {
    const migrated: WLANView = { ...example, qos_class: '' }
    const form = toForm(migrated)
    expect(form.qos_class).toBe('none')
    const schema = wlanSchema({ create: false, current: migrated, others })
    expect(schema.safeParse({ ...form, ssid: 'Example2' }).success).toBe(true)
    // Sending the form back unchanged changes nothing.
    expect(mergePatch(form, toForm(migrated))).toBeUndefined()
  })

  it('edits the migrated production WLAN, whether the view says "none" or the stored ""', () => {
    for (const qos of ['none', ''] as const) {
      const view = { ...(production as unknown as WLANView), qos_class: qos }
      const form = toForm(view)
      const schema = wlanSchema({ create: false, current: view, others: [] })
      const result = schema.safeParse({ ...form, ssid: 'Example2' })
      expect(result.error?.issues ?? []).toEqual([])
    }
  })

  it('keeps a stored passphrase when empty, unless the old mode had none', () => {
    expect(wlanSchema({ create: false, current: example, others }).safeParse(toForm(example)).success).toBe(true)
    const open: WLANView = { ...example, security: 'open', has_psk: false }
    const result = wlanSchema({ create: false, current: open, others }).safeParse({ ...toForm(open), security: 'wpa2_personal' })
    expect(result.error?.issues.some((i) => i.path.join('.') === 'psk')).toBe(true)
  })

  it('refuses a duplicate SSID on an overlapping band and warns on case-only duplicates', () => {
    const schema = wlanSchema({ create: true, others })
    const dup = schema.safeParse({ ...newWlanForm(), ssid: 'Example', psk: 'long enough' })
    expect(dup.error?.issues.find((i) => i.path[0] === 'ssid')?.message).toMatch(/already uses this SSID/)
    expect(caseOnlyDuplicate('example', { others })).toMatch(/differs only in case/)
    expect(caseOnlyDuplicate('Example', { others })).toBeNull()
  })

  it('checks SSID bytes, VLAN range, band steering and RADIUS servers', () => {
    const schema = wlanSchema({ create: true, others })
    const issues = (v: object) => schema.safeParse({ ...newWlanForm(), psk: 'long enough', ...v }).error?.issues.map((i) => `${i.path.join('.')}: ${i.message}`) ?? []
    expect(issues({ ssid: 'é'.repeat(17) })).toContain('ssid: At most 32 bytes.')
    expect(issues({ ssid: 'A', vlan: { tagged: true, id: 1 } })).toContain('vlan.id: Enter a VLAN from 2 to 4094.')
    expect(issues({ ssid: 'A', bands: ['2.4'], band_steering: true })).toContain('band_steering: Band steering needs both bands.')
    expect(issues({ ssid: 'A', security: 'wpa2_enterprise' })).toContain('radius.auth: Add at least one RADIUS server.')
    expect(issues({ ssid: 'A', dtim: 'x' })).toContain('dtim: Enter a whole number from 1 to 255.')
  })

  it('limits per-device passphrases to WPA2-Personal with unique keys', () => {
    const schema = wlanSchema({ create: true, others })
    const key = { id: '', name: 'tv', passphrase: 'tv passphrase', has_passphrase: false, vlan_id: null, mac: '', expires: '' }
    const result = schema.safeParse({ ...newWlanForm(), ssid: 'A', psk: 'long enough', mpsk: { enabled: true, private: false, keys: [key, { ...key }] } })
    const messages = result.error?.issues.map((i) => i.message) ?? []
    expect(messages).toContain('Per-device passphrases need WPA2-Personal.')
    expect(messages).toContain('Each key needs its own name.')
  })
})

describe('Wi-Fi QR payload', () => {
  it('escapes special characters', () => {
    expect(wifiPayload('a;b', 'WPA', 'p:w"d')).toBe('WIFI:T:WPA;S:a\\;b;P:p\\:w\\"d;;')
    expect(wifiPayload('Open', 'nopass', '', true)).toBe('WIFI:T:nopass;S:Open;H:true;;')
  })
})
