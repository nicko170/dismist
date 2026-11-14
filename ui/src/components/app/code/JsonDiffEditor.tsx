import { useEffect, useRef } from 'react'
import { unifiedMergeView } from '@codemirror/merge'
import { EditorState } from '@codemirror/state'
import { EditorView } from '@codemirror/view'
import { cn } from '@/lib/utils'
import { editorExtensions } from './setup'

// A unified line diff of two JSON documents (lazy-loaded by JsonDiff):
// deleted lines from `before` are shown inline above the `after` lines that
// replace them. Read-only.

export default function JsonDiffEditor({ before, after, label, className }: { before: string; after: string; label: string; className?: string }) {
  const host = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!host.current) return
    const view = new EditorView({
      parent: host.current,
      state: EditorState.create({
        doc: after,
        extensions: [
          editorExtensions({ readOnly: true, label }),
          unifiedMergeView({ original: before, mergeControls: false, highlightChanges: true, gutter: true }),
          EditorView.theme({ '&': { maxHeight: '36rem' } }),
        ],
      }),
    })
    return () => view.destroy()
  }, [before, after, label])
  return <div ref={host} className={cn('min-w-0 overflow-hidden rounded-md border', className)} data-json-diff="" />
}
