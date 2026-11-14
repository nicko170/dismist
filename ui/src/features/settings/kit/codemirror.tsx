import { useEffect, useRef } from 'react'
import { EditorState, type Extension } from '@codemirror/state'
import { EditorView, highlightActiveLine, keymap, lineNumbers } from '@codemirror/view'
import { json, jsonParseLinter } from '@codemirror/lang-json'
import { linter, lintGutter, lintKeymap, type Diagnostic } from '@codemirror/lint'
import { autocompletion, closeBrackets, closeBracketsKeymap, completionKeymap, type CompletionContext } from '@codemirror/autocomplete'
// @codemirror/language and @lezer/highlight come with @codemirror/lang-json;
// F asks WS-D to list them (and @codemirror/commands, @codemirror/search) directly.
import { bracketMatching, foldGutter, foldKeymap, HighlightStyle, syntaxHighlighting } from '@codemirror/language'
import { tags } from '@lezer/highlight'

// CodeMirror 6 for expert patches and config views (design §7.4 "Expert JSON
// editor"). Loaded lazily: only the expert and config screens pay for it.

const highlight = HighlightStyle.define([
  { tag: tags.propertyName, color: 'var(--chart-4)' },
  { tag: tags.string, color: 'var(--success)' },
  { tag: tags.number, color: 'var(--info)' },
  { tag: [tags.bool, tags.null], color: 'var(--brand)', fontWeight: '500' },
  { tag: tags.invalid, color: 'var(--destructive)' },
])

const theme = EditorView.theme({
  '&': { backgroundColor: 'var(--card)', color: 'var(--foreground)', borderRadius: 'var(--radius-md)', border: '1px solid color-mix(in oklch, var(--input) 70%, transparent)' },
  '&.cm-focused': { outline: '2px solid var(--ring)', outlineOffset: '2px' },
  '.cm-content': { fontFamily: 'var(--font-mono)', fontSize: '13px', caretColor: 'var(--foreground)' },
  '.cm-gutters': { backgroundColor: 'var(--muted)', color: 'var(--muted-foreground)', borderRight: '1px solid var(--border)' },
  '.cm-activeLine': { backgroundColor: 'color-mix(in oklch, var(--accent) 60%, transparent)' },
  '.cm-activeLineGutter': { backgroundColor: 'var(--accent)' },
  '.cm-tooltip': { backgroundColor: 'var(--popover)', color: 'var(--popover-foreground)', border: '1px solid var(--border)' },
  '.cm-tooltip-autocomplete ul li[aria-selected]': { backgroundColor: 'var(--accent)', color: 'var(--foreground)' },
  '.cm-diagnostic-error': { borderLeftColor: 'var(--destructive)' },
  '.cm-lintRange-error': { backgroundImage: 'none', textDecoration: 'underline wavy var(--destructive)' },
  '.cm-scroller': { minHeight: 'var(--cm-min-height, 12rem)', maxHeight: 'var(--cm-max-height, 70vh)', overflow: 'auto' },
})

export type CodeMirrorJsonProps = {
  value: string
  onChange?: (value: string) => void
  readOnly?: boolean
  /** Key names offered by autocomplete (canonical firmware names). */
  completions?: string[]
  /** Extra diagnostics (forbidden paths) for the current text. */
  check?: (text: string) => { from: number; to: number; message: string }[]
  ariaLabel: string
  minHeight?: string
}

function keyCompletion(words: string[]) {
  return (context: CompletionContext) => {
    const word = context.matchBefore(/"?[A-Za-z0-9_]*/)
    if (!word || (word.from === word.to && !context.explicit)) return null
    const quoted = word.text.startsWith('"')
    return {
      from: quoted ? word.from + 1 : word.from,
      options: words.map((label) => ({ label, type: 'property' })),
      validFor: /^[A-Za-z0-9_]*$/,
    }
  }
}

export default function CodeMirrorJson({ value, onChange, readOnly, completions = [], check, ariaLabel, minHeight }: CodeMirrorJsonProps) {
  const host = useRef<HTMLDivElement>(null)
  const view = useRef<EditorView | null>(null)
  const onChangeRef = useRef(onChange)
  onChangeRef.current = onChange
  const checkRef = useRef(check)
  checkRef.current = check

  useEffect(() => {
    if (!host.current) return
    const extensions: Extension[] = [
      lineNumbers(),
      foldGutter(),
      bracketMatching(),
      EditorView.lineWrapping,
      json(),
      syntaxHighlighting(highlight),
      theme,
      keymap.of([...foldKeymap, ...lintKeymap]),
      EditorView.contentAttributes.of({ 'aria-label': ariaLabel }),
      EditorState.readOnly.of(Boolean(readOnly)),
      EditorView.editable.of(!readOnly),
    ]
    if (!readOnly) {
      extensions.push(
        highlightActiveLine(),
        closeBrackets(),
        autocompletion({ override: [keyCompletion(completions)] }),
        keymap.of([...closeBracketsKeymap, ...completionKeymap]),
        lintGutter(),
        linter((v) => {
          const parse = jsonParseLinter()(v) as Diagnostic[]
          if (parse.length) return parse
          return (checkRef.current?.(v.state.doc.toString()) ?? []).map((d) => ({ ...d, severity: 'error' as const }))
        }),
        EditorView.updateListener.of((update) => {
          if (update.docChanged) onChangeRef.current?.(update.state.doc.toString())
        }),
      )
    }
    const instance = new EditorView({ parent: host.current, state: EditorState.create({ doc: value, extensions }) })
    view.current = instance
    return () => {
      instance.destroy()
      view.current = null
    }
    // The editor is built once per mode; value changes are dispatched below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [readOnly, ariaLabel])

  useEffect(() => {
    const instance = view.current
    if (!instance) return
    const current = instance.state.doc.toString()
    if (current !== value) instance.dispatch({ changes: { from: 0, to: current.length, insert: value } })
  }, [value])

  return <div ref={host} className="min-w-0" style={minHeight ? ({ '--cm-min-height': minHeight } as React.CSSProperties) : undefined} />
}
