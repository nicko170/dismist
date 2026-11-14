import { describe, expect, it } from 'vitest'
import { csvCell, toCSV } from './csv'
import { childKeys, forbiddenMatch, insertPath, isArraySegment, keyPaths } from './jsonpath'
import { parseSSHKey, sshFingerprint, sshTypeLabel } from './ssh'
import { escapeWifi, wifiQRPayload } from './wifi'

// A syntactically valid ed25519 public key blob (type string + 32 zero bytes); synthetic.
const blob = btoa(String.fromCharCode(0, 0, 0, 11) + 'ssh-ed25519' + String.fromCharCode(0, 0, 0, 32) + '\0'.repeat(32))

describe('jsonpath', () => {
  const paths = ['LED.Brightness', 'LED.Locator', 'Radios[].Channel', 'Radios[].RRM.Channel', 'SSH.Disabled']

  it('matches forbidden paths case-insensitively, descendants included', () => {
    expect(forbiddenMatch('wlans[].SSID', ['WLANs'])).toBe('WLANs')
    expect(forbiddenMatch('Timestamp', ['WLANs', 'Timestamp'])).toBe('Timestamp')
    expect(forbiddenMatch('WLANsExtra', ['WLANs'])).toBeNull()
  })

  it('lists child keys for completion', () => {
    expect(childKeys(paths, '')).toEqual(['LED', 'Radios', 'SSH'])
    expect(childKeys(paths, 'led')).toEqual(['Brightness', 'Locator'])
    expect(childKeys(paths, 'Radios[]')).toEqual(['Channel', 'RRM'])
    expect(isArraySegment(paths, '', 'Radios')).toBe(true)
    expect(isArraySegment(paths, '', 'LED')).toBe(false)
  })

  it('inserts a path with a null leaf, reusing existing keys by case', () => {
    const out = JSON.parse(insertPath('{"led":{"Brightness":10}}', 'LED.Locator'))
    expect(out).toEqual({ led: { Brightness: 10, Locator: null } })
    expect(JSON.parse(insertPath('', 'Radios[].RRM.Channel'))).toEqual({ Radios: [{ RRM: { Channel: null } }] })
    expect(() => insertPath('[1]', 'LED.Locator')).toThrow('must be a JSON object')
  })

  it('collects key paths', () => {
    expect(keyPaths({ LED: { Brightness: 1 }, Radios: [{ Channel: 1 }] })).toEqual(['LED', 'LED.Brightness', 'Radios[]', 'Radios[].Channel'])
  })
})

describe('csv', () => {
  it('quotes and defuses formulas', () => {
    expect(csvCell('a,b')).toBe('"a,b"')
    expect(csvCell('say "hi"')).toBe('"say ""hi"""')
    expect(csvCell('=1+1')).toBe("'=1+1")
    expect(csvCell(null)).toBe('')
    expect(toCSV([{ n: 'Hallway', c: 3 }], [{ header: 'Name', value: (r) => r.n }, { header: 'Clients', value: (r) => r.c }])).toBe('Name,Clients\r\nHallway,3\r\n')
  })
})

describe('wifi QR', () => {
  it('escapes and builds the join string', () => {
    expect(escapeWifi('a;b,c:d"e\\f')).toBe('a\\;b\\,c\\:d\\"e\\\\f')
    expect(wifiQRPayload({ ssid: 'Example', passphrase: 'correct horse' })).toBe('WIFI:T:WPA;S:Example;P:correct horse;;')
    expect(wifiQRPayload({ ssid: 'Open', hidden: true })).toBe('WIFI:T:nopass;S:Open;H:true;;')
  })
})

describe('ssh keys', () => {
  it('parses a key and checks the embedded type', () => {
    const key = parseSSHKey(`ssh-ed25519 ${blob} you@laptop`)
    expect(key?.comment).toBe('you@laptop')
    expect(parseSSHKey(`ssh-rsa ${blob}`)).toBeNull()
    expect(parseSSHKey('nonsense')).toBeNull()
    expect(sshTypeLabel('ecdsa-sha2-nistp256')).toBe('ecdsa-256')
  })

  it('fingerprints like ssh-keygen', async () => {
    expect(await sshFingerprint(blob)).toMatch(/^SHA256:[A-Za-z0-9+/]{43}$/)
  })
})
