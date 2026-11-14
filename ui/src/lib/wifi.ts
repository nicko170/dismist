// The Wi-Fi join string phones read from a QR code (phase 4, design §3.8):
// WIFI:T:<WPA|nopass>;S:<ssid>;P:<passphrase>;H:<true>;; with \ ; , : and "
// escaped. WPA covers WPA2, WPA3 and the transition mode.

export function escapeWifi(value: string): string {
  return value.replace(/[\\;,:"]/g, (char) => `\\${char}`)
}

export function wifiQRPayload({ ssid, passphrase, hidden = false }: { ssid: string; passphrase?: string | null; hidden?: boolean }): string {
  const parts = [`T:${passphrase ? 'WPA' : 'nopass'}`, `S:${escapeWifi(ssid)}`]
  if (passphrase) parts.push(`P:${escapeWifi(passphrase)}`)
  if (hidden) parts.push('H:true')
  return `WIFI:${parts.join(';')};;`
}
