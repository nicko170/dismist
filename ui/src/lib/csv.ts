// CSV export (phase 4, design §8.7): RFC 4180 quoting, a UTF-8 BOM so
// spreadsheet apps read non-ASCII names, CRLF line ends.

export type CsvColumn<T> = { header: string; value: (row: T) => string | number | boolean | null | undefined }

export function csvCell(value: string | number | boolean | null | undefined): string {
  if (value === null || value === undefined) return ''
  const text = String(value)
  // Leading = + - @ would run as a formula in a spreadsheet.
  const safe = /^[=+\-@\t\r]/.test(text) && typeof value === 'string' ? `'${text}` : text
  return /[",\r\n]/.test(safe) ? `"${safe.replaceAll('"', '""')}"` : safe
}

export function toCSV<T>(rows: readonly T[], columns: readonly CsvColumn<T>[]): string {
  const lines = [columns.map((c) => csvCell(c.header)).join(',')]
  for (const row of rows) lines.push(columns.map((c) => csvCell(c.value(row))).join(','))
  return lines.join('\r\n') + '\r\n'
}

/** Starts a download of `csv` as `filename`. */
export function downloadCSV(filename: string, csv: string) {
  const blob = new Blob(['﻿', csv], { type: 'text/csv;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  link.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}
