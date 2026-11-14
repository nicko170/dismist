import { describe, expect, it } from 'vitest'
import { loginSchema } from './schema'

const parse = (token: string) => loginSchema.safeParse({ username: 'admin', token })

describe('loginSchema', () => {
  it('accepts the 64-hex admin token and dsm_ API tokens', () => {
    expect(parse('ab'.repeat(32)).success).toBe(true)
    expect(parse(` ${'AB'.repeat(32)} `).success).toBe(true)
    expect(parse('dsm_abc123_secretpart').success).toBe(true)
  })

  it('explains what is wrong', () => {
    expect(parse('').error?.issues[0].message).toBe('Enter your token.')
    expect(parse('abc').error?.issues[0].message).toMatch(/64-character admin token/)
  })
})
