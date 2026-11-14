import { parseTime } from '@/lib/time'

// Certificate expiry tones (design §3.14): amber below 21 days, red below 14.

export type ExpiryTone = 'good' | 'warn' | 'crit' | 'idle'

export function daysLeft(notAfter: string | null | undefined, now: number): number | null {
  const date = parseTime(notAfter)
  if (!date) return null
  return Math.floor((date.getTime() - now) / 86400_000)
}

export function expiryTone(days: number | null): ExpiryTone {
  if (days === null) return 'idle'
  if (days < 14) return 'crit'
  if (days < 21) return 'warn'
  return 'good'
}

export function expiryText(days: number | null): string {
  if (days === null) return 'Unknown'
  if (days < 0) return `Expired ${-days} d ago`
  if (days === 0) return 'Expires today'
  return `${days} d left`
}
