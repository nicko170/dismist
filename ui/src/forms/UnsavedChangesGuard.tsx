import { useEffect, useRef } from 'react'
import { useBlocker } from '@tanstack/react-router'
import type { FieldValues, UseFormReturn } from 'react-hook-form'
import { AlertDialog, AlertDialogContent, AlertDialogDescription, AlertDialogTitle } from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { notify } from '@/components/app/toast'
import { takeDraft, useDraft } from '@/features/auth/drafts'

// Unsaved changes (design §7.4): while the form is dirty, leaving the page
// asks "Discard changes?" (router navigation) or the browser's own prompt
// (reload, close). Hash and search changes on the same page (#security,
// ?view=) never ask. The draft is registered so a session expiry keeps it
// in sessionStorage, and `useRestoredDraft` offers it back after sign-in.

export function UnsavedChangesGuard({ when, draft }: { when: boolean; draft?: { key: string; read: () => unknown } }) {
  const latest = useRef(when)
  useEffect(() => {
    latest.current = when
  })
  useDraft(draft?.key ?? '', draft?.read ?? (() => undefined), Boolean(draft) && when)
  const blocker = useBlocker({
    shouldBlockFn: ({ current, next }) => latest.current && current.pathname !== next.pathname,
    enableBeforeUnload: () => latest.current,
    withResolver: true,
  })
  return (
    <AlertDialog open={blocker.status === 'blocked'} onOpenChange={(open) => !open && blocker.reset?.()}>
      <AlertDialogContent>
        <div className="grid gap-2">
          <AlertDialogTitle>Discard changes?</AlertDialogTitle>
          <AlertDialogDescription>You have unsaved changes on this page. Leaving discards them.</AlertDialogDescription>
        </div>
        <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
          <Button variant="outline" onClick={() => blocker.reset?.()}>
            Keep editing
          </Button>
          <Button variant="destructive" onClick={() => blocker.proceed?.()}>
            Discard
          </Button>
        </div>
      </AlertDialogContent>
    </AlertDialog>
  )
}

/**
 * Restores a draft saved when the session expired (once, on mount): the
 * form shows the restored values as unsaved changes against the loaded
 * defaults.
 */
export function useRestoredDraft<T extends FieldValues>(key: string, form: UseFormReturn<T, unknown, unknown>, ready = true) {
  const done = useRef(false)
  useEffect(() => {
    if (!ready || done.current) return
    done.current = true
    const draft = takeDraft<T>(key)
    if (!draft) return
    form.reset(draft, { keepDefaultValues: true })
    notify.info('Restored your unsaved changes', 'Your session ended before you saved. Review and save them.')
  }, [key, form, ready])
}
