import { useId, useState } from 'react'
import { useController, useFieldArray, useFormContext, useWatch } from 'react-hook-form'
import { KeyRound, Plus, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input, Textarea } from '@/components/ui/input'
import { Mono } from '@/components/app/identifiers'
import { normaliseMac } from '@/lib/mac'
import { describedBy, FieldShell, ListInput, NumberInput, useOptionState } from './kit/fields'
import { NativeSelect, Switch } from './kit/primitives'
import { parseSSHKeys, sshFingerprint } from './kit/validators'

// Site-level composite fields (design §3.11): SSH key lists, remote syslog,
// blocked clients, eth1 mode, config auto-recovery and the penalty box.

function errorAt(errors: unknown, path: string): string | undefined {
  const node = path.split('.').reduce<unknown>((n, p) => (n as Record<string, unknown> | undefined)?.[p], errors)
  return (node as { message?: string } | undefined)?.message
}

export type SSHKeyRow = { key: string; comment: string; type: string; fingerprint: string }

/** Rows (type, SHA256 fingerprint, comment, Remove) plus a paste box for several lines. */
export function SSHKeysField({ optionKey, name, otherList }: { optionKey: string; name: string; otherList?: string }) {
  const state = useOptionState(optionKey)
  const { control, formState, getValues } = useFormContext()
  const { fields, append, remove } = useFieldArray({ control, name })
  const [paste, setPaste] = useState('')
  const [problems, setProblems] = useState<string[]>([])
  const id = useId()
  if (state.hidden) return null
  const max = state.meta.max ?? 32
  const error = errorAt(formState.errors, name)

  async function add() {
    const { keys: parsed, errors } = parseSSHKeys(paste)
    const existing = new Set((getValues(name) as SSHKeyRow[]).map((k) => k.key))
    const others = new Set(otherList ? (getValues(otherList) as SSHKeyRow[]).map((k) => k.key) : [])
    const notes = errors.map((e) => `${e.line}: ${e.error}`)
    let room = max - existing.size
    for (const key of parsed) {
      if (existing.has(key.key)) {
        notes.push(`${key.comment || key.type}: already in the list.`)
        continue
      }
      if (others.has(key.key)) {
        notes.push(`${key.comment || key.type}: already a ${otherList?.includes('readonly') ? 'read-only' : 'root'} key.`)
        continue
      }
      if (room <= 0) {
        notes.push(`At most ${max} keys.`)
        break
      }
      existing.add(key.key)
      room--
      append({ key: key.key, comment: key.comment, type: key.type, fingerprint: await sshFingerprint(key.blob) })
    }
    setProblems(notes)
    if (!errors.length) setPaste('')
  }

  return (
    <FieldShell as="fieldset" labelAs="legend" id={id} label={state.meta.label} help={state.lockReason ?? state.meta.help} error={error} badges={state.badges}>
      <div className="grid gap-3">
        {fields.length ? (
          <ul className="grid gap-2" aria-label={state.meta.label}>
            {(fields as unknown as (SSHKeyRow & { id: string })[]).map((row, index) => (
              <li key={row.id} className="flex items-start gap-3 rounded-lg border p-3 text-sm">
                <KeyRound aria-hidden className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
                <div className="grid min-w-0 flex-1 gap-0.5">
                  <span className="font-medium break-words">{row.comment || <span className="text-muted-foreground">No comment</span>}</span>
                  <span className="text-xs text-muted-foreground">{row.type}</span>
                  <Mono className="text-xs">{row.fingerprint || '(fingerprint after saving)'}</Mono>
                </div>
                <Button variant="ghost" size="icon-sm" aria-label={`Remove key ${row.comment || index + 1}`} disabled={!state.editable} onClick={() => remove(index)}>
                  <Trash2 />
                </Button>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-sm text-muted-foreground">No keys yet.</p>
        )}
        <label htmlFor={id} className="text-sm font-medium">
          Paste public keys
        </label>
        <Textarea
          id={id}
          value={paste}
          onChange={(e) => setPaste(e.target.value)}
          placeholder="ssh-ed25519 AAAA… you@laptop"
          className="font-mono text-xs"
          disabled={!state.editable}
          autoCapitalize="none"
          autoCorrect="off"
          spellCheck={false}
          aria-describedby={describedBy(id, state.meta.help, problems.length ? 'x' : undefined)}
        />
        {problems.length ? (
          <ul id={`${id}-error`} role="alert" className="grid gap-1 text-sm text-destructive">
            {problems.map((p) => (
              <li key={p}>{p}</li>
            ))}
          </ul>
        ) : null}
        <Button variant="outline" size="sm" className="justify-self-start" disabled={!state.editable || !paste.trim()} onClick={() => void add()}>
          <Plus /> Add keys
        </Button>
      </div>
    </FieldShell>
  )
}

export type SyslogRow = { protocol: 'udp' | 'tcp'; host: string; port: number | string | null; facility: number | string | null; severity: number | string | null; tag: string }

export function SyslogField() {
  const state = useOptionState('site.syslog')
  const { control, register, formState } = useFormContext()
  const { fields, append, remove } = useFieldArray({ control, name: 'syslog' })
  const id = useId()
  if (state.hidden) return null
  return (
    <FieldShell as="fieldset" labelAs="legend" id={id} label={state.meta.label} help="Facility 0 to 23 and severity 0 to 7 make the syslog priority." badges={state.badges}>
      <div className="grid gap-3">
        {fields.map((row, index) => {
          const base = `syslog.${index}`
          const err = (leaf: string) => errorAt(formState.errors, `${base}.${leaf}`)
          return (
            <div key={row.id} className="grid gap-2 rounded-lg border p-3 sm:grid-cols-2 lg:grid-cols-[6rem_minmax(0,2fr)_6rem_6rem_6rem_minmax(0,1fr)_auto] lg:items-start">
              <label className="grid gap-1 text-sm">
                <span className="text-muted-foreground">Protocol</span>
                <NativeSelect {...register(`${base}.protocol`)} disabled={!state.editable}>
                  <option value="udp">UDP</option>
                  <option value="tcp">TCP</option>
                </NativeSelect>
              </label>
              <label className="grid gap-1 text-sm">
                <span className="text-muted-foreground">Host</span>
                <Input {...register(`${base}.host`)} disabled={!state.editable} autoCapitalize="none" autoCorrect="off" spellCheck={false} aria-invalid={err('host') ? true : undefined} />
                {err('host') ? <span role="alert" className="text-destructive">{err('host')}</span> : null}
              </label>
              <SmallNumber name={`${base}.port`} label="Port" min={1} max={65535} disabled={!state.editable} error={err('port')} />
              <SmallNumber name={`${base}.facility`} label="Facility" min={0} max={23} disabled={!state.editable} error={err('facility')} />
              <SmallNumber name={`${base}.severity`} label="Severity" min={0} max={7} disabled={!state.editable} error={err('severity')} />
              <label className="grid gap-1 text-sm">
                <span className="text-muted-foreground">Tag</span>
                <Input {...register(`${base}.tag`)} disabled={!state.editable} autoCapitalize="none" />
              </label>
              <Button variant="ghost" size="icon-sm" className="lg:mt-6" aria-label={`Remove syslog target ${index + 1}`} disabled={!state.editable} onClick={() => remove(index)}>
                <Trash2 />
              </Button>
            </div>
          )
        })}
        <Button
          variant="outline"
          size="sm"
          className="justify-self-start"
          disabled={!state.editable || fields.length >= 16}
          onClick={() => append({ protocol: 'udp', host: '', port: 514, facility: 1, severity: 6, tag: '' } satisfies SyslogRow)}
        >
          <Plus /> Add syslog target
        </Button>
      </div>
    </FieldShell>
  )
}

export function SmallNumber({ name, label, min, max, disabled, error, unit, placeholder }: { name: string; label: string; min: number; max: number; disabled?: boolean; error?: string; unit?: string; placeholder?: string }) {
  const { control } = useFormContext()
  const { field } = useController({ control, name })
  const id = useId()
  return (
    <div className="grid gap-1 text-sm">
      <label htmlFor={id} className="text-muted-foreground">
        {label}
      </label>
      <NumberInput id={id} name={field.name} inputRef={field.ref} value={field.value as number | null} onChange={field.onChange} onBlur={field.onBlur} min={min} max={max} unit={unit} disabled={disabled} placeholder={placeholder} aria-invalid={error ? true : undefined} aria-describedby={error ? `${id}-error` : undefined} />
      {error ? (
        <span id={`${id}-error`} role="alert" className="text-destructive">
          {error}
        </span>
      ) : null}
    </div>
  )
}

export type BlockedRow = { mac: string; note: string }

/** Blocked clients: paste MACs in any format; stored lower-case and dashed (§3.11 Protection). */
export function MACListField() {
  const state = useOptionState('site.protection.blocked_clients')
  const { control, register, formState, getValues } = useFormContext()
  const { fields, append, remove } = useFieldArray({ control, name: 'protection.blocked_clients' })
  const [paste, setPaste] = useState('')
  const [problems, setProblems] = useState<string[]>([])
  const id = useId()
  if (state.hidden) return null

  function add() {
    const existing = new Set((getValues('protection.blocked_clients') as BlockedRow[]).map((r) => r.mac))
    const notes: string[] = []
    for (const token of paste.split(/[\s,;]+/).filter(Boolean)) {
      const mac = normaliseMac(token)
      if (!mac) notes.push(`${token.slice(0, 40)}: not a MAC address.`)
      else if (existing.has(mac)) notes.push(`${mac}: already blocked.`)
      else {
        existing.add(mac)
        append({ mac, note: '' })
      }
    }
    setProblems(notes)
    setPaste('')
  }

  return (
    <FieldShell as="fieldset" labelAs="legend" id={id} label={state.meta.label} help={state.meta.help} badges={state.badges}>
      <div className="grid gap-3">
        {fields.length ? (
          <ul className="grid gap-2">
            {(fields as unknown as (BlockedRow & { id: string })[]).map((row, index) => (
              <li key={row.id} className="grid gap-2 rounded-lg border p-3 sm:grid-cols-[minmax(0,14rem)_minmax(0,1fr)_auto] sm:items-center">
                <Mono className="text-sm">{row.mac}</Mono>
                <Input {...register(`protection.blocked_clients.${index}.note`)} aria-label={`Note for ${row.mac}`} placeholder="Note (optional)" disabled={!state.editable} />
                <Button variant="ghost" size="icon-sm" aria-label={`Unblock ${row.mac}`} disabled={!state.editable} onClick={() => remove(index)}>
                  <Trash2 />
                </Button>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-sm text-muted-foreground">No blocked clients.</p>
        )}
        <div className="flex flex-wrap items-end gap-2">
          <label className="grid min-w-0 flex-1 gap-1 text-sm">
            <span className="font-medium">Add MAC addresses</span>
            <Input value={paste} onChange={(e) => setPaste(e.target.value)} placeholder="02:aa:bb:cc:dd:ee" disabled={!state.editable} autoCapitalize="none" autoCorrect="off" spellCheck={false} />
          </label>
          <Button variant="outline" disabled={!state.editable || !paste.trim()} onClick={add}>
            <Plus /> Block
          </Button>
        </div>
        {problems.length ? (
          <ul role="alert" className="grid gap-1 text-sm text-destructive">
            {problems.map((p) => (
              <li key={p}>{p}</li>
            ))}
          </ul>
        ) : null}
        {errorAt(formState.errors, 'protection.blocked_clients') ? <p role="alert" className="text-sm text-destructive">{errorAt(formState.errors, 'protection.blocked_clients')}</p> : null}
      </div>
    </FieldShell>
  )
}

export type Eth1Form = { mode: 'inherit' | 'same_as_uplink' | 'access' | 'trunk' | 'disabled'; vlan_id: number | string | null; vlans: string; pvid: number | string | null }

/** eth1 mode (§3.6 Ports, §3.11 AP defaults): experimental, may strand an AP. */
export function Eth1Field({ optionKey, name, allowInherit }: { optionKey: string; name: string; allowInherit?: boolean }) {
  const { control, register, formState } = useFormContext()
  const mode = useWatch({ control, name: `${name}.mode` }) as Eth1Form['mode']
  // Inherit and "same as uplink" are the unset values: experimental and unused stays hidden.
  const state = useOptionState(optionKey, { value: mode === 'inherit' || mode === 'same_as_uplink' ? null : mode })
  const id = useId()
  if (state.hidden) return null
  return (
    <FieldShell as="fieldset" labelAs="legend" id={id} label={state.meta.label} help={state.lockReason ?? 'eth0 is the uplink and can never be disabled.'} badges={state.badges}>
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="grid gap-1 text-sm">
          <span className="text-muted-foreground">Mode</span>
          <NativeSelect {...register(`${name}.mode`)} disabled={!state.editable}>
            {allowInherit ? <option value="inherit">Inherit from site</option> : null}
            <option value="same_as_uplink">Same as uplink</option>
            <option value="access">Access VLAN</option>
            <option value="trunk">Custom trunk</option>
            <option value="disabled">Disabled</option>
          </NativeSelect>
        </label>
        {mode === 'access' ? <SmallNumber name={`${name}.vlan_id`} label="VLAN" min={1} max={4094} disabled={!state.editable} error={errorAt(formState.errors, `${name}.vlan_id`)} /> : null}
        {mode === 'trunk' ? (
          <>
            <label className="grid gap-1 text-sm">
              <span className="text-muted-foreground">VLANs</span>
              <Input {...register(`${name}.vlans`)} placeholder="1, 30, 40" inputMode="numeric" disabled={!state.editable} aria-invalid={errorAt(formState.errors, `${name}.vlans`) ? true : undefined} />
              {errorAt(formState.errors, `${name}.vlans`) ? <span role="alert" className="text-destructive">{errorAt(formState.errors, `${name}.vlans`)}</span> : null}
            </label>
            <SmallNumber name={`${name}.pvid`} label="Native VLAN (PVID)" min={1} max={4094} disabled={!state.editable} error={errorAt(formState.errors, `${name}.pvid`)} />
          </>
        ) : null}
      </div>
    </FieldShell>
  )
}

export function eth1Error(value: Eth1Form): { path: string; message: string } | null {
  const vlan = (v: unknown) => Number.isInteger(v) && (v as number) >= 1 && (v as number) <= 4094
  if (value.mode === 'access' && !vlan(value.vlan_id)) return { path: 'vlan_id', message: 'VLAN 1 to 4094.' }
  if (value.mode === 'trunk') {
    const list = value.vlans.split(/[\s,]+/).filter(Boolean)
    if (!list.length || !list.every((x) => /^\d+$/.test(x) && Number(x) >= 1 && Number(x) <= 4094)) return { path: 'vlans', message: 'VLANs 1 to 4094, separated by commas.' }
    if (!vlan(value.pvid)) return { path: 'pvid', message: 'VLAN 1 to 4094.' }
    if (!list.map(Number).includes(value.pvid as number)) return { path: 'pvid', message: 'The native VLAN must be in the VLAN list.' }
  }
  return null
}

export function eth1ToForm(value: { mode: Eth1Form['mode']; vlan_id: number | null; vlans: number[]; pvid: number | null } | null | undefined, inherit: boolean): Eth1Form {
  if (!value) return { mode: inherit ? 'inherit' : 'same_as_uplink', vlan_id: null, vlans: '', pvid: null }
  return { mode: value.mode, vlan_id: value.vlan_id, vlans: value.vlans.join(', '), pvid: value.pvid }
}

export function eth1FromForm(form: Eth1Form) {
  if (form.mode === 'inherit') return null
  return {
    mode: form.mode,
    vlan_id: form.mode === 'access' ? Number(form.vlan_id) : null,
    vlans: form.mode === 'trunk' ? form.vlans.split(/[\s,]+/).filter(Boolean).map(Number) : [],
    pvid: form.mode === 'trunk' ? Number(form.pvid) : null,
  }
}

/** A switch plus a number (config auto-recovery minutes, penalty-box threshold). */
export function ToggleWithNumbers({
  optionKey,
  enabledName,
  label,
  numbers,
}: {
  optionKey: string
  enabledName: string
  label: string
  numbers: { name: string; label: string; min: number; max: number; unit?: string }[]
}) {
  const { control, formState } = useFormContext()
  const { field } = useController({ control, name: enabledName })
  const state = useOptionState(optionKey, { value: field.value ? true : null })
  const id = useId()
  if (state.hidden) return null
  return (
    <FieldShell id={id} label={state.meta.label} help={state.lockReason ?? state.meta.help} badges={state.badges} as="fieldset" labelAs="legend">
      <label className="flex min-h-11 items-center gap-3 text-sm">
        <Switch checked={Boolean(field.value)} onCheckedChange={field.onChange} disabled={!state.editable} aria-label={label} />
        {label}
      </label>
      {field.value ? (
        <div className="grid gap-3 sm:grid-cols-2">
          {numbers.map((n) => (
            <SmallNumber key={n.name} name={n.name} label={n.label} min={n.min} max={n.max} unit={n.unit} disabled={!state.editable} error={errorAt(formState.errors, n.name)} />
          ))}
        </div>
      ) : null}
    </FieldShell>
  )
}

/** NTP and DNS server lists. */
export function ServerListField({ optionKey, name, label, help, placeholder, inputMode, warning }: { optionKey: string; name: string; label?: string; help?: string; placeholder?: string; inputMode?: 'decimal' | 'url'; warning?: string }) {
  const state = useOptionState(optionKey)
  const { control, formState } = useFormContext()
  const { field } = useController({ control, name })
  const id = useId()
  if (state.hidden) return null
  const value = (field.value as string[]) ?? []
  const listErrors = formState.errors && errorAt(formState.errors, name)
  return (
    <FieldShell id={id} label={label ?? state.meta.label} help={state.lockReason ?? help ?? state.meta.help} error={listErrors} badges={label ? undefined : state.badges} labelAs="span">
      <ListInput id={id} value={value} onChange={field.onChange} max={label ? null : state.meta.max} disabled={!state.editable} itemLabel="Server" placeholder={placeholder} inputMode={inputMode} describedBy={describedBy(id, help ?? state.meta.help, listErrors)} />
      {warning && value.length ? <p className="text-sm text-warning">{warning}</p> : null}
    </FieldShell>
  )
}
