import { useRef, useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import type { FieldValues, UseFormReturn } from 'react-hook-form'
import { applyFieldErrors, describeError, isApiError } from '@/api/errors'
import { notify } from '@/components/app/toast'
import { fieldPaths } from './Form'
import { StaleDialog } from './StaleDialog'

// Saving a form (design §7.4 "Errors", "Pending state"): one mutation per
// form; 400/422 field errors land on their fields (the rest in the form
// alert); 409 stale opens Reload / Overwrite; success toasts and makes the
// saved values the new baseline, so the form is clean again.

export type SubmitOptions = {
  /** The object's updated_rev to send as If-Match (Overwrite sends the current one). */
  ifMatch?: number
}

/** The current object a 409 stale response carries (`object`, or the body itself). */
export function staleObject(error: unknown): unknown {
  if (!isApiError(error) || typeof error.body !== 'object' || error.body === null) return null
  const body = error.body as { object?: unknown; current?: unknown }
  return body.object ?? body.current ?? null
}

export function useFormSubmit<TIn extends FieldValues, TOut extends FieldValues, TResult>({
  form,
  mutationFn,
  ifMatch,
  fieldMap,
  successMessage,
  objectName,
  onSuccess,
  onReload,
}: {
  form: UseFormReturn<TIn, unknown, TOut>
  mutationFn: (values: TOut, options: SubmitOptions) => Promise<TResult>
  /** The loaded object's updated_rev. */
  ifMatch?: number
  /** Server JSON path → form field name, where they differ. */
  fieldMap?: Record<string, string>
  successMessage?: string | ((result: TResult) => string)
  /** Names the object in the stale dialog ("WLAN INTERNET"). */
  objectName?: string
  onSuccess?: (result: TResult, values: TOut) => void
  /** Reload after a stale conflict: reset the form from the current object. */
  onReload?: (current: unknown) => void
}) {
  const [alert, setAlert] = useState<string[]>([])
  const [stale, setStale] = useState<{ current: unknown } | null>(null)
  const last = useRef<TOut | null>(null)

  const mutation = useMutation({
    mutationFn: ({ values, options }: { values: TOut; options: SubmitOptions }) => mutationFn(values, options),
    onSuccess: (result, { values }) => {
      setAlert([])
      setStale(null)
      form.reset(form.getValues(), { keepValues: true })
      if (successMessage) notify.success(typeof successMessage === 'function' ? successMessage(result) : successMessage)
      onSuccess?.(result, values)
    },
    onError: (error) => {
      if (isApiError(error) && error.status === 409 && error.code === 'stale') {
        setStale({ current: staleObject(error) })
        return
      }
      if (isApiError(error) && (error.status === 400 || error.status === 422) && Object.keys(error.fields).length) {
        setAlert(applyFieldErrors(error, form.setError as never, fieldPaths(form.getValues()), fieldMap))
        return
      }
      setAlert([describeError(error).message + (isApiError(error) && error.code ? ` (${error.code})` : '')])
    },
  })

  const submit = (values: TOut) => {
    last.current = values
    setAlert([])
    mutation.mutate({ values, options: { ifMatch } })
  }

  const currentRev = (stale?.current as { updated_rev?: number } | null)?.updated_rev
  const staleDialog = (
    <StaleDialog
      open={stale !== null}
      objectName={objectName}
      pending={mutation.isPending}
      onCancel={() => setStale(null)}
      onReload={() => {
        const current = stale?.current
        setStale(null)
        onReload?.(current)
      }}
      onOverwrite={() => {
        if (!last.current) return
        mutation.mutate({ values: last.current, options: { ifMatch: currentRev } })
      }}
    />
  )

  return { submit, mutation, alert, setAlert, staleDialog, pending: mutation.isPending }
}
