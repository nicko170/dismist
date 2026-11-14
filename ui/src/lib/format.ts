// Number and size formatting shared by every screen. Units follow SI for
// rates and IEC-style 1024 steps for memory and storage, as the AP reports.

const integer = new Intl.NumberFormat(undefined, { maximumFractionDigits: 0 })

export function formatCount(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value)) return '–'
  return integer.format(value)
}

export function formatBytes(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value)) return '–'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let size = Math.abs(value)
  let unit = 0
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024
    unit++
  }
  const digits = unit === 0 || size >= 100 ? 0 : 1
  return `${value < 0 ? '-' : ''}${size.toFixed(digits)} ${units[unit]}`
}

/** "3 d 4 h", "12 m", "45 s": the two largest units. */
export function formatDuration(seconds: number | null | undefined): string {
  if (seconds == null || !Number.isFinite(seconds) || seconds < 0) return '–'
  const parts: [number, string][] = [
    [Math.floor(seconds / 86400), 'd'],
    [Math.floor((seconds % 86400) / 3600), 'h'],
    [Math.floor((seconds % 3600) / 60), 'm'],
    [Math.floor(seconds % 60), 's'],
  ]
  const first = parts.findIndex(([n]) => n > 0)
  if (first < 0) return '0 s'
  return parts
    .slice(first, first + 2)
    .filter(([n]) => n > 0)
    .map(([n, u]) => `${n} ${u}`)
    .join(' ')
}

/** UTF-8 byte length (SSIDs are limited to 32 bytes, not characters). */
export function utf8Length(value: string): number {
  return new TextEncoder().encode(value).length
}
