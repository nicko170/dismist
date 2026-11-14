import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useForm, useWatch, type DefaultValues, type FieldValues, type Path, type Resolver, type UseFormReturn } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import type { z } from 'zod'
import { isApiError, type ApiError } from '@/api/errors'
import { keys } from '@/api/keys'
import type { MutationOptions } from '@/api/queries/sites'
import type { MutationResponse } from '@/gen/api.gen'
import { takeDraft, useDraft } from '@/features/auth/drafts'
import { markClean, useDirtyRegistration } from './guard'
import { getOption, hasOption, isExperimentalValue } from './options'
import { changedPaths, deepEqual, mergePatch } from './patch'

// One config form (design §3.0 "Saving", §7.4): react-hook-form with the zod
// schema, an explicit Save, a 600 ms debounced dry run while the form is dirty
// and valid, If-Match concurrency with Reload/Overwrite on 409 stale, server
// field errors mapped onto fields, unsaved-change guard and session drafts,
// and the one-time experimental acknowledgement per form.

export type SubmitFn<TObject> = (body: unknown, options: MutationOptions) => Promise<MutationResponse<TObject>>

export type ImpactState<TObject> =
  | { status: 'idle' }
  /** messages: problems on values no visible field shows (empty: the highlighted fields say it). */
  | { status: 'invalid'; messages?: string[] }
  | { status: 'loading' }
  | { status: 'ready'; data: MutationResponse<TObject> }
  | { status: 'error'; error: ApiError | Error }

export type ConfirmRequest = { title: string; description: string; action: string; destructive?: boolean }

export type UseConfigFormOptions<V extends FieldValues, TObject> = {
  /** Draft key, dirty-registry key and experimental-ack key. */
  formKey: string
  /** Option key prefix for the experimental and field-error mapping ("wlan", "site", "site.rf", "ap"). */
  scope: string
  values: V
  schema: z.ZodType
  submit: SubmitFn<TObject>
  /** Builds the request body; default is the merge patch of the changes. */
  toBody?: (patch: Record<string, unknown>, values: V) => unknown
  /** Turns the mutation's object back into form values. */
  toValues?: (object: TObject) => V
  ifMatch?: number
  readOnly?: boolean
  /** Ask before saving (disruptive changes, §3.0). */
  confirm?: (values: V, base: V) => ConfirmRequest | null
  onSaved?: (response: MutationResponse<TObject>, values: V) => void
  /** Server JSON paths that do not match form field names. */
  fieldMap?: Record<string, string>
  /** Skip the dry run (create forms whose dry run needs every field). */
  dryRun?: boolean
  successMessage?: (response: MutationResponse<TObject>) => string
  /** Site whose push progress the save bar follows. */
  siteId?: string
}

export type ConfigFormController<V extends FieldValues, TObject> = {
  form: UseFormReturn<V>
  formKey: string
  base: V
  dirty: boolean
  changed: string[]
  impact: ImpactState<TObject>
  saving: boolean
  readOnly: boolean
  formError: string[]
  setFormError: (messages: string[]) => void
  save: (event?: React.BaseSyntheticEvent) => Promise<void>
  discard: () => void
  stale: { current: unknown } | null
  resolveStale: (choice: 'reload' | 'overwrite' | 'cancel') => void
  ack: { labels: string[] } | null
  resolveAck: (accepted: boolean) => void
  confirming: ConfirmRequest | null
  resolveConfirm: (accepted: boolean) => void
  draft: V | null
  resolveDraft: (restore: boolean) => void
  lastSaved: MutationResponse<TObject> | null
  siteId?: string
}

const ACK_PREFIX = 'dismist.experimental-ack:'
/** The prefix features/auth/drafts.ts stores drafts under (WS-D). */
const DRAFT_PREFIX = 'dismist.draft:'

function peekDraft<T>(key: string): T | null {
  try {
    const raw = sessionStorage.getItem(DRAFT_PREFIX + key)
    return raw ? ((JSON.parse(raw) as { value: T }).value ?? null) : null
  } catch {
    return null
  }
}

function hasAck(key: string) {
  try {
    return localStorage.getItem(ACK_PREFIX + key) === '1'
  } catch {
    return false
  }
}

function storeAck(key: string) {
  try {
    localStorage.setItem(ACK_PREFIX + key, '1')
  } catch {
    // Without storage the form asks again next time.
  }
}

/** Names of the fields the form renders (registered inputs and Controllers). */
function mountedFields<V extends FieldValues>(form: UseFormReturn<V>): Set<string> | null {
  // react-hook-form keeps the mounted field names on control._names.mount
  // (v7); no public API lists them. Without it, nothing is reported.
  // oxlint-disable-next-line no-underscore-dangle
  const names = (form.control as unknown as { _names?: { mount?: Set<string> } })._names?.mount
  return names instanceof Set ? names : null
}

/** Whether an issue path is shown by a rendered field (the field itself or a parent composite). */
function shownBy(path: string, mounted: Set<string>): boolean {
  const parts = path.split('.')
  while (parts.length) {
    if (mounted.has(parts.join('.'))) return true
    parts.pop()
  }
  return false
}

/**
 * Client-side validation issues: renders their errors on the fields (the
 * form validates on touch, so a problem on an untouched field would
 * otherwise stay invisible) and returns the messages of the issues no
 * rendered field shows, for the form to say in words.
 */
function surfaceIssues<V extends FieldValues>(form: UseFormReturn<V>, issues: { path: PropertyKey[]; message: string }[]): string[] {
  const paths = [...new Set(issues.map((issue) => issue.path.map(String).join('.')).filter(Boolean))]
  if (paths.length) void form.trigger(paths as Path<V>[])
  const mounted = mountedFields(form)
  if (!mounted) return []
  return [...new Set(issues.filter((issue) => !shownBy(issue.path.map(String).join('.'), mounted)).map((issue) => issue.message))]
}

function leafNames(value: unknown, prefix = ''): string[] {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    const own = prefix ? [prefix] : []
    return [...own, ...Object.entries(value as Record<string, unknown>).flatMap(([k, v]) => leafNames(v, prefix ? `${prefix}.${k}` : k))]
  }
  if (Array.isArray(value)) {
    return [prefix, ...value.flatMap((v, i) => leafNames(v, `${prefix}.${i}`))]
  }
  return prefix ? [prefix] : []
}

/** Leaf paths of client-side (resolver) errors. */
function errorPaths(errors: unknown, prefix = ''): string[] {
  if (!errors || typeof errors !== 'object') return []
  const node = errors as { message?: unknown; type?: unknown; ref?: unknown }
  if (typeof node.message === 'string' && 'type' in node) return node.type === 'server' ? [] : [prefix]
  return Object.entries(errors as Record<string, unknown>).flatMap(([k, v]) => (k === 'ref' || k === 'root' ? [] : errorPaths(v, prefix ? `${prefix}.${k}` : k)))
}

/** Maps a server field path ("wlan.radius.auth[0].host") onto the closest form field. */
export function resolveFieldPath(path: string, scope: string, known: Set<string>, map: Record<string, string> = {}): string | null {
  if (map[path]) return map[path]
  let candidate = path.replace(/\[(\d+)\]/g, '.$1').replace(/^\//, '').replace(/\//g, '.')
  if (candidate.startsWith(`${scope}.`)) candidate = candidate.slice(scope.length + 1)
  const parts = candidate.split('.')
  while (parts.length) {
    const name = parts.join('.')
    if (known.has(name)) return name
    parts.pop()
  }
  return null
}

/** Option labels of experimental values among the changed paths. */
export function experimentalChanges(scope: string, base: unknown, values: unknown): string[] {
  const labels = new Set<string>()
  for (const path of changedPaths(base, values)) {
    const parts = path.split('.')
    while (parts.length) {
      const key = `${scope}.${parts.join('.')}`
      if (hasOption(key)) {
        const meta = getOption(key)
        const value = parts.reduce<unknown>((n, p) => (n as Record<string, unknown> | undefined)?.[p], values)
        if (isExperimentalValue(meta, value)) labels.add(meta.label)
        break
      }
      parts.pop()
    }
  }
  return [...labels]
}

export function useConfigForm<V extends FieldValues, TObject>(options: UseConfigFormOptions<V, TObject>): ConfigFormController<V, TObject> {
  const {
    formKey,
    scope,
    values,
    schema,
    submit,
    toBody,
    toValues,
    ifMatch,
    readOnly = false,
    confirm,
    onSaved,
    fieldMap,
    dryRun = true,
    successMessage,
    siteId,
  } = options
  const queryClient = useQueryClient()
  const form = useForm<V>({
    defaultValues: values as DefaultValues<V>,
    resolver: zodResolver(schema as never) as unknown as Resolver<V>,
    mode: 'onTouched',
    reValidateMode: 'onChange',
  })
  const [base, setBase] = useState<V>(values)
  const current = useWatch({ control: form.control }) as V
  const changed = useMemo(() => changedPaths(base, current), [base, current])
  const dirty = changed.length > 0

  // The revision the form's base came from: If-Match uses it, so a save over
  // someone else's change answers 409 stale (Reload or Overwrite).
  const [baseRev, setBaseRev] = useState<number | undefined>(ifMatch)

  // Server values changed (SSE invalidation, another tab): adopt them unless
  // the operator is editing; then the save meets the conflict instead.
  const valuesRef = useRef(values)
  useEffect(() => {
    if (deepEqual(valuesRef.current, values) && baseRev === ifMatch) return
    if (dirty) return
    valuesRef.current = values
    setBase(values)
    setBaseRev(ifMatch)
    form.reset(values)
    // Only react to new server values.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [values, ifMatch, dirty])

  useDirtyRegistration(formKey, dirty && !readOnly)
  useDraft(formKey, () => form.getValues(), dirty && !readOnly)

  // Peek (not take) in the initializer: StrictMode runs it twice in development.
  const [draft, setDraft] = useState<V | null>(() => peekDraft<V>(formKey))
  const resolveDraft = useCallback(
    (restore: boolean) => {
      takeDraft(formKey)
      if (restore && draft) form.reset(draft, { keepDefaultValues: true })
      setDraft(null)
    },
    [draft, form, formKey],
  )

  const [formError, setFormError] = useState<string[]>([])
  const [saving, setSaving] = useState(false)
  const [lastSaved, setLastSaved] = useState<MutationResponse<TObject> | null>(null)
  const [stale, setStale] = useState<{ current: unknown } | null>(null)
  const [ack, setAck] = useState<{ labels: string[] } | null>(null)
  const [confirming, setConfirming] = useState<ConfirmRequest | null>(null)
  const pending = useRef<{ values: V; ifMatch?: number } | null>(null)
  const serverErrors = useRef<Set<string>>(new Set())

  const knownFields = useMemo(() => new Set(leafNames(current)), [current])

  const mapErrors = useCallback(
    (error: unknown): string[] => {
      if (!isApiError(error)) return [error instanceof Error ? error.message : 'Something went wrong.']
      const unmatched: string[] = []
      let first = true
      for (const [path, message] of Object.entries(error.fields)) {
        const name = resolveFieldPath(path, scope, knownFields, fieldMap)
        if (name) {
          serverErrors.current.add(name)
          form.setError(name as Path<V>, { type: 'server', message }, { shouldFocus: first })
          first = false
        } else unmatched.push(message)
      }
      return first ? [error.message, ...unmatched.filter((m) => m !== error.message)] : unmatched
    },
    [fieldMap, form, knownFields, scope],
  )

  const bodyFor = useCallback(
    (vals: V) => {
      const p = (mergePatch(base, vals) ?? {}) as Record<string, unknown>
      return toBody ? toBody(p, vals) : p
    },
    [base, toBody],
  )

  // Cross-field rules (passphrase by security mode, band steering by bands…):
  // when anything changes, re-check the fields that currently show a client
  // error, so an error caused by another field clears as soon as it is fixed.
  const valuesKey = JSON.stringify(current)
  useEffect(() => {
    const names = errorPaths(form.formState.errors)
    if (names.length) void form.trigger(names as Path<V>[])
    // valuesKey stands for every change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [valuesKey])

  // ---- dry run ----
  const [impact, setImpact] = useState<ImpactState<TObject>>({ status: 'idle' })
  const bodyKey = JSON.stringify(dirty ? bodyFor(current) : null)
  useEffect(() => {
    if (!dirty || readOnly || !dryRun) {
      setImpact({ status: 'idle' })
      return
    }
    const parsed = schema.safeParse(current)
    if (!parsed.success) {
      setImpact({ status: 'invalid', messages: surfaceIssues(form, parsed.error.issues) })
      return
    }
    const controller = new AbortController()
    setImpact((prev) => (prev.status === 'ready' ? prev : { status: 'loading' }))
    const handle = window.setTimeout(async () => {
      setImpact({ status: 'loading' })
      try {
        const data = await submit(bodyFor(current), { dryRun: true, ifMatch: baseRev, signal: controller.signal })
        if (controller.signal.aborted) return
        for (const name of serverErrors.current) form.clearErrors(name as Path<V>)
        serverErrors.current.clear()
        setImpact({ status: 'ready', data })
      } catch (error) {
        if (controller.signal.aborted || (isApiError(error) && error.code === 'aborted')) return
        if (isApiError(error) && error.code === 'stale') {
          setImpact({ status: 'error', error })
          return
        }
        if (isApiError(error) && Object.keys(error.fields).length) mapErrors(error)
        setImpact({ status: 'error', error: error as Error })
      }
    }, 600)
    return () => {
      window.clearTimeout(handle)
      controller.abort()
    }
    // bodyKey captures every change that matters.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [bodyKey, readOnly, dryRun, baseRev])

  // ---- save ----
  const perform = useCallback(
    async (vals: V, match: number | undefined) => {
      setSaving(true)
      setFormError([])
      try {
        const response = await submit(bodyFor(vals), { ifMatch: match })
        const next = toValues ? toValues(response.object) : vals
        markClean(formKey)
        valuesRef.current = next
        setBase(next)
        setBaseRev((response.object as { updated_rev?: number } | null)?.updated_rev ?? match)
        form.reset(next)
        setImpact({ status: 'idle' })
        setLastSaved(response)
        const n = response.impact?.aps?.length ?? 0
        toast.success(successMessage?.(response) ?? 'Saved', {
          description: n ? `Revision ${response.revision} · applies to ${n} AP${n === 1 ? '' : 's'}` : 'No AP changes',
        })
        void queryClient.invalidateQueries({ queryKey: siteId ? keys.siteChanges(siteId) : ['site'] })
        onSaved?.(response, next)
      } catch (error) {
        if (isApiError(error) && error.code === 'stale') {
          const body = error.body as { current?: unknown; object?: unknown } | undefined
          pending.current = { values: vals, ifMatch: match }
          setStale({ current: body?.current ?? body?.object ?? null })
        } else if (isApiError(error) && error.code === 'migration_pending') {
          void queryClient.invalidateQueries({ queryKey: keys.system })
          setFormError([error.message || 'Typed settings are paused until the schema-2 migration commits.'])
        } else {
          setFormError(mapErrors(error))
          toast.error('Not saved', { description: isApiError(error) ? error.message : undefined })
        }
      } finally {
        setSaving(false)
      }
    },
    [bodyFor, form, formKey, mapErrors, onSaved, queryClient, siteId, submit, successMessage, toValues],
  )

  const proceed = useCallback(
    async (vals: V, skipAck = false, skipConfirm = false) => {
      if (!skipAck) {
        const labels = experimentalChanges(scope, base, vals)
        if (labels.length && !hasAck(formKey)) {
          pending.current = { values: vals, ifMatch: baseRev }
          setAck({ labels })
          return
        }
      }
      if (!skipConfirm && confirm) {
        const request = confirm(vals, base)
        if (request) {
          pending.current = { values: vals, ifMatch: baseRev }
          setConfirming(request)
          return
        }
      }
      await perform(vals, baseRev)
    },
    [base, baseRev, confirm, formKey, perform, scope],
  )

  const save = useMemo(
    () =>
      form.handleSubmit(
        (vals) => proceed(vals as V),
        (errors) => {
          const parsed = schema.safeParse(form.getValues())
          const hidden = parsed.success ? [] : surfaceIssues(form, parsed.error.issues)
          const count = Object.keys(errors).length
          setFormError(hidden.length && hidden.length >= count ? hidden : [`Check the highlighted field${count === 1 ? '' : 's'}.`, ...hidden])
        },
      ),
    [form, proceed, schema],
  )

  const discard = useCallback(() => {
    form.reset(base)
    setFormError([])
    setImpact({ status: 'idle' })
  }, [base, form])

  const resolveStale = useCallback(
    (choice: 'reload' | 'overwrite' | 'cancel') => {
      const held = pending.current
      setStale(null)
      if (choice === 'reload') {
        pending.current = null
        void queryClient.invalidateQueries()
        if (stale?.current && toValues) {
          const next = toValues(stale.current as TObject)
          valuesRef.current = next
          setBase(next)
          setBaseRev((stale.current as { updated_rev?: number }).updated_rev)
          form.reset(next)
        } else {
          valuesRef.current = values
          setBase(values)
          setBaseRev(ifMatch)
          form.reset(values)
        }
        return
      }
      if (choice === 'overwrite' && held) {
        const rev = (stale?.current as { updated_rev?: number } | null)?.updated_rev
        void perform(held.values, rev ?? undefined)
      }
    },
    [form, ifMatch, perform, queryClient, stale, toValues, values],
  )

  const resolveAck = useCallback(
    (accepted: boolean) => {
      setAck(null)
      const held = pending.current
      if (!accepted || !held) return
      storeAck(formKey)
      void proceed(held.values, true)
    },
    [formKey, proceed],
  )

  const resolveConfirm = useCallback(
    (accepted: boolean) => {
      setConfirming(null)
      const held = pending.current
      if (!accepted || !held) return
      void perform(held.values, held.ifMatch)
    },
    [perform],
  )

  return {
    form,
    formKey,
    base,
    dirty,
    changed,
    impact,
    saving,
    readOnly,
    formError,
    setFormError,
    save,
    discard,
    stale,
    resolveStale,
    ack,
    resolveAck,
    confirming,
    resolveConfirm,
    draft,
    resolveDraft,
    lastSaved,
    siteId,
  }
}
