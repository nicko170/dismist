import { autocompletion, closeBrackets, closeBracketsKeymap, completionKeymap, type CompletionContext, type CompletionResult } from '@codemirror/autocomplete'
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands'
import { json, jsonParseLinter } from '@codemirror/lang-json'
import { HighlightStyle, bracketMatching, foldGutter, foldKeymap, indentOnInput, syntaxHighlighting, syntaxTree } from '@codemirror/language'
import { linter, lintGutter, type Diagnostic } from '@codemirror/lint'
import { highlightSelectionMatches, search, searchKeymap } from '@codemirror/search'
import { EditorState, type Extension } from '@codemirror/state'
import { EditorView, drawSelection, highlightActiveLine, highlightActiveLineGutter, keymap, lineNumbers, placeholder as placeholderExt } from '@codemirror/view'
import { tags } from '@lezer/highlight'
import type { SyntaxNode } from '@lezer/common'
import { childKeys, forbiddenMatch } from '@/lib/jsonpath'

// CodeMirror 6 for expert patches and config views (design §7.4): JSON
// language and lint with line and column, key completion from the firmware
// path list of the scope, and a client-side mirror of the forbidden paths.
// Colours come from the theme tokens, so both themes follow automatically.

export const editorTheme = EditorView.theme({
  '&': {
    color: 'var(--foreground)',
    backgroundColor: 'var(--card)',
    fontSize: '0.8125rem',
    borderRadius: 'var(--radius-md)',
  },
  '&.cm-focused': { outline: '2px solid var(--ring)', outlineOffset: '2px' },
  '.cm-scroller': { fontFamily: 'var(--font-mono)', lineHeight: '1.6' },
  '.cm-content': { caretColor: 'var(--brand)', padding: '0.5rem 0' },
  '.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--brand)', borderLeftWidth: '2px' },
  '&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection': { backgroundColor: 'var(--brand-soft) !important' },
  '.cm-gutters': { backgroundColor: 'var(--muted)', color: 'var(--muted-foreground)', border: 'none', borderRight: '1px solid var(--border)' },
  '.cm-activeLine': { backgroundColor: 'color-mix(in oklch, var(--accent) 60%, transparent)' },
  '.cm-activeLineGutter': { backgroundColor: 'var(--accent)', color: 'var(--foreground)' },
  '.cm-foldPlaceholder': { backgroundColor: 'var(--accent)', border: '1px solid var(--border)', color: 'var(--muted-foreground)' },
  '.cm-tooltip': { backgroundColor: 'var(--popover)', color: 'var(--popover-foreground)', border: '1px solid var(--border)', borderRadius: 'var(--radius-md)' },
  '.cm-tooltip-autocomplete > ul > li[aria-selected]': { backgroundColor: 'var(--accent)', color: 'var(--foreground)' },
  '.cm-diagnostic-error': { borderLeftColor: 'var(--destructive)' },
  '.cm-diagnostic-warning': { borderLeftColor: 'var(--warning)' },
  '.cm-lintRange-error': { backgroundImage: 'none', textDecoration: 'underline wavy var(--destructive)' },
  '.cm-panels': { backgroundColor: 'var(--muted)', color: 'var(--foreground)' },
  '.cm-panels input, .cm-panels button': { fontSize: 'inherit' },
  '.cm-placeholder': { color: 'var(--muted-foreground)' },
  '.cm-changedLine': { backgroundColor: 'color-mix(in oklch, var(--status-good) 10%, transparent) !important' },
  '.cm-deletedChunk': { backgroundColor: 'color-mix(in oklch, var(--status-crit) 10%, transparent) !important' },
  '.cm-changedText': { background: 'color-mix(in oklch, var(--status-good) 28%, transparent) !important' },
  '.cm-deletedChunk .cm-deletedText': { background: 'color-mix(in oklch, var(--status-crit) 28%, transparent) !important' },
})

const highlight = HighlightStyle.define([
  { tag: tags.propertyName, color: 'var(--chart-4)' },
  { tag: tags.string, color: 'var(--success)' },
  { tag: tags.number, color: 'var(--chart-2)' },
  { tag: [tags.bool, tags.null], color: 'var(--brand)', fontWeight: '600' },
  { tag: tags.invalid, color: 'var(--destructive)' },
])

function keyText(state: EditorState, node: SyntaxNode | null): string {
  if (!node) return ''
  const raw = state.sliceDoc(node.from, node.to)
  try {
    return String(JSON.parse(raw))
  } catch {
    return raw.replace(/^"|"$/g, '')
  }
}

/** The key path of a Property node ("Radios[].RRM.Channel"). */
export function propertyPath(state: EditorState, property: SyntaxNode | null): string {
  const segments: string[] = []
  let node: SyntaxNode | null = property
  let suffix = ''
  while (node && node.name === 'Property') {
    segments.unshift(keyText(state, node.getChild('PropertyName')) + suffix)
    suffix = ''
    let up: SyntaxNode | null = node.parent?.parent ?? null
    while (up && up.name === 'Array') {
      suffix = '[]'
      up = up.parent
    }
    node = up
  }
  return segments.join('.')
}

/** The path of the object a key would be added to ("" for the root). */
function objectPath(state: EditorState, object: SyntaxNode): string {
  let up: SyntaxNode | null = object.parent
  let array = false
  while (up && up.name === 'Array') {
    array = true
    up = up.parent
  }
  if (!up || up.name !== 'Property') return ''
  const path = propertyPath(state, up)
  return array ? `${path}[]` : path
}

function forbiddenLinter(forbidden: readonly string[]) {
  return linter((view) => {
    if (!forbidden.length) return []
    const diagnostics: Diagnostic[] = []
    syntaxTree(view.state).iterate({
      enter: (ref) => {
        if (ref.name !== 'PropertyName') return
        const path = propertyPath(view.state, ref.node.parent)
        const rule = forbiddenMatch(path, forbidden)
        if (rule) {
          diagnostics.push({
            from: ref.from,
            to: ref.to,
            severity: 'error',
            message: `${path} cannot be set here: dismist manages ${rule}.`,
          })
        }
      },
    })
    return diagnostics
  })
}

function keyCompletion(paths: readonly string[]) {
  return (context: CompletionContext): CompletionResult | null => {
    if (!paths.length) return null
    const word = context.matchBefore(/"[A-Za-z0-9_]*/)
    if (!word && !context.explicit) return null
    const from = word?.from ?? context.pos
    // Only in a key position: just after "{" or "," inside an object.
    const before = context.state.sliceDoc(Math.max(0, from - 200), from).trimEnd()
    const last = before.at(-1)
    if (last !== '{' && last !== ',' && before !== '') return null
    let container: SyntaxNode | null = syntaxTree(context.state).resolveInner(from, -1)
    while (container && container.name !== 'Object' && container.name !== 'Array') container = container.parent
    if (container && container.name !== 'Object') return null
    const parent = container ? objectPath(context.state, container) : ''
    const options = childKeys(paths, parent).map((key) => ({ label: key, apply: `"${key}": `, type: 'property' }))
    if (!options.length) return null
    const to = context.state.sliceDoc(context.pos, context.pos + 1) === '"' ? context.pos + 1 : context.pos
    return { from, to, options, validFor: /^"?[A-Za-z0-9_]*$/ }
  }
}

export type EditorOptions = {
  readOnly: boolean
  label: string
  paths?: readonly string[]
  forbidden?: readonly string[]
  placeholder?: string
  onChange?: (value: string) => void
  onDiagnostics?: (count: number) => void
}

export function editorExtensions(options: EditorOptions): Extension[] {
  const base: Extension[] = [
    editorTheme,
    syntaxHighlighting(highlight),
    json(),
    lineNumbers(),
    foldGutter({ openText: '▾', closedText: '▸' }),
    bracketMatching(),
    highlightSelectionMatches(),
    search({ top: true }),
    EditorView.lineWrapping,
    EditorView.contentAttributes.of({ 'aria-label': options.label, autocapitalize: 'none', autocorrect: 'off', spellcheck: 'false' }),
    keymap.of([...defaultKeymap, ...searchKeymap, ...foldKeymap]),
  ]
  if (options.readOnly) return [...base, EditorState.readOnly.of(true), EditorView.editable.of(false)]
  return [
    ...base,
    history(),
    drawSelection(),
    indentOnInput(),
    closeBrackets(),
    highlightActiveLine(),
    highlightActiveLineGutter(),
    lintGutter(),
    linter(jsonParseLinter(), { delay: 250 }),
    forbiddenLinter(options.forbidden ?? []),
    autocompletion({ override: [keyCompletion(options.paths ?? [])], icons: false }),
    keymap.of([...closeBracketsKeymap, ...historyKeymap, ...completionKeymap, indentWithTab]),
    options.placeholder ? placeholderExt(options.placeholder) : [],
    EditorView.updateListener.of((update) => {
      if (update.docChanged) options.onChange?.(update.state.doc.toString())
    }),
  ]
}
