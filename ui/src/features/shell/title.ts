import { useEffect, useSyncExternalStore } from 'react'
import { useMatches } from '@tanstack/react-router'

// Document titles (design §2.4): "{page} · {site} · Dismist", prefixed with
// "(n) " when n APs in the current site are offline. Routes name their page
// with staticData.title or a loader `crumb`; usePageTitle() overrides both
// for titles known only after data loads (an AP's name).

let override: string | null = null
const listeners = new Set<() => void>()

function setOverride(value: string | null) {
  override = value
  listeners.forEach((l) => l())
}

export function usePageTitle(title: string | null | undefined) {
  useEffect(() => {
    if (!title) return
    setOverride(title)
    return () => {
      if (override === title) setOverride(null)
    }
  }, [title])
}

function useOverride() {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l)
      return () => listeners.delete(l)
    },
    () => override,
    () => override,
  )
}

export type Crumb = { label: string; to: string }

/** Breadcrumbs from the matched routes: loader `crumb`, else staticData.crumb or title. */
export function useCrumbs(): Crumb[] {
  return useMatches({
    select: (matches) =>
      matches.flatMap((match) => {
        const loaderCrumb = (match.loaderData as { crumb?: unknown } | undefined)?.crumb
        const label =
          (typeof loaderCrumb === 'string' && loaderCrumb) || match.staticData?.crumb || match.staticData?.title
        return label ? [{ label, to: match.pathname }] : []
      }),
    structuralSharing: true,
  })
}

export function formatTitle(page: string | undefined, site: string | undefined, offline: number): string {
  const parts = [page, site, 'Dismist'].filter((part, index, all) => part && all.indexOf(part) === index)
  return `${offline > 0 ? `(${offline}) ` : ''}${parts.join(' · ')}`
}

export function usePageName(): string | undefined {
  const crumbs = useCrumbs()
  const fromOverride = useOverride()
  return fromOverride ?? crumbs.at(-1)?.label
}

export function useDocumentTitle(page: string | undefined, site: string | undefined, offline: number) {
  useEffect(() => {
    document.title = formatTitle(page, site, offline)
  }, [page, site, offline])
}
