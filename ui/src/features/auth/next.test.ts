import { describe, expect, it } from 'vitest'
import { safeNext } from './next'

describe('safeNext', () => {
  it.each([
    ['/sites/home/aps?status=offline', '/sites/home/aps?status=offline'],
    ['/', '/'],
    [undefined, '/'],
    ['https://example.net/', '/'],
    ['//example.net/', '/'],
    ['/\\example.net', '/'],
    ['/login', '/'],
    ['/login?next=/x', '/'],
    ['/login/again', '/'],
    ['/loginx', '/loginx'],
    ['/a\nb', '/'],
    ['sites', '/'],
  ])('%s → %s', (input, expected) => {
    expect(safeNext(input)).toBe(expected)
  })
})
