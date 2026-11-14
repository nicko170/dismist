import { useEffect, useRef, useState } from 'react'
import { useRouter } from '@tanstack/react-router'
import { Dialog, DialogContent } from '@/components/ui/dialog'
import { siteNav, type NavId } from './nav'
import { useNavSite } from './site'

// Desktop keyboard shortcuts (design §7.8, phase 4): "/" focuses the list
// search (TopBar), "g" then a letter goes to a section of the current site,
// "?" lists them. Keys typed into a field, an editor or with a modifier are
// never taken.

export const GO_KEYS: Record<string, NavId> = {
  o: 'overview',
  a: 'aps',
  c: 'clients',
  w: 'wlans',
  r: 'rf',
  e: 'events',
  h: 'changes',
  s: 'settings',
}

const ARM_MS = 1500

export function isTypingTarget(target: EventTarget | null): boolean {
  const element = target as HTMLElement | null
  if (!element || typeof element.closest !== 'function') return false
  return Boolean(element.closest('input, textarea, select, [contenteditable="true"], [role="combobox"], .cm-editor'))
}

/** The pure key machine: returns where to go, "help", or null; tracks the "g" prefix. */
export function shortcutFor(key: string, armed: boolean): { action: 'arm' | 'help' | 'go' | null; target?: NavId } {
  if (armed && GO_KEYS[key]) return { action: 'go', target: GO_KEYS[key] }
  if (key === 'g') return { action: 'arm' }
  if (key === '?') return { action: 'help' }
  return { action: null }
}

const rows: { keys: string[]; label: string }[] = [
  { keys: ['/'], label: 'Search this list' },
  ...Object.entries(GO_KEYS).map(([key, id]) => ({ keys: ['g', key], label: `Go to ${siteNav.find((item) => item.id === id)?.label ?? id}` })),
  { keys: ['?'], label: 'Show these shortcuts' },
]

export function KeyboardShortcuts() {
  const router = useRouter()
  const { siteId } = useNavSite()
  const [help, setHelp] = useState(false)
  const armedUntil = useRef(0)
  const site = useRef(siteId)
  useEffect(() => {
    site.current = siteId
  }, [siteId])

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.metaKey || event.ctrlKey || event.altKey) return
      if (isTypingTarget(event.target)) return
      if (document.querySelector('[role="dialog"], [role="alertdialog"]') && event.key !== '?') return
      const armed = Date.now() < armedUntil.current
      const result = shortcutFor(event.key, armed)
      if (!result.action) {
        armedUntil.current = 0
        return
      }
      event.preventDefault()
      if (result.action === 'arm') {
        armedUntil.current = Date.now() + ARM_MS
        return
      }
      armedUntil.current = 0
      if (result.action === 'help') {
        setHelp((open) => !open)
        return
      }
      const item = siteNav.find((nav) => nav.id === result.target)
      if (item) void router.navigate({ to: item.link(site.current).to as never })
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [router])

  return (
    <Dialog open={help} onOpenChange={setHelp}>
      <DialogContent title="Keyboard shortcuts" description="Available when no text field has focus.">
        <dl className="grid gap-2">
          {rows.map((row) => (
            <div key={row.label} className="flex items-center justify-between gap-4 text-sm">
              <dt>{row.label}</dt>
              <dd className="flex shrink-0 items-center gap-1">
                {row.keys.map((key, index) => (
                  <span key={key} className="flex items-center gap-1">
                    {index ? <span className="text-xs text-muted-foreground">then</span> : null}
                    <kbd className="min-w-6 rounded border bg-muted px-1.5 py-0.5 text-center font-mono text-xs">{key}</kbd>
                  </span>
                ))}
              </dd>
            </div>
          ))}
        </dl>
      </DialogContent>
    </Dialog>
  )
}
