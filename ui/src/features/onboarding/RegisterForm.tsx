import { useEffect, useId, useMemo, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery } from '@tanstack/react-query'
import { Link, useBlocker, useNavigate } from '@tanstack/react-router'
import { ChevronDown, CircleAlert, Eye, EyeOff, LoaderCircle, Upload } from 'lucide-react'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input, inputClass } from '@/components/ui/input'
import { Mono } from '@/components/app/identifiers'
import { describeError, isApiError } from '@/api/errors'
import { allAPsQuery, onboardingQuery, pendingQuery, useRegisterAP } from '@/api/queries/onboarding'
import type { SiteSummary } from '@/gen/api.gen'
import { normaliseMac } from '@/lib/mac'
import { cn } from '@/lib/utils'
import { FieldRow, describedBy } from './Panel'
import { SECRET_BYTES, decodeSecret, formField, registerSchema, toAPInput, toHex, type RegisterValues } from './schema'

// The Register form (design §3.15). After Register the page replaces itself
// with ?step=verify&id=…, so a refresh or a shared link lands on Verify.

export type RegisterDefaults = { id?: string; site?: string; name?: string }

function suggestedName(id: string | undefined): string {
  const mac = id ? normaliseMac(id) : null
  return mac ? `AP ${mac.slice(-5)}` : ''
}

export function RegisterForm({ sites, defaults }: { sites: SiteSummary[]; defaults: RegisterDefaults }) {
  const navigate = useNavigate()
  const view = useQuery(onboardingQuery)
  const registered = useQuery(allAPsQuery)
  const pending = useQuery(pendingQuery(false))
  const dnsMode = view.data?.dns_mode ?? false
  const registeredIds = useMemo(() => new Set((registered.data ?? []).map((ap) => ap.id)), [registered.data])
  const schema = useMemo(() => registerSchema({ dnsMode, registered: registeredIds }), [dnsMode, registeredIds])

  const ids = { id: useId(), name: useId(), site: useId(), secret: useId(), pin: useId(), file: useId() }
  const [reveal, setReveal] = useState(false)
  const [loaded, setLoaded] = useState<string | null>(null)
  const [fileError, setFileError] = useState<string | null>(null)

  const initialSite = sites.find((s) => s.id === defaults.site)?.id ?? (sites.length === 1 ? sites[0].id : '')
  const form = useForm<RegisterValues>({
    resolver: zodResolver(schema),
    mode: 'onTouched',
    defaultValues: {
      id: defaults.id ?? '',
      name: defaults.name ?? suggestedName(defaults.id),
      site_id: initialSite,
      secret: '',
      pinned_ip: '',
    },
  })
  const { errors } = form.formState
  const idValue = useWatch({ control: form.control, name: 'id' })
  const preview = normaliseMac(idValue ?? '')

  // In DNS mode the pin is the address the controller sees: pre-fill it
  // from the pending row's public address (design §3.15).
  useEffect(() => {
    if (!dnsMode || form.getValues('pinned_ip')) return
    const row = pending.data?.find((p) => p.id === preview)
    if (row?.remote_address) form.setValue('pinned_ip', row.remote_address, { shouldDirty: false })
  }, [dnsMode, pending.data, preview, form])

  // Read formState lazily: a successful submit resets it before navigating.
  useBlocker({
    shouldBlockFn: () => form.formState.isDirty && !window.confirm('Discard this registration?'),
    enableBeforeUnload: () => form.formState.isDirty,
  })

  const mutation = useRegisterAP()
  const onSubmit = form.handleSubmit((values) => {
    const input = toAPInput(values)
    mutation.mutate(input, {
      onSuccess: () => {
        form.reset(values)
        void navigate({ to: '/onboard/register', search: { step: 'verify', id: input.id, site: input.site_id }, replace: true })
      },
      onError: (error) => {
        if (isApiError(error)) {
          let mapped = false
          for (const [field, message] of Object.entries(error.fields)) {
            const name = formField(field)
            if (name) {
              form.setError(name, { type: 'server', message }, { shouldFocus: !mapped })
              mapped = true
            }
          }
          if (!mapped && error.status === 409) {
            form.setError('id', { type: 'server', message: 'An AP with this ID is already registered.' }, { shouldFocus: true })
            mapped = true
          }
          if (mapped) return
        }
        window.setTimeout(() => document.getElementById('register-error')?.focus(), 0)
      },
    })
  })

  const onFile = async (file: File | undefined) => {
    setFileError(null)
    setLoaded(null)
    if (!file) return
    const bytes = new Uint8Array(await file.arrayBuffer())
    if (bytes.length !== SECRET_BYTES) {
      setFileError(`${file.name} is ${bytes.length} bytes; the secret is exactly ${SECRET_BYTES}. Use the 32 bytes at offset 0x400 of the EEPROM.`)
      return
    }
    form.setValue('secret', toHex(bytes), { shouldDirty: true, shouldValidate: true, shouldTouch: true })
    setLoaded(file.name)
  }

  const secretRegistration = form.register('secret', { onChange: () => setLoaded(null) })
  const secretBytes = decodeSecret(useWatch({ control: form.control, name: 'secret' }) ?? '')?.length
  const help = {
    id: preview ? (
      <>
        Saved as <Mono>{preview}</Mono>. The AP’s base MAC (eth0).
      </>
    ) : (
      'The AP’s base MAC (eth0), in any common spelling.'
    ),
    secret: loaded
      ? `${loaded} loaded (${SECRET_BYTES} bytes).`
      : secretBytes
        ? `${secretBytes} bytes.`
        : 'Read from the AP’s EEPROM at offset 0x400. The CLI does this for you.',
    pin: dnsMode
      ? 'Required in DNS mode: the address the controller sees the AP’s DNS and WSS traffic come from.'
      : 'Leave empty: APs are identified by their ID. Only DNS mode needs a pin.',
  }
  const mutationError = mutation.isError && !Object.keys(errors).length ? describeError(mutation.error) : null

  return (
    <form className="grid gap-5" noValidate onSubmit={onSubmit} aria-describedby={mutationError ? 'register-error' : undefined}>
      {mutationError ? (
        <Alert variant="destructive" tabIndex={-1} id="register-error">
          <CircleAlert />
          <AlertTitle>The AP was not registered</AlertTitle>
          <AlertDescription>
            {mutationError.message}
            {mutationError.code ? <span className="block text-xs">Code: {mutationError.code}</span> : null}
          </AlertDescription>
        </Alert>
      ) : null}

      <FieldRow id={ids.id} label="AP ID" help={help.id} error={errors.id?.message}>
        <Input
          id={ids.id}
          autoCapitalize="none"
          autoCorrect="off"
          spellCheck={false}
          autoComplete="off"
          placeholder="aa:bb:cc:dd:ee:ff"
          className="font-mono"
          aria-invalid={errors.id ? true : undefined}
          aria-describedby={describedBy(ids.id, errors.id?.message, help.id)}
          {...form.register('id')}
        />
      </FieldRow>

      <FieldRow id={ids.name} label="Name" error={errors.name?.message}>
        <Input
          id={ids.name}
          maxLength={64}
          autoComplete="off"
          aria-invalid={errors.name ? true : undefined}
          aria-describedby={describedBy(ids.name, errors.name?.message, null)}
          {...form.register('name')}
        />
      </FieldRow>

      <FieldRow id={ids.site} label="Site" error={errors.site_id?.message} help={sites.length ? undefined : 'Create a site first (Sites › New site).'}>
        <div className="relative">
          <select
            id={ids.site}
            className={cn(inputClass, 'appearance-none pr-10')}
            aria-invalid={errors.site_id ? true : undefined}
            aria-describedby={describedBy(ids.site, errors.site_id?.message, sites.length ? null : 'help')}
            {...form.register('site_id')}
          >
            <option value="">Choose a site</option>
            {sites.map((site) => (
              <option key={site.id} value={site.id}>
                {site.name}
              </option>
            ))}
          </select>
          <ChevronDown aria-hidden className="pointer-events-none absolute top-1/2 right-3 size-4 -translate-y-1/2 text-muted-foreground" />
        </div>
      </FieldRow>

      <FieldRow id={ids.secret} label="Secret" help={help.secret} error={errors.secret?.message ?? fileError ?? undefined}>
        <div className="flex flex-wrap gap-2">
          <div className="relative min-w-0 flex-1 basis-60">
            <Input
              id={ids.secret}
              type={reveal ? 'text' : 'password'}
              autoComplete="off"
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
              placeholder="64 hex characters or base64"
              className="pr-12 font-mono"
              aria-invalid={errors.secret || fileError ? true : undefined}
              aria-describedby={describedBy(ids.secret, errors.secret?.message ?? fileError ?? undefined, help.secret)}
              {...secretRegistration}
            />
            <Button
              variant="ghost"
              size="icon"
              className="absolute top-0 right-0 text-muted-foreground hover:bg-transparent hover:text-foreground"
              aria-label={reveal ? 'Hide secret' : 'Show secret'}
              aria-pressed={reveal}
              onClick={() => setReveal((value) => !value)}
            >
              {reveal ? <EyeOff /> : <Eye />}
            </Button>
          </div>
          <input
            id={ids.file}
            type="file"
            className="sr-only"
            tabIndex={-1}
            aria-hidden
            onChange={(event) => {
              void onFile(event.target.files?.[0])
              event.target.value = ''
            }}
          />
          <Button variant="outline" onClick={() => document.getElementById(ids.file)?.click()}>
            <Upload /> Upload secret.bin
          </Button>
        </div>
      </FieldRow>

      <FieldRow id={ids.pin} label="Pinned source IP" optional={!dnsMode} help={help.pin} error={errors.pinned_ip?.message}>
        <Input
          id={ids.pin}
          inputMode="decimal"
          autoCapitalize="none"
          autoCorrect="off"
          spellCheck={false}
          autoComplete="off"
          placeholder={dnsMode ? '198.51.100.7' : 'Any address'}
          className="font-mono"
          aria-invalid={errors.pinned_ip ? true : undefined}
          aria-describedby={describedBy(ids.pin, errors.pinned_ip?.message, help.pin)}
          {...form.register('pinned_ip')}
        />
      </FieldRow>

      <p className="text-sm text-muted-foreground">
        There is no model field: the AP reports its model and firmware when it connects. The secret is stored in the controller database
        and never shown again.
      </p>

      <div className="flex flex-wrap items-center gap-3">
        <Button type="submit" size="lg" disabled={mutation.isPending}>
          {mutation.isPending ? <LoaderCircle className="animate-spin" /> : null}
          {mutation.isPending ? 'Registering…' : 'Register'}
        </Button>
        <Button asChild variant="ghost" size="lg">
          <Link to="/onboard" search={{ site: defaults.site }}>
            Cancel
          </Link>
        </Button>
      </div>
    </form>
  )
}
