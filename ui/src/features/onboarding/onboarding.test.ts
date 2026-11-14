import { describe, expect, it } from 'vitest'
import type { WLANLive } from '@/gen/api.gen'
import { buildOnboardCommand, parseAPAddress, shellQuote } from './command'
import { decodeSecret, formField, isIPLiteral, registerSchema, toAPInput, toHex } from './schema'
import { NO_HELLO_HINT_MS, describeBroadcast, verifyFinished, verifySteps, type VerifyInput } from './verify'

const hex = 'a0a1a2a3a4a5a6a7a8a9aaabacadaeafb0b1b2b3b4b5b6b7b8b9babbbcbdbebf'
const bytes = Uint8Array.from({ length: 32 }, (_, i) => 0xa0 + i)
const base64 = btoa(String.fromCharCode(...bytes))

describe('decodeSecret', () => {
  it('accepts hex in any case, with spaces or colons, and base64 (standard or URL-safe, padding optional)', () => {
    expect(decodeSecret(hex)).toEqual(bytes)
    expect(decodeSecret(hex.toUpperCase())).toEqual(bytes)
    expect(decodeSecret(hex.match(/../g)!.join(':'))).toEqual(bytes)
    expect(decodeSecret(` ${hex.slice(0, 32)}\n${hex.slice(32)} `)).toEqual(bytes)
    expect(decodeSecret(base64)).toEqual(bytes)
    expect(decodeSecret(base64.replace(/=+$/, ''))).toEqual(bytes)
    expect(decodeSecret(base64.replace(/\+/g, '-').replace(/\//g, '_'))).toEqual(bytes)
    expect(toHex(decodeSecret(hex)!)).toBe(hex)
  })

  it('rejects junk and reports the decoded length otherwise', () => {
    expect(decodeSecret('')).toBeNull()
    expect(decodeSecret('not a secret!')).toBeNull()
    expect(decodeSecret(hex.slice(2))?.length).not.toBe(32)
  })
})

describe('isIPLiteral', () => {
  it('accepts IPv4 and IPv6 literals only', () => {
    expect(['192.0.2.1', '198.51.100.255', '2001:db8::1', '::1'].filter((v) => !isIPLiteral(v))).toEqual([])
    expect(['192.0.2.256', '192.0.2', '01.2.3.4', 'example.net', '2001:db8::zz', ''].filter(isIPLiteral)).toEqual([])
  })
})

describe('registerSchema', () => {
  const base = { id: 'AA:BB:CC:00:00:01', name: ' Hallway ', site_id: 'home', secret: hex, pinned_ip: '' }

  it('normalises the ID and sends the secret as hex', () => {
    const schema = registerSchema({ dnsMode: false, registered: new Set() })
    const values = schema.parse({ ...base, secret: base64 })
    expect(toAPInput(values)).toEqual({ id: 'aa-bb-cc-00-00-01', name: 'Hallway', site_id: 'home', secret: hex })
    expect(toAPInput(schema.parse({ ...base, pinned_ip: '198.51.100.7' })).management_ip).toBe('198.51.100.7')
  })

  it('reports each problem on its own field', () => {
    const schema = registerSchema({ dnsMode: false, registered: new Set(['aa-bb-cc-00-00-01']) })
    const result = schema.safeParse({ id: 'aabbcc000001', name: '', site_id: '', secret: 'abcd', pinned_ip: 'nope' })
    expect(result.success).toBe(false)
    const fields = Object.fromEntries(result.error!.issues.map((issue) => [issue.path[0], issue.message]))
    expect(fields.id).toMatch(/already registered/)
    expect(fields.name).toMatch(/name/)
    expect(fields.site_id).toMatch(/site/)
    expect(fields.secret).toMatch(/32 bytes/)
    expect(fields.pinned_ip).toMatch(/IP address/)
  })

  it('requires the pin in DNS mode', () => {
    const dns = registerSchema({ dnsMode: true, registered: new Set() })
    expect(dns.safeParse(base).error?.issues[0]).toMatchObject({ path: ['pinned_ip'] })
    expect(dns.safeParse({ ...base, pinned_ip: '198.51.100.7' }).success).toBe(true)
  })

  it('maps server field names onto form fields', () => {
    expect(formField('management_ip')).toBe('pinned_ip')
    expect(formField('secret')).toBe('secret')
    expect(formField('model')).toBeNull()
  })
})

describe('onboard command', () => {
  it('quotes only what the shell would split', () => {
    expect(shellQuote('Home')).toBe('Home')
    expect(shellQuote('Front room')).toBe(`'Front room'`)
    expect(shellQuote(`Nick's`)).toBe(`'Nick'\\''s'`)
  })

  it('parses AP addresses', () => {
    expect(parseAPAddress('192.0.2.10')).toEqual({ user: 'root', host: '192.0.2.10' })
    expect(parseAPAddress('admin@ap.example.net')).toEqual({ user: 'admin', host: 'ap.example.net' })
    expect(parseAPAddress('[2001:db8::10]')).toEqual({ user: 'root', host: '2001:db8::10' })
    expect(['', '192.0.2.300', 'bad host', 'root@', '-rf@x'].filter((v) => parseAPAddress(v) !== null)).toEqual([])
  })

  it('renders the command of design §3.15', () => {
    expect(
      buildOnboardCommand({ address: '192.0.2.10', controller: 'https://dismist.example.net/', site: 'Home', name: 'Front room' }),
    ).toBe(`dismist ap33 onboard root@192.0.2.10 --controller https://dismist.example.net --token-file ~/.config/dismist/token --site Home --name 'Front room'`)
    expect(buildOnboardCommand({ address: 'nope nope', controller: 'https://x', site: 'Home', name: 'A' })).toBeNull()
  })
})

const fresh: VerifyInput = {
  id: '02-aa-00-00-00-20',
  name: 'Garage',
  status: 'never_connected',
  connected: false,
  connected_at: null,
  last_seen: null,
  model: '',
  firmware: '',
  config_revision: 43,
  applied_revision: 0,
  last_error: '',
  radios: [],
  wlans_live: [],
}

describe('verifySteps', () => {
  const states = (input: VerifyInput, waited = 0) => verifySteps(input, waited).map((step) => `${step.id}:${step.state}`)

  it('walks hello, delivery, apply and broadcast', () => {
    expect(states(fresh)).toEqual(['registered:done', 'hello:active', 'delivered:waiting', 'applied:waiting', 'broadcasting:waiting'])
    const authed = { ...fresh, status: 'pending' as const, connected: true, connected_at: '2026-11-14T05:00:00Z', model: 'AP33-WW', firmware: 'apfw-0.15.35064-noro-84df' }
    expect(verifySteps(authed)[1].label).toBe('Authenticated (AP33-WW, apfw-0.15.35064-noro-84df)')
    expect(states(authed)).toEqual(['registered:done', 'hello:done', 'delivered:done', 'applied:active', 'broadcasting:waiting'])
    const applied = { ...authed, status: 'in_sync' as const, applied_revision: 43 }
    expect(states(applied).at(-1)).toBe('broadcasting:active')
    const wlans: WLANLive[] = [
      { wlan_id: 'w1', ssid: 'Example', band: '5', bssid: '02-aa-00-00-01-10', dev: 'wl1.1', clients: 0 },
      { wlan_id: 'w1', ssid: 'Example', band: '2.4', bssid: '02-aa-00-00-01-00', dev: 'wl0.1', clients: 0 },
    ]
    const broadcasting = verifySteps({ ...applied, wlans_live: wlans })
    expect(broadcasting.at(-1)?.label).toBe('Broadcasting Example on 2.4 and 5 GHz')
    expect(verifyFinished(broadcasting)).toBe('ok')
  })

  it('stops on authentication, source, compile and apply errors', () => {
    expect(verifySteps({ ...fresh, status: 'auth_failing' })[1]).toMatchObject({ state: 'error', label: 'Authentication failed' })
    expect(verifySteps({ ...fresh, status: 'source_mismatch' })[1].state).toBe('error')
    const connected = { ...fresh, connected: true, connected_at: '2026-11-14T05:00:00Z', firmware: 'x' }
    expect(verifySteps({ ...connected, status: 'config_error', last_error: 'bad patch' })[2]).toMatchObject({ state: 'error', detail: 'bad patch' })
    const rejected = verifySteps({ ...connected, status: 'rejected', last_error: 'Can’t unmarshal' })
    expect(rejected[3]).toMatchObject({ state: 'error' })
    expect(verifyFinished(rejected)).toBe('error')
  })

  it('explains what the controller cannot see after a long wait', () => {
    expect(verifySteps(fresh, 1000)[1].detail).toMatch(/two minutes/)
    expect(verifySteps(fresh, NO_HELLO_HINT_MS)[1].detail).toMatch(/TLS failures never reach the controller/)
  })

  it('describes several WLANs', () => {
    expect(
      describeBroadcast([
        { wlan_id: 'a', ssid: 'Example', band: '5', bssid: '', dev: '', clients: 0 },
        { wlan_id: 'b', ssid: 'Guest', band: '2.4', bssid: '', dev: '', clients: 0 },
      ]),
    ).toBe('Example on 5 GHz; Guest on 2.4 GHz')
  })
})
