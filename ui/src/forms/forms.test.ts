import { describe, expect, it } from 'vitest'
import { countDirty, fieldPaths, flattenErrors } from './Form'
import { describeRange, formatNumberInput, inputAttributes, numberMode, parseNumberInput, sanitiseNumberText, toggleSign } from './number'
import { generatePassphrase, passphraseProblem, secretByteLength } from './secret'
import { apSecret, host, ip, ipv4, mac, numberInRange, passphraseOrKeep, ssid, sshPublicKey, vlanId } from './validators'

describe('NumberField modes (design §7.4)', () => {
  it('chooses the keypad from the bounds', () => {
    expect(numberMode(0, 255)).toBe('unsigned')
    expect(numberMode(null, null)).toBe('unsigned')
    expect(numberMode(-95, -50)).toBe('negative')
    expect(numberMode(-10, 10)).toBe('signed')
    expect(inputAttributes('unsigned')).toEqual({ inputMode: 'numeric', pattern: '[0-9]*' })
    expect(inputAttributes('negative')).toEqual({ inputMode: 'numeric', pattern: '[0-9]*' })
    expect(inputAttributes('signed').inputMode).toBe('text')
  })

  it('negative-only ranges type the magnitude and store it negated', () => {
    expect(parseNumberInput('72', 'negative')).toBe(-72)
    expect(parseNumberInput('-72', 'negative')).toBe(-72)
    expect(formatNumberInput(-72, 'negative')).toBe('72')
    expect(parseNumberInput('', 'negative')).toBeNull()
  })

  it('signed ranges accept a minus, the typographic minus and the ± toggle', () => {
    expect(parseNumberInput('-3', 'signed')).toBe(-3)
    expect(parseNumberInput('\u22123', 'signed')).toBe(-3)
    expect(toggleSign('3')).toBe('-3')
    expect(toggleSign('-3')).toBe('3')
    expect(parseNumberInput('-', 'signed')).toBeNull()
  })

  it('reports text that is not a number as NaN for the schema', () => {
    expect(parseNumberInput('1.5', 'unsigned')).toBeNaN()
    expect(parseNumberInput('1.5', 'unsigned', true)).toBe(1.5)
    expect(parseNumberInput('1,5', 'unsigned', true)).toBe(1.5)
    expect(parseNumberInput('abc', 'signed')).toBeNaN()
  })

  it('drops characters the mode cannot type', () => {
    expect(sanitiseNumberText('1a2-3', 'unsigned')).toBe('123')
    expect(sanitiseNumberText('-1-2', 'signed')).toBe('-12')
    expect(sanitiseNumberText('\u22125', 'signed')).toBe('-5')
  })

  it('describes ranges with a typographic minus', () => {
    expect(describeRange(-95, -50, 'dBm')).toBe('\u221295 to \u221250 dBm')
    expect(describeRange(1, 25)).toBe('1 to 25')
  })
})

describe('secrets', () => {
  it('generates passphrases of the requested length from the safe alphabet', () => {
    const value = generatePassphrase(24)
    expect(value).toHaveLength(24)
    expect(value).toMatch(/^[a-km-zA-HJ-NP-Z2-9]+$/)
    expect(passphraseProblem(value)).toBeNull()
  })

  it('counts secret bytes in hex and base64', () => {
    expect(secretByteLength('ab'.repeat(32))).toBe(32)
    expect(secretByteLength(btoa('x'.repeat(32)))).toBe(32)
    expect(secretByteLength('not base64 !')).toBeNull()
  })

  it('applies the WPA passphrase rules', () => {
    expect(passphraseProblem('short')).toMatch(/at least 8/)
    expect(passphraseProblem('x'.repeat(64))).toMatch(/at most 63/)
    expect(passphraseProblem('f'.repeat(64))).toBeNull()
    expect(passphraseProblem('pässwörd123')).toMatch(/ASCII/)
  })
})

describe('validators', () => {
  it('IP, host and VLAN', () => {
    expect(ipv4.safeParse('192.0.2.1').success).toBe(true)
    expect(ipv4.safeParse('192.0.2.256').success).toBe(false)
    expect(ip.safeParse('2001:db8::1').success).toBe(true)
    expect(host.safeParse('ntp.example.com').success).toBe(true)
    expect(host.safeParse('-bad-.example').success).toBe(false)
    expect(vlanId.safeParse(4094).success).toBe(true)
    expect(vlanId.safeParse(4095).success).toBe(false)
  })

  it('MACs come out canonical', () => {
    expect(mac.parse('02:AA:00:00:00:01')).toBe('02-aa-00-00-00-01')
    expect(mac.safeParse('02:AA').success).toBe(false)
  })

  it('SSIDs count UTF-8 bytes', () => {
    expect(ssid.safeParse('Example').success).toBe(true)
    expect(ssid.safeParse('é'.repeat(16)).success).toBe(true)
    expect(ssid.safeParse('é'.repeat(17)).success).toBe(false)
  })

  it('write-only passphrases accept empty (keep)', () => {
    expect(passphraseOrKeep.safeParse('').success).toBe(true)
    expect(passphraseOrKeep.safeParse('short').success).toBe(false)
  })

  it('AP secrets are 32 bytes', () => {
    expect(apSecret.safeParse('ab'.repeat(32)).success).toBe(true)
    expect(apSecret.safeParse('ab'.repeat(31)).success).toBe(false)
  })

  it('SSH keys and number ranges', () => {
    expect(sshPublicKey.safeParse('ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample you@laptop').success).toBe(true)
    expect(sshPublicKey.safeParse('hello').success).toBe(false)
    const power = numberInRange(1, 25, 'dBm')
    expect(power.safeParse(17).success).toBe(true)
    expect(power.safeParse(0).success).toBe(false)
    expect(power.safeParse(Number.NaN).success).toBe(false)
  })
})

describe('form helpers', () => {
  it('counts changed fields, a changed list once', () => {
    expect(countDirty({ ssid: true, bands: [true, false], radius: { host: true, port: false } })).toBe(3)
    expect(countDirty({})).toBe(0)
  })

  it('flattens nested errors in order', () => {
    expect(flattenErrors({ ssid: { type: 'x', message: 'Enter a name.' }, radius: { host: { type: 'x', message: 'Bad host.' } } } as never)).toEqual([
      ['ssid', 'Enter a name.'],
      ['radius.host', 'Bad host.'],
    ])
  })

  it('lists every field path', () => {
    expect(fieldPaths({ ssid: 'x', radius: { auth: [{ host: 'h' }] } })).toEqual(['ssid', 'radius', 'radius.auth', 'radius.auth.0', 'radius.auth.0.host'])
  })
})
