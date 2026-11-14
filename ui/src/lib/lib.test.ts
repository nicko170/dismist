import { describe, expect, it } from 'vitest'
import { formatBytes, formatDuration, utf8Length } from './format'
import { isPrivateMac, normaliseMac } from './mac'
import { formatRelative, parseTime } from './time'
import { resolveBasepath } from '@/router'

describe('mac', () => {
  it('normalises to lower-case dashed form', () => {
    expect(normaliseMac('02:AA:BB:CC:DD:EE')).toBe('02-aa-bb-cc-dd-ee')
    expect(normaliseMac('02aa.bbcc.ddee')).toBe('02-aa-bb-cc-dd-ee')
    expect(normaliseMac('nope')).toBeNull()
    expect(isPrivateMac('02-aa-bb-cc-dd-ee')).toBe(true)
    expect(isPrivateMac('00-aa-bb-cc-dd-ee')).toBe(false)
  })
})

describe('time', () => {
  const now = Date.parse('2026-11-14T12:00:00Z')
  it('formats relative times', () => {
    expect(formatRelative(new Date(now - 2_000), now)).toBe('just now')
    expect(formatRelative(new Date(now - 12_000), now)).toBe('12 s ago')
    expect(formatRelative(new Date(now - 5 * 60_000), now)).toBe('5 m ago')
    expect(formatRelative(new Date(now - 3 * 86400_000), now)).toBe('3 d ago')
    expect(formatRelative(new Date(now + 120_000), now)).toBe('in 2 m')
  })
  it('treats Go zero time as never', () => {
    expect(parseTime('0001-01-01T00:00:00Z')).toBeNull()
    expect(parseTime(null)).toBeNull()
  })
})

describe('format', () => {
  it('formats bytes, durations and UTF-8 length', () => {
    expect(formatBytes(1536)).toBe('1.5 KiB')
    expect(formatDuration(3 * 86400 + 4 * 3600 + 5)).toBe('3 d 4 h')
    expect(formatDuration(0)).toBe('0 s')
    expect(utf8Length('Café')).toBe(5)
  })
})

describe('resolveBasepath', () => {
  it('serves /next/ and / from one bundle', () => {
    expect(resolveBasepath('/next')).toBe('/next')
    expect(resolveBasepath('/next/sites/home/aps')).toBe('/next')
    expect(resolveBasepath('/nextcloud')).toBe('/')
    expect(resolveBasepath('/sites/home')).toBe('/')
  })
})
