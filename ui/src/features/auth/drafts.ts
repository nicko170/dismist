import { useEffect, useRef } from 'react'

// Unsaved form drafts survive an expired session (design §2.4, §7.4): forms
// register a reader while dirty; on a 401 the handler saves every reader's
// value to sessionStorage, and the form offers it back after sign-in.

const PREFIX = 'dismist.draft:'
const readers = new Map<string, () => unknown>()

export function registerDraft(key: string, read: () => unknown): () => void {
  readers.set(key, read)
  return () => {
    if (readers.get(key) === read) readers.delete(key)
  }
}

export function saveDrafts() {
  for (const [key, read] of readers) {
    try {
      const value = read()
      if (value !== undefined) sessionStorage.setItem(PREFIX + key, JSON.stringify({ at: Date.now(), value }))
    } catch {
      // A draft that cannot be serialised is dropped rather than blocking sign-out.
    }
  }
}

/** Returns and removes a saved draft. */
export function takeDraft<T>(key: string): T | null {
  try {
    const raw = sessionStorage.getItem(PREFIX + key)
    if (!raw) return null
    sessionStorage.removeItem(PREFIX + key)
    return (JSON.parse(raw) as { value: T }).value
  } catch {
    return null
  }
}

/** Registers `read` under `key` while `enabled` (typically formState.isDirty). */
export function useDraft(key: string, read: () => unknown, enabled: boolean) {
  const latest = useRef(read)
  useEffect(() => {
    latest.current = read
  })
  useEffect(() => {
    if (!enabled) return
    return registerDraft(key, () => latest.current())
  }, [key, enabled])
}
