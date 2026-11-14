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

/** Typographic minus for negative numbers on screen ("−54"). */
export function formatSigned(value: number, digits = 0): string {
  const text = Math.abs(value).toFixed(digits)
  return value < 0 ? `\u2212${text}` : text
}

export function formatDbm(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value)) return '–'
  return `${formatSigned(Math.round(value))} dBm`
}

/** 0–100 as "31 %"; values under 10 keep one decimal ("4.5 %"). */
export function formatPercent(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value)) return '–'
  const digits = value !== 0 && Math.abs(value) < 10 && !Number.isInteger(value) ? 1 : 0
  return `${value.toFixed(digits)} %`
}

/**
 * Channel utilisation as the API reports it (a fraction from 0 to 1:
 * RadioView.util, util_parts, PlanActual.util, Neighbour.util,
 * SampleRadio.util) as "31 %".
 */
export function formatUtil(fraction: number | null | undefined): string {
  if (fraction == null || !Number.isFinite(fraction)) return '–'
  return `${Math.round(fraction * 100)} %`
}

/** Bits per second in SI steps: "18.1 Mbit/s". */
export function formatRate(bps: number | null | undefined): string {
  if (bps == null || !Number.isFinite(bps)) return '–'
  const units = ['bit/s', 'kbit/s', 'Mbit/s', 'Gbit/s']
  let value = Math.abs(bps)
  let unit = 0
  while (value >= 1000 && unit < units.length - 1) {
    value /= 1000
    unit++
  }
  return `${value.toFixed(unit === 0 || value >= 100 ? 0 : 1)} ${units[unit]}`
}
