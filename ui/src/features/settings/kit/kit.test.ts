import { describe, expect, it } from 'vitest'
import { checkPatch, keyCompletions, parsePatch } from './expert'
import { insertablePaths, insertPath } from './ExpertPatchEditor'
import { conditionsHold, defaultsFor, fieldName, getOption, isExperimentalValue, leafSchema, visibleEnum } from './options'
import { changedPaths, compact, deepEqual, mergePatch } from './patch'
import { experimentalChanges, resolveFieldPath } from './useConfigForm'
import { isHostname, isIP, isIPv4, isIPv6, isNTPServer, isTimeRange, parseSSHKey, parseSSHKeys, secretByteLength, sshFingerprint } from './validators'
import { parseNumber } from './fields'

const KEY = 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEUEUFgIbAddz8QpHk7ULufpCRX9Obj5krYposbKm/YQ'

describe('merge patch', () => {
  it('sends changed leaves, arrays whole, removals as null', () => {
    expect(mergePatch({ a: 1, b: { c: 1, d: 2 }, e: [1, 2] }, { a: 1, b: { c: 1, d: 3 }, e: [1, 2, 3] })).toEqual({ b: { d: 3 }, e: [1, 2, 3] })
    expect(mergePatch({ a: 1, b: 2 }, { a: 1 })).toEqual({ b: null })
    expect(mergePatch({ a: 1 }, { a: 1 })).toBeUndefined()
  })
  it('lists changed paths and compares deeply', () => {
    expect(changedPaths({ a: { b: 1, c: [1] } }, { a: { b: 2, c: [1, 2] } })).toEqual(['a.b', 'a.c'])
    expect(deepEqual({ a: [1, { b: 2 }] }, { a: [1, { b: 2 }] })).toBe(true)
    expect(compact({ a: undefined, b: { c: undefined, d: 1 } })).toEqual({ b: { d: 1 } })
  })
})

describe('option registry access', () => {
  it('falls back to the provisional transcription with scope-relative field names', () => {
    const meta = getOption('site.rf.band_5.width')
    expect(fieldName(meta)).toBe('band_5.width')
    expect(meta.enum.map((e) => e.value)).toEqual(['20', '40', '80'])
    expect(() => getOption('wlan.nope')).toThrow('unknown option wlan.nope')
  })
  it('marks needs-live-test options and enum values experimental', () => {
    expect(getOption('wlan.rate_limit').experimental).toBe(true)
    expect(isExperimentalValue(getOption('wlan.security'), 'owe')).toBe(true)
    expect(isExperimentalValue(getOption('wlan.security'), 'wpa2_wpa3_personal')).toBe(false)
    const security = getOption('wlan.security')
    expect(visibleEnum(security, false, 'wpa2_personal').some((v) => v.value === 'owe')).toBe(false)
    expect(visibleEnum(security, false, 'owe').some((v) => v.value === 'owe')).toBe(true)
    expect(visibleEnum(security, true, 'open').some((v) => v.value === 'owe_transition')).toBe(true)
  })
  it('builds zod leaves from bounds and enums', () => {
    expect(leafSchema('wlan.dtim').safeParse(3).success).toBe(true)
    expect(leafSchema('wlan.dtim').safeParse(0).error?.issues[0].message).toBe('Enter a whole number from 1 to 255.')
    expect(leafSchema('wlan.ssid', { required: true }).safeParse('é'.repeat(17)).error?.issues[0].message).toBe('At most 32 bytes.')
    expect(leafSchema('site.rf.band_5.width').safeParse(160).success).toBe(false)
    expect(leafSchema('wlan.isolation').safeParse('clients').success).toBe(true)
  })
  it('evaluates visibility conditions and new-object defaults', () => {
    expect(conditionsHold(getOption('wlan.pmf'), { security: 'wpa2_personal' })).toBe(true)
    expect(conditionsHold(getOption('wlan.pmf'), { security: 'wpa3_personal' })).toBe(false)
    expect(defaultsFor(['wlan.security', 'wlan.band_steering', 'wlan.idle_timeout_sec', 'wlan.roaming.neighbor_report'])).toEqual({
      security: 'wpa2_wpa3_personal',
      band_steering: false,
      idle_timeout_sec: 1800,
      roaming: { neighbor_report: true },
    })
  })
})

describe('form helpers', () => {
  it('maps server field paths onto form fields', () => {
    const known = new Set(['ssid', 'radius', 'radius.auth', 'radius.auth.0', 'radius.auth.0.host', 'vlan.id'])
    expect(resolveFieldPath('wlan.ssid', 'wlan', known)).toBe('ssid')
    expect(resolveFieldPath('radius.auth[0].host', 'wlan', known)).toBe('radius.auth.0.host')
    expect(resolveFieldPath('radius.auth[1].secret', 'wlan', known)).toBe('radius.auth')
    expect(resolveFieldPath('vlan_id', 'wlan', known, { vlan_id: 'vlan.id' })).toBe('vlan.id')
    expect(resolveFieldPath('/WLANs', 'wlan', known)).toBeNull()
  })
  it('finds experimental values among the changes', () => {
    expect(experimentalChanges('wlan', { security: 'open', rates: 'compatible' }, { security: 'owe', rates: 'compatible' })).toEqual(['Security'])
    expect(experimentalChanges('wlan', { rate_limit: { wlan_up: null } }, { rate_limit: { wlan_up: 100 } })).toEqual(['Rate limits'])
    expect(experimentalChanges('wlan', { hidden: false }, { hidden: true })).toEqual([])
  })
  it('parses number text, negating negative-only ranges', () => {
    expect(parseNumber('', false)).toBeNull()
    expect(parseNumber('42', false)).toBe(42)
    expect(parseNumber('75', true)).toBe(-75)
    expect(parseNumber('4.5', false)).toBe('4.5')
  })
})

describe('expert patch checks (§4.4)', () => {
  it('refuses forbidden paths in any case', () => {
    expect(checkPatch('site', { wlans: [] }).map((p) => p.kind)).toEqual(['forbidden'])
    expect(checkPatch('site', { Timestamp: 1 })[0].pointer).toBe('/Timestamp')
    expect(checkPatch('ap', { Stats: { interval: 5 } })[0].path).toBe('Stats.interval')
    expect(checkPatch('wlan', { ssid: 'x' })[0].message).toMatch(/set by the WLAN form/)
    expect(checkPatch('wlan', { auth: { psk: 'x' } })[0].kind).toBe('forbidden')
    expect(checkPatch('wlan', { BandConfigs: [{ Band: '5', bssidindex: 2 }] })[0].path).toBe('BandConfigs[].bssidindex')
    expect(checkPatch('wlan', { Wxlan: { PortalAllowedHostnames: [] } })[0].kind).toBe('forbidden')
    expect(checkPatch('wlan', { DTIM: { Period: 3 } })).toEqual([])
    expect(checkPatch('site', { Radios: [{ Band: '5', Power: 10 }] })).toEqual([])
  })
  it('flags keys that fold together and warns on the proxy URL', () => {
    expect(checkPatch('site', { LED: 1, led: 2 }).map((p) => p.kind)).toEqual(['duplicate'])
    expect(checkPatch('site', { Cloud: { Proxy: { URL: 'x' } } })[0].kind).toBe('warning')
  })
  it('parses patches, completes keys and inserts paths', () => {
    expect(parsePatch('')).toEqual({ ok: true, value: {} })
    expect(parsePatch('[1]').ok).toBe(false)
    expect(parsePatch('{"a":').ok).toBe(false)
    expect(keyCompletions('wlan')).toContain('Hidden')
    expect(keyCompletions('site')).toContain('Brightness')
    expect(insertablePaths('site')).toContain('LED.Brightness')
    expect(insertablePaths('ap')).toContain('Radios[5].Channel')
    expect(insertPath({}, 'LED.Brightness')).toEqual({ LED: { Brightness: '<value>' } })
    expect(insertPath({ Radios: [{ Band: '5', Power: 10 }] }, 'Radios[5].Channel')).toEqual({ Radios: [{ Band: '5', Power: 10, Channel: '<value>' }] })
  })
})

describe('validators', () => {
  it('checks IP literals and names', () => {
    expect(isIPv4('192.0.2.1')).toBe(true)
    expect(isIPv4('192.0.2.256')).toBe(false)
    expect(isIPv4('01.2.3.4')).toBe(false)
    expect(isIPv6('2001:db8::1')).toBe(true)
    expect(isIPv6('::')).toBe(true)
    expect(isIPv6('::ffff:192.0.2.1')).toBe(true)
    expect(isIPv6('2001:db8::1::2')).toBe(false)
    expect(isIP('example')).toBe(false)
    expect(isHostname('pool.ntp.org')).toBe(true)
    expect(isHostname('-bad.example')).toBe(false)
    expect(isNTPServer('224.0.0.1')).toBe(false)
    expect(isNTPServer('255.255.255.255')).toBe(false)
    expect(isNTPServer('time.example.net')).toBe(true)
  })
  it('parses OpenSSH public keys, keeps comments and refuses private keys', async () => {
    const parsed = parseSSHKey(`${KEY} admin@example`)
    expect(parsed.ok && parsed.key).toMatchObject({ type: 'ssh-ed25519', comment: 'admin@example' })
    expect(parseSSHKey('-----BEGIN OPENSSH PRIVATE KEY-----')).toMatchObject({ ok: false })
    expect(parseSSHKey('ssh-rsa AAAAC3NzaC1lZDI1NTE5AAAAIEUEUFgIbAddz8QpHk7ULufpCRX9Obj5krYposbKm/YQ')).toMatchObject({ ok: false, error: 'The key data does not match its type.' })
    const many = parseSSHKeys(`# comment\n${KEY} a\n\nnot a key\n`)
    expect(many.keys).toHaveLength(1)
    expect(many.errors).toHaveLength(1)
    expect(await sshFingerprint(KEY.split(' ')[1])).toBe('SHA256:dEeUqHbpsbgeYPEMeQO6MlIJYO8jlEXMkvk8icjkrMg')
  })
  it('measures the AP secret and time ranges', () => {
    expect(secretByteLength('ab'.repeat(32))).toBe(32)
    expect(secretByteLength(btoa('x'.repeat(32)))).toBe(32)
    expect(secretByteLength('ab'.repeat(31))).toBe(31)
    expect(isTimeRange('08:00-17:30')).toBe(true)
    expect(isTimeRange('17:00-08:00')).toBe(false)
    expect(isTimeRange('8-5')).toBe(false)
  })
})

describe('experimental visibility', () => {
  it('treats empty, default and switched-off values as unset', async () => {
    const { isUnset } = await import('./fields')
    expect(isUnset(null, null)).toBe(true)
    expect(isUnset('none', 'none')).toBe(true)
    expect(isUnset(false, null)).toBe(true)
    expect(isUnset({ wlan_up: null, wlan_down: '' }, null)).toBe(true)
    expect(isUnset({ enabled: false, type: 'standard', vlan_ids: '30' }, null)).toBe(true)
    expect(isUnset({ enabled: false, private: false, keys: [] }, null)).toBe(true)
    expect(isUnset({ b24: -70, b5: null }, null)).toBe(false)
    expect(isUnset({ enabled: true }, null)).toBe(false)
    expect(isUnset('voice', 'none')).toBe(false)
  })
})
