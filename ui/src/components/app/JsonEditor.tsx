import { lazy, Suspense, useState } from 'react'
import { ListPlus } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { insertPath } from '@/lib/jsonpath'
import { cn } from '@/lib/utils'
import type { JsonCodeEditorProps } from './code/JsonCodeEditor'

// The expert JSON editor and the read-only JSON view (design §7.4). Both
// load CodeMirror on first use. JsonEditor adds the "Insert field" picker;
// the scope's heading ("Expert patch · WLAN INTERNET") is the caller's.

const Code = lazy(() => import('./code/JsonCodeEditor'))

function Loading({ minHeight = '10rem' }: { minHeight?: string }) {
  return <Skeleton className="w-full" style={{ height: minHeight }} />
}

export function JsonView({ value, label, className, maxHeight = '36rem' }: { value: unknown; label: string; className?: string; maxHeight?: string }) {
  const text = typeof value === 'string' ? value : JSON.stringify(value ?? {}, null, 2)
  return (
    <Suspense fallback={<Loading />}>
      <Code value={text} label={label} readOnly className={className} minHeight="6rem" maxHeight={maxHeight} />
    </Suspense>
  )
}

export type JsonEditorProps = Omit<JsonCodeEditorProps, 'readOnly' | 'onChange'> & {
  onChange: (value: string) => void
  /** Offer an "Insert field" picker over these firmware paths. */
  insertable?: boolean
  disabled?: boolean
}

export function JsonEditor({ insertable = true, disabled = false, className, ...props }: JsonEditorProps) {
  const [picking, setPicking] = useState(false)
  const [filter, setFilter] = useState('')
  const [insertError, setInsertError] = useState<string | null>(null)
  const paths = props.paths ?? []
  const matches = filter ? paths.filter((path) => path.toLowerCase().includes(filter.toLowerCase())).slice(0, 50) : paths.slice(0, 50)

  return (
    <div className={cn('grid min-w-0 gap-2', className)}>
      {insertable && paths.length && !disabled ? (
        <div className="grid gap-2">
          <div className="flex flex-wrap items-center gap-2">
            <Button variant="outline" size="sm" aria-expanded={picking} onClick={() => setPicking((open) => !open)}>
              <ListPlus /> Insert field
            </Button>
            {insertError ? (
              <p role="alert" className="text-sm text-destructive">
                {insertError}
              </p>
            ) : null}
          </div>
          {picking ? (
            <div className="grid gap-2 rounded-md border bg-muted/40 p-2">
              <label className="grid gap-1 text-sm">
                <span className="sr-only">Find a firmware field</span>
                <input
                  type="search"
                  value={filter}
                  onChange={(event) => setFilter(event.target.value)}
                  placeholder="Find a firmware field, e.g. LED.Brightness"
                  autoCapitalize="none"
                  autoCorrect="off"
                  spellCheck={false}
                  className="h-11 w-full rounded-md border border-input/70 bg-card px-3 text-base outline-none focus-visible:border-ring fine:md:h-9 fine:md:text-sm"
                />
              </label>
              <ul className="max-h-60 overflow-y-auto" aria-label="Firmware fields">
                {matches.map((path) => (
                  <li key={path}>
                    <button
                      type="button"
                      className="flex min-h-11 w-full cursor-pointer items-center rounded-md px-2 text-left font-mono text-[0.8125rem] break-all hover:bg-accent fine:md:min-h-8"
                      onClick={() => {
                        try {
                          props.onChange(insertPath(props.value, path))
                          setInsertError(null)
                          setPicking(false)
                        } catch (error) {
                          setInsertError(error instanceof Error ? `Fix the JSON first: ${error.message}` : 'Fix the JSON first.')
                        }
                      }}
                    >
                      {path}
                    </button>
                  </li>
                ))}
                {!matches.length ? <li className="px-2 py-2 text-sm text-muted-foreground">No field matches.</li> : null}
              </ul>
            </div>
          ) : null}
        </div>
      ) : null}
      <Suspense fallback={<Loading minHeight={props.minHeight} />}>
        <Code {...props} readOnly={disabled} />
      </Suspense>
    </div>
  )
}
