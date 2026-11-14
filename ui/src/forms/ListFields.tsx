import { useEffect, useState, type ReactNode } from 'react'
import { useController } from 'react-hook-form'
import { KeyRound, Plus, X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input, Textarea } from '@/components/ui/input'
import { normaliseMac } from '@/lib/mac'
import { parseSSHKey, sshFingerprint, sshTypeLabel } from '@/lib/ssh'
import { cn } from '@/lib/utils'
import type { BoundField } from './common'
import { FieldShell } from './Field'
import { useInherit } from './inherit'
import { identifierProps } from './TextFields'

// Lists of short values (design §7.4): servers, NTP, DNS, MACs, SSH keys.
// The value is a string array. Pasting several lines into one row splits
// them into rows. Per-row errors come from the schema (path name.N).

function itemError(error: unknown, index: number): string | undefined {
  if (!error || typeof error !== 'object') return undefined
  const item = (error as Record<number, { message?: string } | undefined>)[index]
  return typeof item?.message === 'string' ? item.message : undefined
}

function rootError(error: unknown): string | undefined {
  if (!error || typeof error !== 'object') return undefined
  const e = error as { message?: string; root?: { message?: string } }
  return e.message ?? e.root?.message
}

export function ListField({
  name,
  disabled,
  markDirty = true,
  inherit,
  max,
  placeholder,
  addLabel = 'Add',
  identifier = true,
  mono = true,
  inputMode,
  normalise,
  itemLabel = 'Item',
  ...chrome
}: BoundField & {
  max?: number
  placeholder?: string
  addLabel?: string
  identifier?: boolean
  mono?: boolean
  inputMode?: 'text' | 'decimal' | 'numeric' | 'url'
  /** Applied to a row on blur (for example MAC canonical form). */
  normalise?: (value: string) => string
  /** Accessible name prefix for each row ("DNS server 2"). */
  itemLabel?: string
}) {
  const { field, fieldState } = useController({ name, disabled })
  const { slot, locked } = useInherit(name, inherit, disabled)
  const items: string[] = Array.isArray(locked ? inherit?.value : field.value) ? ((locked ? inherit?.value : field.value) as string[]) : []
  const set = (next: string[]) => field.onChange(next)
  const full = max !== undefined && items.length >= max
  return (
    <FieldShell {...chrome} labelFor={false} dirty={markDirty && fieldState.isDirty} error={rootError(fieldState.error)} inherit={slot}>
      {(ids) => (
        <div className="grid gap-2" aria-describedby={ids.describedBy}>
          {items.length ? (
            <ol className="grid gap-2">
              {items.map((item, index) => {
                const error = itemError(fieldState.error, index)
                const errorId = `${ids.id}-${index}-error`
                return (
                  <li key={index} className="grid gap-1">
                    <div className="flex gap-2">
                      <Input
                        {...(identifier ? identifierProps : {})}
                        aria-label={`${itemLabel} ${index + 1}`}
                        aria-invalid={error ? true : undefined}
                        aria-describedby={error ? errorId : undefined}
                        value={item}
                        inputMode={inputMode}
                        placeholder={placeholder}
                        disabled={field.disabled || locked}
                        className={cn(mono && 'font-mono')}
                        onChange={(event) => {
                          const lines = event.target.value.split(/[\r\n]+/).map((l) => l.trim())
                          const next = [...items]
                          if (lines.length > 1) next.splice(index, 1, ...lines.filter(Boolean))
                          else next[index] = event.target.value
                          set(max !== undefined ? next.slice(0, max) : next)
                        }}
                        onBlur={() => {
                          if (normalise && items[index]) {
                            const next = [...items]
                            next[index] = normalise(items[index])
                            if (next[index] !== items[index]) set(next)
                          }
                          field.onBlur()
                        }}
                      />
                      <Button
                        variant="ghost"
                        size="icon"
                        aria-label={`Remove ${itemLabel.toLowerCase()} ${index + 1}`}
                        disabled={field.disabled || locked}
                        onClick={() => set(items.filter((_item, i) => i !== index))}
                      >
                        <X />
                      </Button>
                    </div>
                    {error ? (
                      <p id={errorId} role="alert" className="text-sm font-medium text-destructive">
                        {error}
                      </p>
                    ) : null}
                  </li>
                )
              })}
            </ol>
          ) : null}
          <div className="flex flex-wrap items-center gap-2">
            <Button variant="outline" size="sm" disabled={field.disabled || locked || full} onClick={() => set([...items, ''])}>
              <Plus aria-hidden /> {addLabel}
            </Button>
            {max !== undefined ? (
              <span className="text-xs text-muted-foreground tabular-nums">
                {items.length} of {max}
              </span>
            ) : null}
          </div>
        </div>
      )}
    </FieldShell>
  )
}

/** MAC addresses, shown and stored in the canonical aa-bb-cc-dd-ee-ff form. */
export function MACListField(props: Omit<Parameters<typeof ListField>[0], 'normalise' | 'mono' | 'identifier'>) {
  return <ListField itemLabel="MAC address" placeholder="02-aa-00-00-00-01" addLabel="Add MAC address" {...props} normalise={(v) => normaliseMac(v) ?? v.trim()} />
}

function KeyCard({ line, onRemove, disabled, error }: { line: string; onRemove: () => void; disabled?: boolean; error?: string }) {
  const key = parseSSHKey(line)
  const blob = key?.blob
  const [fingerprint, setFingerprint] = useState<string | null>(null)
  useEffect(() => {
    let cancelled = false
    if (blob) void sshFingerprint(blob).then((fp) => !cancelled && setFingerprint(fp)).catch(() => undefined)
    return () => {
      cancelled = true
    }
  }, [blob])
  return (
    <li className={cn('flex items-start gap-3 rounded-md border bg-card px-3 py-2.5', error && 'border-destructive/50')}>
      <KeyRound aria-hidden className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
      <div className="grid min-w-0 flex-1 gap-0.5">
        <span className="text-sm font-medium break-words">
          {key ? (key.comment || 'No comment') : 'Not a valid public key'}
          {key ? <span className="ml-2 text-xs font-normal text-muted-foreground">{sshTypeLabel(key.type)}</span> : null}
        </span>
        <span className="font-mono text-xs break-all text-muted-foreground">{fingerprint ?? (key ? 'Fingerprint…' : line.slice(0, 60))}</span>
        {error ? (
          <span role="alert" className="text-sm font-medium text-destructive">
            {error}
          </span>
        ) : null}
      </div>
      <Button variant="ghost" size="icon-sm" aria-label={`Remove key ${key?.comment || ''}`.trim()} disabled={disabled} onClick={onRemove} className="-my-1.5">
        <X />
      </Button>
    </li>
  )
}

/** Site SSH keys: one card per key with type, comment and SHA256 fingerprint; paste to add. */
export function SSHKeysField({ name, disabled, markDirty = true, max, emptyText = 'No keys yet.', ...chrome }: Omit<BoundField, 'inherit'> & { max?: number; emptyText?: ReactNode }) {
  const { field, fieldState } = useController({ name, disabled })
  const keys: string[] = Array.isArray(field.value) ? (field.value as string[]) : []
  const [draft, setDraft] = useState('')
  const [draftError, setDraftError] = useState<string | null>(null)
  const add = () => {
    const lines = draft.split(/\r?\n/).map((l) => l.trim()).filter(Boolean)
    if (!lines.length) return
    const parsed = lines.map(parseSSHKey)
    const bad = parsed.findIndex((key) => !key)
    if (bad >= 0) {
      setDraftError(lines.length > 1 ? `Line ${bad + 1} is not an OpenSSH public key.` : 'That is not an OpenSSH public key. Paste the .pub file, like "ssh-ed25519 AAAA… you@laptop".')
      return
    }
    const existing = new Set(keys.map((k) => parseSSHKey(k)?.blob))
    const fresh = parsed.filter((key) => key && !existing.has(key.blob)).map((key) => key!.line)
    const next = [...keys, ...fresh]
    if (max !== undefined && next.length > max) {
      setDraftError(`At most ${max} keys.`)
      return
    }
    field.onChange(next)
    field.onBlur()
    setDraft('')
    setDraftError(null)
  }
  return (
    <FieldShell {...chrome} dirty={markDirty && fieldState.isDirty} error={draftError ?? rootError(fieldState.error)}>
      {(ids) => (
        <div className="grid gap-3">
          {keys.length ? (
            <ul className="grid gap-2" aria-label="Keys">
              {keys.map((line, index) => (
                <KeyCard
                  key={line}
                  line={line}
                  disabled={field.disabled}
                  error={itemError(fieldState.error, index)}
                  onRemove={() => field.onChange(keys.filter((_k, i) => i !== index))}
                />
              ))}
            </ul>
          ) : (
            <p className="text-sm text-muted-foreground">{emptyText}</p>
          )}
          <div className="grid gap-2">
            <Textarea
              id={ids.id}
              ref={field.ref}
              aria-invalid={ids.invalid || undefined}
              aria-describedby={ids.describedBy}
              {...identifierProps}
              rows={3}
              value={draft}
              disabled={field.disabled}
              placeholder="Paste one or more public keys (ssh-ed25519 AAAA… you@laptop)"
              className="font-mono text-[0.9em]"
              onChange={(event) => {
                setDraft(event.target.value)
                setDraftError(null)
              }}
            />
            <Button variant="outline" size="sm" className="justify-self-start" disabled={field.disabled || !draft.trim()} onClick={add}>
              <Plus aria-hidden /> Add key
            </Button>
          </div>
        </div>
      )}
    </FieldShell>
  )
}
