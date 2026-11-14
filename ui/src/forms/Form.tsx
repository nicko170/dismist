import { useEffect, useRef, type ReactNode } from 'react'
import { FormProvider, useForm, type FieldErrors, type FieldValues, type SubmitHandler, type UseFormProps, type UseFormReturn } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import type { z } from 'zod'
import { CircleAlert } from 'lucide-react'
import { cn } from '@/lib/utils'

// The form frame (design §7.4): react-hook-form with zod, validating on
// touch and re-validating on change; a failed submit moves focus to an error
// summary that links to each field (§7.8); server errors that map to no
// field go into one form-level alert, never a global banner.

/** useForm with the zod resolver and the design's validation modes. */
export function useAppForm<S extends z.ZodType<FieldValues, FieldValues>>(
  schema: S,
  props: Omit<UseFormProps<z.input<S>, unknown, z.output<S>>, 'resolver'> = {},
): UseFormReturn<z.input<S>, unknown, z.output<S>> {
  return useForm<z.input<S>, unknown, z.output<S>>({
    // The resolver's generics cannot follow S through useForm's; the schema is the same object.
    resolver: zodResolver(schema as never) as never,
    mode: 'onTouched',
    reValidateMode: 'onChange',
    shouldFocusError: false,
    ...props,
  })
}

/** Flattens RHF errors to [path, message] in document order. */
export function flattenErrors(errors: FieldErrors, prefix = ''): [string, string][] {
  const out: [string, string][] = []
  for (const [key, value] of Object.entries(errors ?? {})) {
    if (!value || key === 'ref') continue
    const path = prefix ? `${prefix}.${key}` : key
    const message = (value as { message?: unknown }).message
    if (typeof message === 'string' && message) out.push([path, message])
    else if (typeof value === 'object') out.push(...flattenErrors(value as FieldErrors, path))
  }
  return out
}

/** Every leaf path of a values object ("radius.auth.0.host"), for mapping server field errors. */
export function fieldPaths(values: unknown, prefix = ''): string[] {
  if (values === null || typeof values !== 'object') return prefix ? [prefix] : []
  const out: string[] = prefix ? [prefix] : []
  for (const [key, value] of Object.entries(values as Record<string, unknown>)) {
    out.push(...fieldPaths(value, prefix ? `${prefix}.${key}` : key))
  }
  return out
}

/** Counts changed fields from formState.dirtyFields (a changed list counts once). */
export function countDirty(dirty: unknown): number {
  if (dirty === true) return 1
  if (Array.isArray(dirty)) return dirty.some((item) => countDirty(item) > 0) ? 1 : 0
  if (dirty && typeof dirty === 'object') return Object.values(dirty).reduce<number>((sum, value) => sum + countDirty(value), 0)
  return 0
}

/** Server errors that matched no field (design §7.4 "Errors"); focused when it appears. */
export function FormAlert({ messages, title = 'Not saved', className }: { messages: readonly string[]; title?: string; className?: string }) {
  const ref = useRef<HTMLDivElement>(null)
  const key = messages.join('\n')
  useEffect(() => {
    if (key) ref.current?.focus()
  }, [key])
  if (!messages.length) return null
  return (
    <div ref={ref} tabIndex={-1} role="alert" className={cn('flex gap-3 rounded-lg border border-destructive/35 bg-destructive/6 px-4 py-3 outline-none focus-visible:outline-2 focus-visible:outline-ring', className)}>
      <CircleAlert aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
      <div className="grid min-w-0 gap-1">
        <p className="text-sm font-medium">{title}</p>
        {messages.length === 1 ? (
          <p className="text-sm break-words text-muted-foreground">{messages[0]}</p>
        ) : (
          <ul className="list-disc pl-4 text-sm text-muted-foreground">
            {messages.map((message) => (
              <li key={message} className="break-words">
                {message}
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}

/** After a failed submit: "Fix 2 fields" with a link per field; takes focus. */
function ErrorSummary({ form, labels }: { form: UseFormReturn<FieldValues>; labels?: Record<string, string> }) {
  const ref = useRef<HTMLDivElement>(null)
  const { errors, submitCount, isSubmitSuccessful } = form.formState
  const list = flattenErrors(errors).filter(([, message]) => message)
  const show = submitCount > 0 && !isSubmitSuccessful && list.length > 0
  useEffect(() => {
    if (show) ref.current?.focus()
    // Focus once per failed submit, not on every keystroke that changes the list.
    // oxlint-disable-next-line react-hooks/exhaustive-deps
  }, [submitCount, show])
  if (!show) return null
  return (
    <div ref={ref} tabIndex={-1} role="alert" aria-labelledby="form-error-summary" className="grid gap-2 rounded-lg border border-destructive/35 bg-destructive/6 px-4 py-3 outline-none focus-visible:outline-2 focus-visible:outline-ring">
      <p id="form-error-summary" className="flex items-center gap-2 text-sm font-medium">
        <CircleAlert aria-hidden className="size-4 text-destructive" />
        {list.length === 1 ? 'Fix 1 field before saving' : `Fix ${list.length} fields before saving`}
      </p>
      <ul className="grid gap-0.5 pl-6 text-sm">
        {list.map(([path, message]) => (
          <li key={path}>
            <button
              type="button"
              className="min-h-11 cursor-pointer text-left text-brand underline-offset-4 hover:underline fine:md:min-h-0"
              onClick={() => {
                form.setFocus(path as never)
                document.querySelector<HTMLElement>(`[name="${CSS.escape(path)}"]`)?.scrollIntoView({ block: 'center' })
              }}
            >
              {labels?.[path] ? `${labels[path]}: ` : null}
              {message}
            </button>
          </li>
        ))}
      </ul>
    </div>
  )
}

export function AppForm<TIn extends FieldValues, TOut extends FieldValues>({
  form,
  onSubmit,
  id,
  alert,
  labels,
  className,
  children,
}: {
  form: UseFormReturn<TIn, unknown, TOut>
  onSubmit: SubmitHandler<TOut>
  id?: string
  /** Form-level messages (from useFormSubmit). */
  alert?: readonly string[]
  /** Field labels for the error summary, by path. */
  labels?: Record<string, string>
  className?: string
  children: ReactNode
}) {
  return (
    <FormProvider {...form}>
      <form id={id} noValidate onSubmit={form.handleSubmit(onSubmit)} className={cn('grid gap-4', className)}>
        <ErrorSummary form={form as unknown as UseFormReturn<FieldValues>} labels={labels} />
        {alert?.length ? <FormAlert messages={alert} /> : null}
        {children}
      </form>
    </FormProvider>
  )
}
