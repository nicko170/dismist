// CSV export of the rows a list currently shows (design §8.7, phase 4).
// RFC 4180 quoting; formula-leading cells are prefixed so spreadsheets do
// not evaluate them (CSV injection).

export function toCsv(header: string[], rows: (string | number | boolean | null | undefined)[][]): string {
  const cell = (value: string | number | boolean | null | undefined) => {
    let text = value === null || value === undefined ? '' : String(value)
    if (/^[=+\-@\t\r]/.test(text) && typeof value === 'string') text = `'${text}`
    return /[",\n\r]/.test(text) ? `"${text.replace(/"/g, '""')}"` : text
  }
  return [header, ...rows].map((row) => row.map(cell).join(',')).join('\r\n') + '\r\n'
}

export function downloadCsv(filename: string, csv: string) {
  const blob = new Blob([csv], { type: 'text/csv;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  window.setTimeout(() => URL.revokeObjectURL(url), 1000)
}
