import { useSyncExternalStore } from 'react'

// One shared clock ticks every 15 s and drives every relative time on screen
// (design §3.0 "Times"), so a page of timestamps re-renders once, together.

const TICK_MS = 15_000
let now = Date.now()
const listeners = new Set<() => void>()
let timer: ReturnType<typeof setInterval> | undefined

function subscribe(listener: () => void) {
  listeners.add(listener)
  if (!timer) {
    timer = setInterval(() => {
      now = Date.now()
      listeners.forEach((l) => l())
    }, TICK_MS)
  }
  return () => {
    listeners.delete(listener)
    if (!listeners.size && timer) {
      clearInterval(timer)
      timer = undefined
    }
  }
}

export function useNow(): number {
  return useSyncExternalStore(
    subscribe,
    () => now,
    () => now,
  )
}

/** Go's zero time and empty strings both mean "never". */
export function parseTime(value: string | null | undefined): Date | null {
  if (!value || value.startsWith('0001-')) return null
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? null : date
}

/** "just now", "12 s ago", "5 m ago", "3 h ago", "2 d ago"; future times read "in 5 m". */
export function formatRelative(date: Date, reference: number): string {
  const delta = Math.round((reference - date.getTime()) / 1000)
  const abs = Math.abs(delta)
  let text: string
  if (abs < 5) return 'just now'
  if (abs < 60) text = `${abs} s`
  else if (abs < 3600) text = `${Math.floor(abs / 60)} m`
  else if (abs < 86400) text = `${Math.floor(abs / 3600)} h`
  else text = `${Math.floor(abs / 86400)} d`
  return delta >= 0 ? `${text} ago` : `in ${text}`
}

const absolute = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'medium' })

export function formatAbsolute(date: Date): string {
  return absolute.format(date)
}
