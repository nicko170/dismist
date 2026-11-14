import { useEffect, useState } from 'react'

// The on-screen keyboard is "open" while a text control has focus and the
// visual viewport is below 75 % of the layout viewport (design §2.2). The
// bottom tabs hide then, and --kb-inset tells sticky bars how far up to sit.

const TEXT_INPUT = /^(text|search|email|url|tel|password|number|date|datetime-local|month|time|week)$/

export function isTextControl(element: Element | null): boolean {
  if (!element) return false
  if (element instanceof HTMLTextAreaElement) return !element.readOnly
  if (element instanceof HTMLInputElement) return TEXT_INPUT.test(element.type) && !element.readOnly
  return element instanceof HTMLElement && element.isContentEditable
}

export function keyboardOpen(win: Window = window): boolean {
  const viewport = win.visualViewport
  if (!viewport || !isTextControl(win.document.activeElement)) return false
  return viewport.height < win.innerHeight * 0.75
}

export function useKeyboardOpen(): boolean {
  const [open, setOpen] = useState(false)
  useEffect(() => {
    const viewport = window.visualViewport
    const update = () => {
      const next = keyboardOpen(window)
      setOpen(next)
      const inset = viewport ? Math.max(0, window.innerHeight - viewport.height - viewport.offsetTop) : 0
      document.documentElement.style.setProperty('--kb-inset', `${next ? inset : 0}px`)
      document.documentElement.toggleAttribute('data-keyboard-open', next)
    }
    viewport?.addEventListener('resize', update)
    viewport?.addEventListener('scroll', update)
    document.addEventListener('focusin', update)
    document.addEventListener('focusout', update)
    return () => {
      viewport?.removeEventListener('resize', update)
      viewport?.removeEventListener('scroll', update)
      document.removeEventListener('focusin', update)
      document.removeEventListener('focusout', update)
    }
  }, [])
  return open
}
