import { describe, expect, it } from 'vitest'
import { isTypingTarget, shortcutFor } from './shortcuts'

describe('keyboard shortcuts', () => {
  it('g arms, then a letter goes to a section', () => {
    expect(shortcutFor('g', false)).toEqual({ action: 'arm' })
    expect(shortcutFor('a', true)).toEqual({ action: 'go', target: 'aps' })
    expect(shortcutFor('w', true)).toEqual({ action: 'go', target: 'wlans' })
    expect(shortcutFor('a', false)).toEqual({ action: null })
    expect(shortcutFor('?', false)).toEqual({ action: 'help' })
  })

  it('never takes keys typed into fields', () => {
    const input = document.createElement('input')
    const div = document.createElement('div')
    expect(isTypingTarget(input)).toBe(true)
    expect(isTypingTarget(div)).toBe(false)
  })
})
