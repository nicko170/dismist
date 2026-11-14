import { useEffect, useState } from 'react'
import { Eye, Loader2 } from 'lucide-react'
import { describeError } from '@/api/errors'
import { revealNetwork, type RevealView, type WLANView } from '@/api/queries/networks'
import { Button } from '@/components/ui/button'
import { Dialog } from '@/features/settings/kit/primitives'

// Reveal passphrase and Wi-Fi QR (design §8.7, phase 4). The reveal call is
// admin-scope and audit-logged on the server, so nothing is fetched until the
// operator asks. The QR encoder (qrcode) loads lazily.

/** The standard Wi-Fi QR payload, escaping \ ; , : and " (used for open WLANs). */
export function wifiPayload(ssid: string, security: 'WPA' | 'nopass', psk = '', hidden = false): string {
  const esc = (v: string) => v.replace(/([\\;,:"])/g, '\\$1')
  return `WIFI:T:${security};S:${esc(ssid)};${security === 'WPA' ? `P:${esc(psk)};` : ''}${hidden ? 'H:true;' : ''};`
}

function useQRCode(text: string | null) {
  const [result, setResult] = useState<{ text: string; url: string } | null>(null)
  useEffect(() => {
    let cancelled = false
    if (!text) return
    void import('qrcode').then((QR) =>
      QR.toDataURL(text, { margin: 2, width: 512, errorCorrectionLevel: 'M' }).then((url) => {
        if (!cancelled) setResult({ text, url })
      }),
    )
    return () => {
      cancelled = true
    }
  }, [text])
  return text && result?.text === text ? result.url : null
}

export function WifiQRDialog({ siteId, view, open, onOpenChange }: { siteId: string; view: WLANView; open: boolean; onOpenChange: (open: boolean) => void }) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title={`Join ${view.ssid}`} description="Scan with a phone camera to join this WLAN.">
      {/* The dialog content unmounts when closed, so a reveal never outlives one opening. */}
      <QRBody siteId={siteId} view={view} />
    </Dialog>
  )
}

function QRBody({ siteId, view }: { siteId: string; view: WLANView }) {
  const [reveal, setReveal] = useState<RevealView | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [shown, setShown] = useState(false)
  const openNetwork = view.security === 'open'
  const payload = openNetwork ? wifiPayload(view.ssid, 'nopass', '', view.hidden) : reveal?.wifi_qr ?? null
  const qr = useQRCode(payload)

  async function load() {
    setLoading(true)
    setError(null)
    try {
      setReveal(await revealNetwork(siteId, view.id))
    } catch (e) {
      setError(describeError(e).message)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="grid justify-items-center gap-4">
      {!openNetwork && !reveal ? (
        <div className="grid gap-3 text-sm">
          <p>Showing the QR code reveals the passphrase. dismist records who revealed it and when.</p>
          <Button onClick={() => void load()} disabled={loading} className="justify-self-start">
            {loading ? <Loader2 className="motion-safe:animate-spin" /> : <Eye />} Reveal and show QR
          </Button>
          {error ? (
            <p role="alert" className="font-medium text-destructive">
              {error}
            </p>
          ) : null}
        </div>
      ) : null}
      {qr ? <img src={qr} alt={`QR code to join ${view.ssid}`} className="aspect-square w-full max-w-72 rounded-lg border bg-card p-2" /> : null}
      {reveal ? (
        <div className="grid w-full gap-1">
          <span className="text-sm font-medium">Passphrase</span>
          <div className="flex flex-wrap items-center gap-2">
            <span className="min-w-0 flex-1 rounded-md border bg-muted px-3 py-2 font-mono text-sm break-all">{shown ? reveal.psk : '•'.repeat(Math.min(reveal.psk.length, 20))}</span>
            <Button variant="outline" onClick={() => setShown((s) => !s)}>
              {shown ? 'Hide' : 'Show'}
            </Button>
            <Button variant="outline" onClick={() => void navigator.clipboard?.writeText(reveal.psk)}>
              Copy
            </Button>
          </div>
        </div>
      ) : null}
    </div>
  )
}
