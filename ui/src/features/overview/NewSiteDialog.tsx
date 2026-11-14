import { useEffect, useId, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { toast } from 'sonner'
import { applyFieldErrors, describeError } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { inputClass } from '@/components/ui/input'
import { cn } from '@/lib/utils'
import { ResponsiveDialog } from './kit/ConfirmDialog'
import { SUPPORTED_COUNTRIES, UNSUPPORTED_COUNTRIES, browserCountry, browserTimezone, timezones, useCreateSite } from './site'

// New site (design §3.2): Name, Country (only countries with a verified
// regulatory table are selectable), Timezone. Creating navigates to the new
// site with replace, so Back does not reopen the dialog.

type Fields = { name: string; country: string; timezone: string }
type Errors = Partial<Record<keyof Fields, string>>

export function validateSite(values: Fields, existing: string[], zones: string[]): Errors {
  const errors: Errors = {}
  const name = values.name.trim()
  if (!name) errors.name = 'Enter a name.'
  else if ([...name].length > 64) errors.name = 'Use 64 characters or fewer.'
  else if (existing.some((other) => other.toLowerCase() === name.toLowerCase())) errors.name = 'Another site already has this name.'
  if (!values.country) errors.country = 'Choose the country the APs are in.'
  else if (!SUPPORTED_COUNTRIES.some((c) => c.code === values.country)) errors.country = 'That country is not supported yet.'
  if (!values.timezone || !zones.includes(values.timezone)) errors.timezone = 'Choose a timezone.'
  return errors
}

function Field({ id, label, help, error, children }: { id: string; label: string; help?: string; error?: string; children: React.ReactNode }) {
  return (
    <div className="grid gap-1.5">
      <label htmlFor={id} className="text-sm font-medium">
        {label}
      </label>
      {children}
      {error ? (
        <p id={`${id}-error`} role="alert" className="text-sm text-destructive">
          {error}
        </p>
      ) : help ? (
        <p id={`${id}-help`} className="text-xs text-muted-foreground">
          {help}
        </p>
      ) : null}
    </div>
  )
}

export function NewSiteDialog({ open, onOpenChange, existing }: { open: boolean; onOpenChange: (open: boolean) => void; existing: string[] }) {
  const navigate = useNavigate()
  const create = useCreateSite()
  const zones = timezones()
  const initial = (): Fields => ({
    name: '',
    country: browserCountry() === 'AU' ? 'AU' : '',
    timezone: zones.includes(browserTimezone()) ? browserTimezone() : 'UTC',
  })
  const [values, setValues] = useState<Fields>(initial)
  const [errors, setErrors] = useState<Errors>({})
  const [formError, setFormError] = useState<string | null>(null)
  const base = useId()
  useEffect(() => {
    if (open) {
      setValues(initial())
      setErrors({})
      setFormError(null)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  const set = (key: keyof Fields) => (event: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => {
    setValues((v) => ({ ...v, [key]: event.target.value }))
    if (errors[key]) setErrors((e) => ({ ...e, [key]: undefined }))
  }

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    const found = validateSite(values, existing, zones)
    setErrors(found)
    setFormError(null)
    if (Object.keys(found).length) {
      document.getElementById(`${base}-${Object.keys(found)[0]}`)?.focus()
      return
    }
    create.mutate(
      { name: values.name.trim(), country: values.country, timezone: values.timezone },
      {
        onSuccess: (site) => {
          toast.success('Site created')
          void navigate({ to: '/sites/$siteId/overview', params: { siteId: site.id }, replace: true })
        },
        onError: (error) => {
          const mapped: Errors = {}
          const rest = applyFieldErrors(
            error,
            ((name: keyof Fields, e: { message: string }) => {
              mapped[name] = e.message
            }) as never,
            ['name', 'country', 'timezone'],
          )
          setErrors(mapped)
          setFormError(rest.length ? rest.join(' ') : Object.keys(mapped).length ? null : describeError(error).message)
        },
      },
    )
  }

  const describedBy = (key: keyof Fields) => (errors[key] ? `${base}-${key}-error` : `${base}-${key}-help`)
  return (
    <ResponsiveDialog open={open} onOpenChange={onOpenChange} title="New site" dismissable={!create.isPending}>
      <form noValidate onSubmit={submit} className="grid gap-4">
        <Field id={`${base}-name`} label="Name" error={errors.name}>
          <input
            id={`${base}-name`}
            className={inputClass}
            value={values.name}
            onChange={set('name')}
            autoCapitalize="words"
            autoComplete="off"
            maxLength={64}
            aria-invalid={errors.name ? true : undefined}
            aria-describedby={errors.name ? `${base}-name-error` : undefined}
          />
        </Field>
        <Field
          id={`${base}-country`}
          label="Country"
          error={errors.country}
          help="Sets the radios’ regulatory domain (channels and power limits). AU uses AU/890."
        >
          <select
            id={`${base}-country`}
            className={cn(inputClass, 'pr-8')}
            value={values.country}
            onChange={set('country')}
            aria-invalid={errors.country ? true : undefined}
            aria-describedby={describedBy('country')}
          >
            <option value="">Choose a country</option>
            {SUPPORTED_COUNTRIES.map((c) => (
              <option key={c.code} value={c.code}>
                {c.name} ({c.code})
              </option>
            ))}
            <optgroup label="Not yet supported">
              {UNSUPPORTED_COUNTRIES.map((c) => (
                <option key={c.code} value={c.code} disabled>
                  {c.name} ({c.code})
                </option>
              ))}
            </optgroup>
          </select>
        </Field>
        <Field id={`${base}-timezone`} label="Timezone" error={errors.timezone} help="Used for schedules and for the overnight channel-planning window.">
          <select
            id={`${base}-timezone`}
            className={cn(inputClass, 'pr-8')}
            value={values.timezone}
            onChange={set('timezone')}
            aria-invalid={errors.timezone ? true : undefined}
            aria-describedby={describedBy('timezone')}
          >
            {zones.map((zone) => (
              <option key={zone} value={zone}>
                {zone.replace(/_/g, ' ')}
              </option>
            ))}
          </select>
        </Field>
        {formError ? (
          <p role="alert" className="rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2 text-sm">
            {formError}
          </p>
        ) : null}
        <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={create.isPending}>
            Cancel
          </Button>
          <Button type="submit" variant="brand" disabled={create.isPending}>
            {create.isPending ? 'Creating…' : 'Create site'}
          </Button>
        </div>
      </form>
    </ResponsiveDialog>
  )
}
