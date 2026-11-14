import { lazy, Suspense, useId } from 'react'
import { Textarea } from '@/components/ui/input'
import type { CodeMirrorJsonProps } from './codemirror'

// Lazy CodeMirror (design §7.9): the chunk loads only on expert and config
// screens. While it loads, a plain textarea keeps the text editable.

const CodeMirrorJson = lazy(() => import('./codemirror'))

export function JsonEditor(props: CodeMirrorJsonProps) {
  return (
    <Suspense fallback={<Fallback {...props} />}>
      <CodeMirrorJson {...props} />
    </Suspense>
  )
}

function Fallback({ value, onChange, readOnly, ariaLabel }: CodeMirrorJsonProps) {
  const id = useId()
  return (
    <Textarea
      id={id}
      aria-label={ariaLabel}
      value={value}
      readOnly={readOnly}
      onChange={(e) => onChange?.(e.target.value)}
      className="min-h-48 font-mono text-xs"
      spellCheck={false}
      autoCapitalize="none"
      autoCorrect="off"
    />
  )
}

/** Read-only JSON with folding (config views, history). */
export function JsonView({ value, ariaLabel }: { value: unknown; ariaLabel: string }) {
  const text = typeof value === 'string' ? value : JSON.stringify(value ?? {}, null, 2)
  return <JsonEditor value={text} readOnly ariaLabel={ariaLabel} />
}
