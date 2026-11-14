import { useRef, useState, type ComponentProps, type ReactNode } from 'react'
import { useController } from 'react-hook-form'
import { Eye, EyeOff, FileUp, Sparkles } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input, Textarea, inputClass } from '@/components/ui/input'
import { utf8Length } from '@/lib/format'
import { cn } from '@/lib/utils'
import { errorText, type BoundField } from './common'
import { FieldShell, controlProps } from './Field'
import { useInherit } from './inherit'
import { fileToBase64, generatePassphrase } from './secret'

// Text controls bound to react-hook-form (design §7.4). All render at 16 px
// on phones and touch screens through the Input primitive.

type InputExtras = Pick<ComponentProps<'input'>, 'placeholder' | 'inputMode' | 'autoComplete' | 'maxLength' | 'enterKeyHint'>

/** Identifiers (MACs, IPs, hostnames, IDs): no capitalisation or correction (§7.7 rule 6). */
export const identifierProps = { autoCapitalize: 'none', autoCorrect: 'off', spellCheck: false } as const

export function TextField({
  name,
  disabled,
  markDirty = true,
  inherit,
  identifier = false,
  mono = false,
  suffix,
  autoCapitalize,
  ...props
}: BoundField &
  InputExtras & {
    /** MACs, IPs, hostnames: no autocapitalise or autocorrect. */
    identifier?: boolean
    mono?: boolean
    /** Trailing text inside the field ("dBm", "s"). */
    suffix?: ReactNode
    autoCapitalize?: string
    /** Text under the control, right-aligned (a counter). */
    counter?: ReactNode
  }) {
  const { field, fieldState } = useController({ name, disabled })
  const { slot, locked } = useInherit(name, inherit, disabled)
  const { placeholder, inputMode, autoComplete, maxLength, enterKeyHint, counter, ...chrome } = props
  return (
    <FieldShell {...chrome} dirty={markDirty && fieldState.isDirty} error={errorText(fieldState.error)} inherit={slot}>
      {(ids) => (
        <div className="grid gap-1">
          <div className="relative">
            <Input
              {...controlProps(ids)}
              {...(identifier ? identifierProps : {})}
              autoCapitalize={autoCapitalize ?? (identifier ? 'none' : undefined)}
              name={field.name}
              ref={field.ref}
              value={locked ? '' : ((field.value as string | null | undefined) ?? '')}
              placeholder={locked ? String(inherit?.display ?? inherit?.value ?? '') : placeholder}
              onChange={(event) => field.onChange(event.target.value)}
              onBlur={field.onBlur}
              disabled={field.disabled || locked}
              inputMode={inputMode}
              autoComplete={autoComplete}
              maxLength={maxLength}
              enterKeyHint={enterKeyHint}
              className={cn(mono && 'font-mono', suffix ? 'pr-14' : undefined)}
            />
            {suffix ? (
              <span aria-hidden className="pointer-events-none absolute inset-y-0 right-3 flex items-center text-sm text-muted-foreground">
                {suffix}
              </span>
            ) : null}
          </div>
          {counter ? <div className="text-right text-xs text-muted-foreground tabular-nums">{counter}</div> : null}
        </div>
      )}
    </FieldShell>
  )
}

/** Network names: the counter is in UTF-8 bytes (32 max), because the firmware counts bytes. */
export function SSIDField(props: BoundField & InputExtras) {
  const { field } = useController({ name: props.name })
  const bytes = utf8Length(String(field.value ?? ''))
  return (
    <TextField
      autoComplete="off"
      autoCapitalize="none"
      enterKeyHint="next"
      {...props}
      counter={
        <span className={cn(bytes > 32 && 'font-medium text-destructive')}>
          {bytes}/32 bytes
          {bytes !== String(field.value ?? '').length ? <span className="sr-only"> (some characters use more than one byte)</span> : null}
        </span>
      }
    />
  )
}

export function TextareaField({
  name,
  disabled,
  markDirty = true,
  inherit,
  identifier = false,
  rows = 4,
  placeholder,
  mono = false,
  ...chrome
}: BoundField & { identifier?: boolean; rows?: number; placeholder?: string; mono?: boolean }) {
  const { field, fieldState } = useController({ name, disabled })
  const { slot, locked } = useInherit(name, inherit, disabled)
  return (
    <FieldShell {...chrome} dirty={markDirty && fieldState.isDirty} error={errorText(fieldState.error)} inherit={slot}>
      {(ids) => (
        <Textarea
          {...controlProps(ids)}
          {...(identifier ? identifierProps : {})}
          name={field.name}
          ref={field.ref}
          rows={rows}
          value={locked ? '' : ((field.value as string | null | undefined) ?? '')}
          placeholder={placeholder}
          onChange={(event) => field.onChange(event.target.value)}
          onBlur={field.onBlur}
          disabled={field.disabled || locked}
          className={cn(mono && 'font-mono text-[0.9em]')}
        />
      )}
    </FieldShell>
  )
}

/**
 * Passphrases (design §7.4 "Secrets"): write-only. With `stored` (the
 * view's has_psk) an edit form shows "Change passphrase" first; an empty
 * value means keep. Show/hide, Generate, a length counter, and
 * autocomplete="new-password" so browsers offer to save, not to fill.
 */
export function PasswordField({
  name,
  disabled,
  markDirty = true,
  stored = false,
  generate = true,
  minLength = 8,
  maxLength = 63,
  changeLabel = 'Change passphrase',
  ...chrome
}: Omit<BoundField, 'inherit'> & {
  /** A value is already stored (has_psk / has_secret). */
  stored?: boolean
  generate?: boolean
  minLength?: number
  maxLength?: number
  changeLabel?: string
}) {
  const { field, fieldState } = useController({ name, disabled })
  const [changing, setChanging] = useState(!stored)
  const [visible, setVisible] = useState(false)
  const input = useRef<HTMLInputElement | null>(null)
  const value = String(field.value ?? '')

  if (!changing) {
    return (
      <FieldShell {...chrome} labelFor={false} dirty={false} error={errorText(fieldState.error)}>
        <div className="flex flex-wrap items-center gap-3">
          <span className="text-sm text-muted-foreground">Set, not shown</span>
          <Button
            variant="outline"
            size="sm"
            disabled={field.disabled}
            onClick={() => {
              setChanging(true)
              requestAnimationFrame(() => input.current?.focus())
            }}
          >
            {changeLabel}
          </Button>
        </div>
      </FieldShell>
    )
  }

  return (
    <FieldShell {...chrome} dirty={markDirty && fieldState.isDirty} error={errorText(fieldState.error)}>
      {(ids) => (
        <div className="grid gap-1.5">
          <div className="flex gap-2">
            <div className="relative min-w-0 flex-1">
              <input
                {...controlProps(ids)}
                {...identifierProps}
                ref={(element) => {
                  input.current = element
                  field.ref(element)
                }}
                name={field.name}
                type={visible ? 'text' : 'password'}
                autoComplete="new-password"
                value={value}
                onChange={(event) => field.onChange(event.target.value)}
                onBlur={field.onBlur}
                disabled={field.disabled}
                placeholder={stored ? 'Leave empty to keep the current one' : undefined}
                className={cn(inputClass, 'pr-12 font-mono')}
              />
              <button
                type="button"
                aria-label={visible ? 'Hide passphrase' : 'Show passphrase'}
                aria-pressed={visible}
                onClick={() => setVisible((v) => !v)}
                className="absolute top-1/2 right-0 flex size-11 -translate-y-1/2 cursor-pointer items-center justify-center rounded-md text-muted-foreground hover:text-foreground fine:md:size-9"
              >
                {visible ? <EyeOff aria-hidden className="size-4" /> : <Eye aria-hidden className="size-4" />}
              </button>
            </div>
            {generate ? (
              <Button
                variant="outline"
                disabled={field.disabled}
                onClick={() => {
                  field.onChange(generatePassphrase(20))
                  setVisible(true)
                }}
              >
                <Sparkles aria-hidden /> Generate
              </Button>
            ) : null}
          </div>
          <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
            <span className="tabular-nums">
              {value.length} characters ({minLength} to {maxLength})
            </span>
            {stored ? (
              <button
                type="button"
                className="min-h-11 cursor-pointer text-brand underline-offset-4 hover:underline fine:md:min-h-0"
                onClick={() => {
                  field.onChange('')
                  setChanging(false)
                  setVisible(false)
                }}
              >
                Keep the current one
              </button>
            ) : null}
          </div>
        </div>
      )}
    </FieldShell>
  )
}

/**
 * The AP's 32-byte secret (design §3.6): paste hex or base64, or choose
 * secret.bin (read in the browser, sent as base64). Never displayed back.
 */
export function SecretField({
  name,
  disabled,
  stored = false,
  ...chrome
}: Omit<BoundField, 'inherit'> & { stored?: boolean }) {
  const { field, fieldState } = useController({ name, disabled })
  const [fileName, setFileName] = useState<string | null>(null)
  const [readError, setReadError] = useState<string | null>(null)
  const file = useRef<HTMLInputElement>(null)
  return (
    <FieldShell {...chrome} dirty={fieldState.isDirty} error={readError ?? errorText(fieldState.error)}>
      {(ids) => (
        <div className="grid gap-2">
          <input
            {...controlProps(ids)}
            {...identifierProps}
            ref={field.ref}
            name={field.name}
            type="password"
            autoComplete="new-password"
            value={fileName ? '' : String(field.value ?? '')}
            placeholder={fileName ? `From ${fileName}` : stored ? 'A secret is stored; paste a new one to replace it' : 'Paste hex or base64'}
            onChange={(event) => {
              setFileName(null)
              setReadError(null)
              field.onChange(event.target.value)
            }}
            onBlur={field.onBlur}
            disabled={field.disabled}
            className={cn(inputClass, 'font-mono')}
          />
          <div className="flex flex-wrap items-center gap-2">
            <Button variant="outline" size="sm" disabled={field.disabled} onClick={() => file.current?.click()}>
              <FileUp aria-hidden /> Choose secret.bin
            </Button>
            {fileName ? <span className="text-sm text-muted-foreground">Read {fileName}</span> : null}
            <input
              ref={file}
              type="file"
              tabIndex={-1}
              aria-hidden
              className="sr-only"
              onChange={async (event) => {
                const chosen = event.target.files?.[0]
                event.target.value = ''
                if (!chosen) return
                if (chosen.size > 4096) {
                  setReadError('That file is too large to be an AP secret (expected 32 bytes).')
                  return
                }
                try {
                  field.onChange(await fileToBase64(chosen))
                  setFileName(chosen.name)
                  setReadError(null)
                } catch {
                  setReadError('That file could not be read.')
                }
              }}
            />
          </div>
        </div>
      )}
    </FieldShell>
  )
}
