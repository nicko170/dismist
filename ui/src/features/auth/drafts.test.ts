import { describe, expect, it } from 'vitest'
import { registerDraft, saveDrafts, takeDraft } from './drafts'

describe('drafts', () => {
  it('saves registered readers on expiry and hands the value back once', () => {
    const unregister = registerDraft('/sites/home/wlans/new', () => ({ ssid: 'Example' }))
    saveDrafts()
    unregister()
    expect(takeDraft('/sites/home/wlans/new')).toEqual({ ssid: 'Example' })
    expect(takeDraft('/sites/home/wlans/new')).toBeNull()
  })
})
