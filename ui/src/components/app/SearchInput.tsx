import { useEffect, useRef, useState } from 'react'
import { Search, X } from 'lucide-react'
import { inputClass } from '@/components/ui/input'
import { cn } from '@/lib/utils'

// A list search box (design §7.3). `onChange` fires after a short pause, so
// typing does not rewrite the URL on every key. `shortcut` focuses it with
// "/" on desktop (design §7.8), unless another text control has focus.

export function SearchInput({
  value,
  onChange,
  label = 'Search',
  placeholder = 'Search',
  delay = 200,
  shortcut = false,
  className,
}: {
  value: string
  onChange: (value: string) => void
  label?: string
  placeholder?: string
  delay?: number
  shortcut?: boolean
  className?: string
}) {
  const [draft, setDraft] = useState<string | null>(null)
  const input = useRef<HTMLInputElement>(null)
  const latest = useRef(onChange)
  useEffect(() => {
    latest.current = onChange
  })

  useEffect(() => {
    if (draft === null) return
    const timer = window.setTimeout(() => {
      latest.current(draft.trim())
      setDraft(null)
    }, delay)
    return () => window.clearTimeout(timer)
  }, [draft, delay])

  useEffect(() => {
    if (!shortcut) return
    const onKey = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null
      if (event.key !== '/' || event.metaKey || event.ctrlKey || event.altKey) return
      if (target?.closest('input, textarea, select, [contenteditable="true"], .cm-editor')) return
      event.preventDefault()
      input.current?.focus()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [shortcut])

  const shown = draft ?? value
  return (
    <div className={cn('relative', className)}>
      <label>
        <span className="sr-only">{label}</span>
        <Search aria-hidden className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
        <input
          ref={input}
          type="search"
          value={shown}
          onChange={(event) => setDraft(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Escape' && shown) {
              event.preventDefault()
              setDraft('')
            }
          }}
          placeholder={placeholder}
          autoCapitalize="none"
          autoCorrect="off"
          spellCheck={false}
          enterKeyHint="search"
          className={cn(inputClass, 'pr-11 pl-9 [&::-webkit-search-cancel-button]:hidden')}
        />
      </label>
      {shown ? (
        <button
          type="button"
          aria-label="Clear search"
          onClick={() => {
            setDraft('')
            input.current?.focus()
          }}
          className="absolute top-1/2 right-0 flex size-11 -translate-y-1/2 cursor-pointer items-center justify-center rounded-md text-muted-foreground hover:text-foreground fine:md:size-9"
        >
          <X aria-hidden className="size-4" />
        </button>
      ) : shortcut ? (
        <kbd aria-hidden className="pointer-events-none absolute top-1/2 right-2.5 -translate-y-1/2 rounded border px-1.5 font-mono text-xs text-muted-foreground max-lg:hidden">
          /
        </kbd>
      ) : null}
    </div>
  )
}
