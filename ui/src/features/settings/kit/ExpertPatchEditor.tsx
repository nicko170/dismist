import { useEffect, useMemo, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { CircleAlert, FileDiff, Loader2, Plus, TriangleAlert } from 'lucide-react'
import { describeError, isApiError } from '@/api/errors'
import { keys } from '@/api/keys'
import type { ExpertPatchView, ExpertScope, MutationOptions } from '@/api/queries/sites'
import type { DiffEntry, MutationResponse } from '@/gen/api.gen'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { DiffTable, ImpactNotice, WarningList } from './ConfigForm'
import { checkPatch, keyCompletions, parsePatch, problemRanges } from './expert'
import { markClean, useDirtyRegistration } from './guard'
import { optionMeta } from '@/gen/options.gen'
import { JsonEditor } from './JsonEditor'
import { getOption, hasOption } from './options'
import { provisionalOptions } from './provisional-options'
import { changedPaths } from './patch'
import { ConfirmDialog, Dialog, NativeSelect } from './primitives'
import type { ImpactState } from './useConfigForm'

// The expert patch editor (design §7.4): CodeMirror with JSON lint, key
// autocomplete from the firmware paths, the client-side forbidden-path
// mirror, an "Insert field" picker, a diff against the saved patch, a dry run,
// and a confirmation that lists the typed fields the patch would shadow. The
// heading always names the target scope and object.

type Props = {
  scope: ExpertScope
  heading: string
  headingLevel?: 'h1' | 'h2'
  view: ExpertPatchView
  save: (patch: Record<string, unknown>, options: MutationOptions) => Promise<MutationResponse<ExpertPatchView>>
  siteId: string
  readOnly?: boolean
  onSaved?: (response: MutationResponse<ExpertPatchView>) => void
  legacy?: boolean
}

function format(patch: unknown) {
  return JSON.stringify(patch ?? {}, null, 2)
}

function leafDiff(from: unknown, to: unknown): DiffEntry[] {
  const read = (source: unknown, path: string) =>
    path === '(root)' ? source : path.split('.').reduce<unknown>((n, p) => (n as Record<string, unknown> | undefined)?.[p], source)
  return changedPaths(from, to).map((path) => ({ path, from: read(from, path), to: read(to, path) }))
}

function optionLabel(key: string) {
  return hasOption(key) ? getOption(key).label : key
}

export function ExpertPatchEditor({ scope, heading, headingLevel = 'h2', view, save, siteId, readOnly, onSaved, legacy }: Props) {
  const queryClient = useQueryClient()
  const saved = useMemo(() => format(view.patch), [view.patch])
  const [text, setText] = useState(saved)
  const [savedText, setSavedText] = useState(saved)
  useEffect(() => {
    // A new saved patch arrived (another tab, a restore): adopt it unless editing.
    if (text === savedText) setText(saved)
    setSavedText(saved)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [saved])

  const parsed = useMemo(() => parsePatch(text), [text])
  const problems = useMemo(() => (parsed.ok ? checkPatch(scope, parsed.value) : []), [parsed, scope])
  const blocking = problems.filter((p) => p.kind !== 'warning')
  const savedValue = useMemo(() => parsePatch(savedText), [savedText])
  const diff = parsed.ok && savedValue.ok ? leafDiff(savedValue.value, parsed.value) : []
  const dirty = text.trim() !== savedText.trim() && (!parsed.ok || diff.length > 0)
  const formKey = `expert:${scope}:${view.object_id}`
  useDirtyRegistration(formKey, dirty && !readOnly)

  const [impact, setImpact] = useState<ImpactState<ExpertPatchView>>({ status: 'idle' })
  const [serverErrors, setServerErrors] = useState<string[]>([])
  const [saving, setSaving] = useState(false)
  const [confirm, setConfirm] = useState<string[] | null>(null)
  const [diffOpen, setDiffOpen] = useState(false)
  const [insert, setInsert] = useState('')
  const completions = useMemo(() => keyCompletions(scope), [scope])
  const insertable = useMemo(() => insertablePaths(scope), [scope])

  const body = parsed.ok ? parsed.value : null
  const bodyKey = JSON.stringify(body)
  useEffect(() => {
    if (!dirty || readOnly || !body || blocking.length) {
      setImpact(dirty && (!parsed.ok || blocking.length) ? { status: 'invalid' } : { status: 'idle' })
      return
    }
    const controller = new AbortController()
    const handle = window.setTimeout(async () => {
      setImpact({ status: 'loading' })
      try {
        const data = await save(body, { dryRun: true, ifMatch: view.updated_rev, signal: controller.signal })
        if (!controller.signal.aborted) {
          setServerErrors([])
          setImpact({ status: 'ready', data })
        }
      } catch (error) {
        if (controller.signal.aborted) return
        setServerErrors(isApiError(error) ? Object.entries(error.fields).map(([path, message]) => `${path}: ${message}`) : [])
        setImpact({ status: 'error', error: error as Error })
      }
    }, 600)
    return () => {
      window.clearTimeout(handle)
      controller.abort()
    }
    // bodyKey covers the patch.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [bodyKey, dirty, readOnly, blocking.length, view.updated_rev])

  const shadowed = impact.status === 'ready' ? impact.data.object?.shadowed ?? [] : []
  const partly = impact.status === 'ready' ? impact.data.object?.partly_shadowed ?? [] : []
  const ignored = impact.status === 'ready' ? impact.data.object?.ignored_keys ?? [] : []

  async function commit() {
    if (!body) return
    setSaving(true)
    setConfirm(null)
    try {
      const response = await save(body, { ifMatch: view.updated_rev })
      markClean(formKey)
      const next = format(response.object?.patch ?? body)
      setSavedText(next)
      setText(next)
      setImpact({ status: 'idle' })
      setServerErrors([])
      toast.success('Expert patch saved', {
        description: response.impact?.aps?.length ? `Revision ${response.revision} · applies to ${response.impact.aps.length} AP(s)` : 'No AP changes',
      })
      void queryClient.invalidateQueries({ queryKey: keys.siteChanges(siteId) })
      onSaved?.(response)
    } catch (error) {
      setServerErrors(
        isApiError(error)
          ? [error.message, ...Object.entries(error.fields).map(([path, message]) => `${path}: ${message}`)]
          : [describeError(error).message],
      )
      toast.error('Expert patch not saved')
    } finally {
      setSaving(false)
    }
  }

  const Heading = headingLevel
  return (
    <section aria-labelledby={`${formKey}-heading`} className="grid gap-4">
      <div className="grid gap-1">
        <Heading
          id={`${formKey}-heading`}
          tabIndex={headingLevel === 'h1' ? -1 : undefined}
          data-page-title={headingLevel === 'h1' ? '' : undefined}
          className={cn('font-semibold break-words', headingLevel === 'h1' ? 'text-2xl md:text-[1.75rem] md:leading-tight' : 'text-base')}
        >
          {heading}
        </Heading>
        <p className="text-sm text-muted-foreground">
          A JSON merge patch applied on top of the typed settings{scope === 'wlan' ? ' of this WLAN only' : ''}. Keys are matched without case, as the AP
          reads them; <span className="font-mono">null</span> removes a key. It wins over the typed settings of this scope, and dismist shows every
          field it overrides.
          {legacy ? ' While typed settings are paused, this edits the legacy patch the running config uses.' : ''}
        </p>
      </div>

      {view.shadowed?.length || view.partly_shadowed?.length || view.ignored_keys?.length ? (
        <Alert variant="warning">
          <TriangleAlert />
          <AlertTitle>The saved patch</AlertTitle>
          <AlertDescription className="grid gap-1">
            {view.shadowed?.length ? <p>Overrides: {view.shadowed.map(optionLabel).join(', ')}</p> : null}
            {view.partly_shadowed?.length ? <p>Partly overrides: {view.partly_shadowed.map(optionLabel).join(', ')}</p> : null}
            {view.ignored_keys?.length ? <p>Ignored by AP33 0.15: {view.ignored_keys.join(', ')}</p> : null}
          </AlertDescription>
        </Alert>
      ) : null}

      <JsonEditor
        value={text}
        onChange={setText}
        readOnly={readOnly}
        completions={completions}
        check={(t) => {
          const p = parsePatch(t)
          return p.ok ? problemRanges(t, checkPatch(scope, p.value)) : []
        }}
        ariaLabel={heading}
      />

      {!readOnly ? (
        <div className="flex flex-wrap items-end gap-2">
          <label className="grid min-w-0 flex-1 gap-1 text-sm sm:max-w-sm">
            <span className="font-medium">Insert field</span>
            <NativeSelect value={insert} onChange={(e) => setInsert(e.target.value)}>
              <option value="">Choose a firmware path…</option>
              {insertable.map((path) => (
                <option key={path} value={path}>
                  {path}
                </option>
              ))}
            </NativeSelect>
          </label>
          <Button
            variant="outline"
            disabled={!insert || !parsed.ok}
            onClick={() => {
              if (!parsed.ok) return
              setText(format(insertPath(parsed.value, insert)))
              setInsert('')
            }}
          >
            <Plus /> Insert
          </Button>
        </div>
      ) : null}

      {!parsed.ok ? (
        <p role="alert" className="text-sm font-medium text-destructive">
          {parsed.message}
        </p>
      ) : null}
      {problems.length ? (
        <ul className="grid gap-1.5" aria-label="Problems in the patch">
          {problems.map((p) => (
            <li key={`${p.pointer}:${p.kind}`} className={cn('flex items-start gap-2 text-sm', p.kind === 'warning' ? 'text-foreground' : 'text-destructive')}>
              {p.kind === 'warning' ? <TriangleAlert aria-hidden className="mt-0.5 size-4 shrink-0 text-warning" /> : <CircleAlert aria-hidden className="mt-0.5 size-4 shrink-0" />}
              <span>
                <span className="font-mono text-xs">{p.pointer}</span> {p.message}
              </span>
            </li>
          ))}
        </ul>
      ) : null}
      {serverErrors.length ? (
        <Alert variant="destructive">
          <CircleAlert />
          <AlertTitle>{serverErrors[0]}</AlertTitle>
          {serverErrors.length > 1 ? (
            <AlertDescription>
              <ul className="list-disc pl-4 font-mono text-xs">
                {serverErrors.slice(1).map((m) => (
                  <li key={m}>{m}</li>
                ))}
              </ul>
            </AlertDescription>
          ) : null}
        </Alert>
      ) : null}

      {dirty && !readOnly ? (
        <div
          role="region"
          aria-label="Save expert patch"
          className="sticky bottom-[calc(var(--shell-tabs)+env(safe-area-inset-bottom)+0.75rem)] z-30 grid gap-2 rounded-xl border bg-popover/95 p-3 shadow-lg backdrop-blur md:bottom-4 md:flex md:items-center md:gap-4 md:px-4"
        >
          <div className="grid min-w-0 flex-1 gap-0.5 text-sm" aria-live="polite">
            <span className="font-medium">{diff.length ? `${diff.length} change${diff.length === 1 ? '' : 's'} to the patch` : 'Patch edited'}</span>
            <span className="text-xs break-words md:text-sm">
              <ImpactNotice state={impact} />
            </span>
            {shadowed.length || partly.length ? (
              <span className="text-xs md:text-sm">Overrides typed settings: {[...shadowed, ...partly].map(optionLabel).join(', ')}</span>
            ) : null}
            {ignored.length ? <span className="text-xs text-muted-foreground md:text-sm">Ignored by AP33 0.15: {ignored.join(', ')}</span> : null}
            {impact.status === 'ready' ? <WarningList warnings={impact.data.warnings} /> : null}
          </div>
          <div className="flex flex-wrap gap-2">
            <Button variant="ghost" onClick={() => setDiffOpen(true)} disabled={!diff.length}>
              <FileDiff /> Diff
            </Button>
            <Button variant="outline" onClick={() => setText(savedText)} disabled={saving}>
              Discard
            </Button>
            <Button
              disabled={saving || !parsed.ok || blocking.length > 0}
              onClick={() => (shadowed.length || partly.length ? setConfirm([...shadowed, ...partly]) : void commit())}
            >
              {saving ? <Loader2 className="motion-safe:animate-spin" /> : null}
              Save patch
            </Button>
          </div>
        </div>
      ) : null}

      <Dialog open={diffOpen} onOpenChange={setDiffOpen} title="Changes to the expert patch" wide>
        <DiffTable diff={diff} />
      </Dialog>
      <ConfirmDialog
        open={Boolean(confirm)}
        onOpenChange={(open) => !open && setConfirm(null)}
        title="Override typed settings?"
        description="This patch overrides settings that have their own form fields. Those fields are disabled (or marked) until the key is removed from the patch."
        action="Save patch"
        pending={saving}
        onConfirm={() => void commit()}
      >
        <ul className="list-disc pl-5 text-sm">
          {confirm?.map((key) => (
            <li key={key}>{optionLabel(key)}</li>
          ))}
        </ul>
      </ConfirmDialog>
    </section>
  )
}

/** Firmware paths of the scope's typed options, for the Insert field picker. */
export function insertablePaths(scope: ExpertScope): string[] {
  const out = new Set<string>()
  const registry = { ...provisionalOptions, ...optionMeta }
  for (const meta of Object.values(registry)) {
    if (meta.scope !== scope && !meta.scope.startsWith(`${scope}.`)) continue
    for (const raw of meta.firmwarePaths) {
      const path = raw.replace(/^WLANs\[\]\./, '')
      if (path.startsWith('Radios[].')) {
        out.add(path.replace('Radios[]', 'Radios[2.4]'))
        out.add(path.replace('Radios[]', 'Radios[5]'))
      } else if (/^Radios\[(2\.4|5)\]\./.test(path) || !path.includes('[')) {
        out.add(path)
      }
    }
  }
  return [...out].toSorted()
}

/** Adds a firmware path to a patch with a placeholder value the server will reject until it is replaced. */
export function insertPath(patch: Record<string, unknown>, path: string): Record<string, unknown> {
  const next = structuredClone(patch)
  const radio = path.match(/^Radios\[(2\.4|5)\]\.(.+)$/)
  if (radio) {
    const radios = (Array.isArray(next.Radios) ? next.Radios : []) as Record<string, unknown>[]
    let element = radios.find((r) => String(r.Band) === radio[1])
    if (!element) {
      element = { Band: radio[1] }
      radios.push(element)
    }
    setNested(element, radio[2].split('.'))
    next.Radios = radios
    return next
  }
  setNested(next, path.split('.'))
  return next
}

function setNested(target: Record<string, unknown>, parts: string[]) {
  let node = target
  parts.slice(0, -1).forEach((part) => {
    const key = part.replace(/\[\]$/, '')
    if (typeof node[key] !== 'object' || node[key] === null || Array.isArray(node[key])) node[key] = {}
    node = node[key] as Record<string, unknown>
  })
  const leaf = parts[parts.length - 1].replace(/\[\]$/, '')
  if (!(leaf in node)) node[leaf] = '<value>'
}
