// Mock session state. In the browser it lives in localStorage so Playwright
// can start a test signed in (`dismist.mock.session` = "1") and survive
// reloads; under Vitest it is a plain variable.

const KEY = 'dismist.mock.session'
let memory = false

function storage(): Storage | null {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage
  } catch {
    return null
  }
}

export const mockState = {
  signedIn(): boolean {
    const store = storage()
    return store ? store.getItem(KEY) === '1' : memory
  },
  setSignedIn(value: boolean) {
    const store = storage()
    if (store) {
      if (value) store.setItem(KEY, '1')
      else store.removeItem(KEY)
    } else memory = value
  },
  failedLogins: 0,
}
