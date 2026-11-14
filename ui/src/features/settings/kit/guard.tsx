import { useEffect, useSyncExternalStore } from 'react'
import { useBlocker } from '@tanstack/react-router'
import { deepEqual } from './patch'
import { ConfirmDialog } from './primitives'

// Unsaved changes (design §7.4): every config form registers its dirty state;
// one guard per page blocks navigation (TanStack Router useBlocker) and the
// tab closing (beforeunload) while any form is dirty. Hash-only moves (to a
// section anchor) are never blocked.

const dirty = new Set<string>()
const listeners = new Set<() => void>()

function emit() {
  listeners.forEach((l) => l())
}

export function setFormDirty(key: string, value: boolean) {
  const had = dirty.has(key)
  if (value === had) return
  if (value) dirty.add(key)
  else dirty.delete(key)
  emit()
}

/** Marks a form clean at once, so a navigation right after saving is not blocked. */
export function markClean(key: string) {
  setFormDirty(key, false)
}

export function anyFormDirty() {
  return dirty.size > 0
}

export function useDirtyRegistration(key: string, isDirty: boolean) {
  useEffect(() => {
    setFormDirty(key, isDirty)
  }, [key, isDirty])
  useEffect(() => () => setFormDirty(key, false), [key])
}

function useAnyDirty() {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l)
      return () => listeners.delete(l)
    },
    anyFormDirty,
    anyFormDirty,
  )
}

export function UnsavedChangesGuard() {
  // Subscribed so the beforeunload handler is armed while anything is dirty.
  useAnyDirty()
  const blocker = useBlocker({
    shouldBlockFn: ({ current, next }) =>
      anyFormDirty() && (current.pathname !== next.pathname || !deepEqual(current.search, next.search)),
    enableBeforeUnload: () => anyFormDirty(),
    withResolver: true,
  })
  return (
    <ConfirmDialog
      open={blocker.status === 'blocked'}
      onOpenChange={(open) => {
        if (!open && blocker.status === 'blocked') blocker.reset()
      }}
      title="Discard changes?"
      description="You have unsaved changes on this page. If you leave now they are lost."
      action="Discard changes"
      cancel="Keep editing"
      destructive
      onConfirm={() => {
        if (blocker.status !== 'blocked') return
        dirty.clear()
        emit()
        blocker.proceed()
      }}
    />
  )
}
