import { useEffect, useState } from 'react'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

// A QR code (Wi-Fi join, phase 4), drawn as SVG by the lazily loaded
// `qrcode` package. The text it encodes is the caller's to show or hide;
// the image's accessible name says what scanning it does.

export function QRCode({ value, label, className }: { value: string; label: string; className?: string }) {
  const [svg, setSvg] = useState<string | null>(null)
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    let cancelled = false
    void import('qrcode')
      .then((qr) => qr.toString(value, { type: 'svg', margin: 1, errorCorrectionLevel: 'M' }))
      .then((text) => {
        if (!cancelled) setSvg(text)
      })
      .catch(() => {
        if (!cancelled) setFailed(true)
      })
    return () => {
      cancelled = true
    }
  }, [value])
  if (failed) return <p className="text-sm text-destructive">The QR code could not be drawn.</p>
  if (!svg) return <Skeleton className={cn('aspect-square w-56', className)} />
  return (
    <div
      role="img"
      aria-label={label}
      // The SVG comes from the qrcode library, not from user HTML.
      // oxlint-disable-next-line react/no-danger
      dangerouslySetInnerHTML={{ __html: svg }}
      className={cn('aspect-square w-56 rounded-lg bg-white p-2 text-black [&_svg]:size-full', className)}
    />
  )
}
