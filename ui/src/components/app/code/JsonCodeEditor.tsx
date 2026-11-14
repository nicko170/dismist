import { useEffect, useRef } from 'react'
import { EditorState } from '@codemirror/state'
import { EditorView } from '@codemirror/view'
import { forEachDiagnostic } from '@codemirror/lint'
import { cn } from '@/lib/utils'
import { editorExtensions, type EditorOptions } from './setup'

// The CodeMirror half of JsonEditor and JsonView (lazy-loaded).

export type JsonCodeEditorProps = Omit<EditorOptions, 'readOnly'> & {
  value: string
  readOnly?: boolean
  className?: string
  /** CSS height bounds; the editor grows with its content in between. */
  minHeight?: string
  maxHeight?: string
}

export default function JsonCodeEditor({ value, readOnly = false, className, minHeight = '10rem', maxHeight = '32rem', ...options }: JsonCodeEditorProps) {
  const host = useRef<HTMLDivElement>(null)
  const view = useRef<EditorView | null>(null)
  const latest = useRef(options)
  useEffect(() => {
    latest.current = options
  })

  const pathsKey = (options.paths ?? []).join('\n')
  const forbiddenKey = (options.forbidden ?? []).join('\n')
  useEffect(() => {
    if (!host.current) return
    const instance = new EditorView({
      parent: host.current,
      state: EditorState.create({
        doc: view.current?.state.doc.toString() ?? value,
        extensions: [
          editorExtensions({
            readOnly,
            label: options.label,
            placeholder: options.placeholder,
            paths: options.paths,
            forbidden: options.forbidden,
            onChange: (next) => latest.current.onChange?.(next),
          }),
          EditorView.updateListener.of((update) => {
            let count = 0
            forEachDiagnostic(update.state, (d) => {
              if (d.severity === 'error') count++
            })
            latest.current.onDiagnostics?.(count)
          }),
          EditorView.theme({ '&': { minHeight, maxHeight }, '.cm-scroller': { minHeight } }),
        ],
      }),
    })
    view.current = instance
    return () => {
      instance.destroy()
      view.current = null
    }
    // value is applied by the effect below; options' identity changes every render.
    // oxlint-disable-next-line react-hooks/exhaustive-deps
  }, [readOnly, pathsKey, forbiddenKey, options.label, options.placeholder, minHeight, maxHeight])

  // An outside change (Reset, Insert field, a reload) replaces the document.
  useEffect(() => {
    const instance = view.current
    if (!instance) return
    const current = instance.state.doc.toString()
    if (current !== value) instance.dispatch({ changes: { from: 0, to: current.length, insert: value } })
  }, [value])

  return <div ref={host} className={cn('min-w-0 overflow-hidden rounded-md border border-input/70 bg-card', className)} data-json-editor="" />
}
