import { useEffect } from 'react'
import { useRouter } from '@tanstack/react-router'

// Remembers the previous resolved location so a phone back chevron can use
// history back when it would land on the parent list (keeping its filters),
// and moves focus to the new page's H1 after each navigation (§2.4).

let previousPathname: string | null = null

export function getPreviousPathname() {
  return previousPathname
}

export function useNavigationEffects() {
  const router = useRouter()
  useEffect(
    () =>
      router.subscribe('onResolved', (event) => {
        previousPathname = event.fromLocation?.pathname ?? null
        if (!event.fromLocation || !event.pathChanged) return
        requestAnimationFrame(() => {
          const heading = document.querySelector<HTMLElement>('#main [data-page-title], #main h1')
          if (heading) {
            if (!heading.hasAttribute('tabindex')) heading.tabIndex = -1
            heading.focus({ preventScroll: true })
          }
        })
      }),
    [router],
  )
}
