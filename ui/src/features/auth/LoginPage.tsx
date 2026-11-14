import { useEffect, useId, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useRouter } from '@tanstack/react-router'
import { Eye, EyeOff, LoaderCircle } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { isApiError } from '@/api/errors'
import { keys } from '@/api/keys'
import { BrandMark, Wordmark } from '@/features/shell/brand'
import { login } from './api'
import { safeNext } from './next'
import { loginSchema, type LoginValues } from './schema'
import { SpectrumBackdrop } from './SpectrumBackdrop'

// Login (design §3.1): one centred card, the token field at 16 px with a
// show/hide button, a hidden username so password managers save the token,
// inline errors cleared on the next keystroke, and the 429 countdown.

function failureMessage(error: unknown): string {
  if (isApiError(error)) {
    if (error.status === 401) return 'That token is not valid for this controller.'
    if (error.status === 429) {
      return error.retryAfter
        ? `Too many attempts. Try again in ${error.retryAfter} s.`
        : 'Too many attempts. Wait a minute, then try again.'
    }
    if (error.code === 'network') return error.message
  }
  return 'Sign-in failed. Try again.'
}

export function LoginPage({ next }: { next?: string }) {
  const router = useRouter()
  const queryClient = useQueryClient()
  const [reveal, setReveal] = useState(false)
  const submitRef = useRef<HTMLButtonElement>(null)
  const ids = { token: useId(), help: useId(), error: useId() }
  useEffect(() => {
    document.title = 'Sign in · Dismist'
  }, [])

  const form = useForm<LoginValues>({
    resolver: zodResolver(loginSchema),
    mode: 'onSubmit',
    reValidateMode: 'onSubmit',
    defaultValues: { username: 'admin', token: '' },
  })
  const { errors } = form.formState

  const mutation = useMutation({
    mutationFn: (values: LoginValues) => login(values.token.trim()),
    onSuccess: async () => {
      queryClient.removeQueries({ predicate: (query) => query.queryKey[0] !== 'session' })
      queryClient.setQueryData(keys.session, { authenticated: true, expires_at: null })
      await router.navigate({ href: safeNext(next), replace: true })
    },
    onError: (error) => {
      form.setError('token', { type: 'server', message: failureMessage(error) }, { shouldFocus: true })
    },
  })

  const tokenField = form.register('token', {
    onChange: () => {
      if (form.formState.errors.token) form.clearErrors('token')
    },
  })
  const tokenError = errors.token?.message

  return (
    <main id="main" className="relative isolate grid min-h-dvh place-items-center overflow-hidden px-4 pt-safe pb-safe max-md:content-start max-md:pt-[max(6vh,env(safe-area-inset-top))]">
      <SpectrumBackdrop />
      <div className="relative w-full max-w-[22.5rem] py-10 max-md:pb-[24vh]">
        <div className="mb-7 flex items-center gap-2.5">
          <BrandMark className="size-9" />
          <Wordmark className="text-xl" />
        </div>
        <section aria-labelledby="login-title" className="rounded-xl border bg-card p-6 shadow-[0_1px_0_0_var(--border),0_12px_32px_-16px_oklch(0.2_0.03_252/0.25)] sm:p-7">
          <h1 id="login-title" tabIndex={-1} className="text-2xl font-semibold">
            Sign in
          </h1>
          <p className="mt-1.5 text-sm text-muted-foreground">Use this controller’s admin token or an API token.</p>

          <form className="mt-6 grid gap-5" noValidate onSubmit={form.handleSubmit((values) => mutation.mutate(values))}>
            <input
              type="text"
              autoComplete="username"
              tabIndex={-1}
              aria-hidden
              readOnly
              className="sr-only"
              {...form.register('username')}
            />
            <div className="grid gap-2">
              <Label htmlFor={ids.token}>Token</Label>
              <div className="relative">
                <Input
                  id={ids.token}
                  type={reveal ? 'text' : 'password'}
                  autoComplete="current-password"
                  autoCapitalize="none"
                  autoCorrect="off"
                  spellCheck={false}
                  enterKeyHint="go"
                  className="pr-12 font-mono tracking-tight"
                  aria-invalid={tokenError ? true : undefined}
                  aria-describedby={tokenError ? ids.error : ids.help}
                  onFocus={() => window.setTimeout(() => submitRef.current?.scrollIntoView({ block: 'nearest' }), 300)}
                  {...tokenField}
                />
                <Button
                  variant="ghost"
                  size="icon"
                  className="absolute top-0 right-0 text-muted-foreground hover:bg-transparent hover:text-foreground"
                  aria-label={reveal ? 'Hide token' : 'Show token'}
                  aria-pressed={reveal}
                  onClick={() => setReveal((value) => !value)}
                >
                  {reveal ? <EyeOff /> : <Eye />}
                </Button>
              </div>
              {tokenError ? (
                <p id={ids.error} role="alert" className="text-sm text-destructive">
                  {tokenError}
                </p>
              ) : (
                <p id={ids.help} className="text-sm text-muted-foreground">
                  64 hex characters, or a token that starts with <span className="font-mono">dsm_</span>.
                </p>
              )}
            </div>

            <Button ref={submitRef} type="submit" size="lg" className="w-full" disabled={mutation.isPending}>
              {mutation.isPending ? <LoaderCircle className="animate-spin" /> : null}
              {mutation.isPending ? 'Signing in…' : 'Sign in'}
            </Button>
          </form>

          <details className="group mt-5 border-t pt-4 text-sm">
            <summary className="flex min-h-11 cursor-pointer list-none items-center gap-2 font-medium text-foreground marker:hidden fine:md:min-h-8 [&::-webkit-details-marker]:hidden">
              <span aria-hidden className="inline-block transition-transform group-open:rotate-90">›</span>
              Where is my token?
            </summary>
            <div className="mt-2 grid gap-3 text-muted-foreground">
              <p>
                Local install: the file <code className="font-mono text-foreground">data/admin.token</code> next to
                the controller database.
              </p>
              <div>
                <p>Kubernetes:</p>
                <pre className="mt-1.5 overflow-x-auto rounded-md bg-muted px-3 py-2 font-mono text-xs leading-relaxed whitespace-pre-wrap break-all text-foreground">
                  kubectl -n dismist get secret &lt;privateSecret.name&gt; -o jsonpath=&apos;{'{'}.data.admin\.token{'}'}&apos; | base64 -d
                </pre>
              </div>
            </div>
          </details>
        </section>
      </div>
    </main>
  )
}
