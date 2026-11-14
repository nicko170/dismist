import { z } from 'zod'
import type { WLANView } from '@/api/queries/networks'
import { getOption, intMessage, leafSchema } from '@/features/settings/kit/options'
import { isHostOrIP, isIP, isPrintableASCII, isTimeRange } from '@/features/settings/kit/validators'
import { utf8Length } from '@/lib/format'
import { normaliseMac } from '@/lib/mac'
import { DAYS, type WlanForm } from './form'
import { isEnterprise, isPersonal, passphraseError } from './security'

// WLAN validation (design §3.9, §7.4): leaves from the option registry, plus
// the cross-field rules written by hand. The server runs the same rules; the
// client only gives early, inline feedback.

type Context = {
  create: boolean
  /** The stored WLAN, for passphrase-required and duplicate checks. */
  current?: Pick<WLANView, 'id' | 'security' | 'has_psk'>
  /** The site's other WLANs. */
  others: Pick<WLANView, 'id' | 'ssid' | 'bands' | 'enabled'>[]
}

const optionalInt = (key: string) => {
  const meta = getOption(key)
  return z.union([z.null(), z.literal(''), z.number({ error: intMessage(meta) })]).refine(
    (v) => v === null || v === '' || (Number.isInteger(v) && (meta.min == null || v >= meta.min) && (meta.max == null || v <= meta.max)),
    { error: intMessage(meta) },
  )
}

/** Accepts the raw text a number field holds, failing with the bounds message. */
const intOrText = (key: string, nullable = false) => {
  const meta = getOption(key)
  const base = leafSchema(key)
  return z.unknown().superRefine((value, ctx) => {
    if (nullable && (value === null || value === '')) return
    const result = base.safeParse(value)
    if (!result.success) ctx.addIssue({ code: 'custom', message: intMessage(meta) })
  })
}

const serverRow = z.object({
  host: z.string().trim().min(1, { error: 'Enter the server’s IP address or name.' }).refine(isHostOrIP, { error: 'Enter an IP address or a DNS name.' }),
  port: z.unknown().refine((v) => Number.isInteger(v) && (v as number) >= 1 && (v as number) <= 65535, { error: 'Port 1 to 65535.' }),
  secret: z.string(),
  has_secret: z.boolean(),
})

export function wlanSchema(ctx: Context) {
  return z
    .object({
      ssid: leafSchema('wlan.ssid', { required: true }),
      enabled: z.boolean(),
      security: leafSchema('wlan.security'),
      psk: z.string(),
      bands: z.array(z.enum(['2.4', '5'])).min(1, { error: 'Choose at least one band.' }),
      vlan: z.object({ tagged: z.boolean(), id: z.unknown() }),
      hidden: z.boolean(),
      isolation: leafSchema('wlan.isolation'),
      pmf: leafSchema('wlan.pmf'),
      sae_h2e_only: z.boolean(),
      transition_disable: z.boolean(),
      group_rekey_sec: intOrText('wlan.group_rekey_sec'),
      eapol_v1: z.boolean(),
      owe_ssid: leafSchema('wlan.owe_ssid'),
      roaming: z.object({
        fast: leafSchema('wlan.roaming.fast'),
        ap_key_cache: z.boolean(),
        neighbor_report: z.boolean(),
        bss_transition: z.boolean(),
        ap_steering: z.boolean(),
        bss_load: z.boolean(),
      }),
      band_steering: z.boolean(),
      rates: leafSchema('wlan.rates'),
      min_rssi: z.object({ b24: optionalInt('wlan.min_rssi'), b5: optionalInt('wlan.min_rssi') }),
      dtim: intOrText('wlan.dtim'),
      wmm_disabled: z.boolean(),
      uapsd_disabled: z.boolean(),
      disable_11ax: z.boolean(),
      disable_11nac: z.boolean(),
      broadcast: z.object({
        limit: z.boolean(),
        allow_mdns: z.boolean(),
        allow_ssdp: z.boolean(),
        allow_ipv6_nd: z.boolean(),
        arp_filter: z.boolean(),
        mcast_to_ucast: z.boolean(),
        wireless_bridge_dhcp: z.boolean(),
      }),
      dhcp_only: z.boolean(),
      dhcp_dns_only: z.boolean(),
      option82: z.object({ enabled: z.boolean(), circuit_id: leafSchema('wlan.option82.circuit_id') }),
      max_clients: intOrText('wlan.max_clients', true),
      idle_timeout_sec: intOrText('wlan.idle_timeout_sec'),
      rate_limit: z.object({
        wlan_up: optionalInt('wlan.rate_limit'),
        wlan_down: optionalInt('wlan.rate_limit'),
        client_up: optionalInt('wlan.rate_limit'),
        client_down: optionalInt('wlan.rate_limit'),
      }),
      qos_class: leafSchema('wlan.qos_class'),
      honour_blocklist: z.boolean(),
      radius: z.object({
        auth: z.array(serverRow),
        acct: z.array(serverRow),
        mac_auth: z.boolean(),
        acct_interim_sec: optionalInt('wlan.radius.acct_interim_sec'),
        nas_id: leafSchema('wlan.radius.nas_id'),
        nas_ip: z.string().refine((v) => !v || isIP(v), { error: 'Enter an IP address, or leave it empty.' }),
        dynamic_vlan: z.object({ enabled: z.boolean(), type: z.enum(['standard', 'airespace-interface-name']), vlan_ids: z.string() }),
        reauth_sec: optionalInt('wlan.radius.reauth_sec'),
        reauth_on_session_timeout: z.boolean(),
        coa: z.object({
          enabled: z.boolean(),
          servers: z.array(
            z.object({
              host: z.string().trim().min(1, { error: 'Enter the CoA client’s address.' }).refine(isHostOrIP, { error: 'Enter an IP address or a DNS name.' }),
              local_port: z.unknown().refine((v) => Number.isInteger(v) && (v as number) >= 1 && (v as number) <= 65535, { error: 'Port 1 to 65535.' }),
              secret: z.string(),
              has_secret: z.boolean(),
            }),
          ),
          event_timestamp_optional: z.boolean(),
        }),
      }),
      mpsk: z.object({
        enabled: z.boolean(),
        private: z.boolean(),
        keys: z.array(
          z.object({
            id: z.string(),
            name: z.string(),
            passphrase: z.string(),
            has_passphrase: z.boolean(),
            vlan_id: z.unknown(),
            mac: z.string(),
            expires: z.string(),
          }),
        ),
      }),
      schedule: z.object({ enabled: z.boolean(), days: z.record(z.string(), z.string()) }),
    })
    .superRefine((v, issue) => {
      const add = (path: (string | number)[], message: string) => issue.addIssue({ code: 'custom', path, message })
      const form = v as unknown as WlanForm

      // SSID duplicates among the site's enabled WLANs on overlapping bands.
      const duplicate = ctx.others.find(
        (o) => o.id !== ctx.current?.id && o.enabled && o.ssid === form.ssid && o.bands.some((b) => form.bands.includes(b)),
      )
      if (form.enabled && duplicate) add(['ssid'], 'Another enabled WLAN on the same band already uses this SSID.')

      // Passphrase by security mode (§3.9).
      if (isPersonal(form.security)) {
        const hadPassphrase = Boolean(ctx.current?.has_psk && ctx.current && isPersonal(ctx.current.security))
        if (!form.psk && (ctx.create || !hadPassphrase)) add(['psk'], 'Enter a passphrase.')
        if (form.psk) {
          const message = passphraseError(form.security, form.psk)
          if (message) add(['psk'], message)
        }
      }

      if (form.vlan.tagged) {
        const id = form.vlan.id
        if (!(Number.isInteger(id) && (id as number) >= 2 && (id as number) <= 4094)) add(['vlan', 'id'], 'Enter a VLAN from 2 to 4094.')
      }

      if (form.band_steering && form.bands.length < 2) add(['band_steering'], 'Band steering needs both bands.')

      if (isEnterprise(form.security)) {
        if (!form.radius.auth.length) add(['radius', 'auth'], 'Add at least one RADIUS server.')
        if (form.radius.auth.length > 2) add(['radius', 'auth'], 'At most two RADIUS servers.')
        form.radius.auth.forEach((row, i) => {
          if (!row.secret && !row.has_secret) add(['radius', 'auth', i, 'secret'], 'Enter the shared secret.')
        })
        form.radius.acct.forEach((row, i) => {
          if (!row.secret && !row.has_secret) add(['radius', 'acct', i, 'secret'], 'Enter the shared secret.')
        })
        if (form.radius.dynamic_vlan.enabled) {
          const ids = form.radius.dynamic_vlan.vlan_ids.split(/[\s,]+/).filter(Boolean)
          if (!ids.length) add(['radius', 'dynamic_vlan', 'vlan_ids'], 'Add at least one VLAN.')
          else if (!ids.every((x) => /^\d+$/.test(x) && Number(x) >= 2 && Number(x) <= 4094)) add(['radius', 'dynamic_vlan', 'vlan_ids'], 'VLANs are numbers from 2 to 4094, separated by commas.')
        }
        form.radius.coa.servers.forEach((row, i) => {
          if (form.radius.coa.enabled && !row.secret && !row.has_secret) add(['radius', 'coa', 'servers', i, 'secret'], 'Enter the shared secret.')
        })
      }
      if (form.radius.mac_auth && !form.radius.auth.length) add(['radius', 'mac_auth'], 'MAC authentication needs a RADIUS server.')

      if (form.mpsk.enabled) {
        if (form.security !== 'wpa2_personal') add(['mpsk', 'enabled'], 'Per-device passphrases need WPA2-Personal.')
        if (form.mpsk.keys.length > 32) add(['mpsk', 'keys'], 'At most 32 keys.')
        const names = new Set<string>()
        const phrases = new Set<string>()
        form.mpsk.keys.forEach((key, i) => {
          const name = key.name.trim()
          if (!name || name.length > 32) add(['mpsk', 'keys', i, 'name'], 'A name of 1 to 32 characters.')
          else if (names.has(name)) add(['mpsk', 'keys', i, 'name'], 'Each key needs its own name.')
          names.add(name)
          if (!key.passphrase && !key.has_passphrase) add(['mpsk', 'keys', i, 'passphrase'], 'Enter a passphrase.')
          if (key.passphrase) {
            if (key.passphrase.length < 8 || key.passphrase.length > 63 || !isPrintableASCII(key.passphrase)) add(['mpsk', 'keys', i, 'passphrase'], '8 to 63 printable characters.')
            else if (phrases.has(key.passphrase) || key.passphrase === form.psk) add(['mpsk', 'keys', i, 'passphrase'], 'Each key needs its own passphrase, different from the main one.')
            phrases.add(key.passphrase)
          }
          if (key.vlan_id !== null && key.vlan_id !== '' && !(Number.isInteger(key.vlan_id) && (key.vlan_id as number) >= 2 && (key.vlan_id as number) <= 4094)) add(['mpsk', 'keys', i, 'vlan_id'], 'VLAN 2 to 4094, or empty for the WLAN’s VLAN.')
          if (key.mac && !normaliseMac(key.mac)) add(['mpsk', 'keys', i, 'mac'], 'Enter a MAC address, or leave it empty.')
        })
      }

      if (form.schedule.enabled) {
        for (const day of DAYS) {
          const ranges = form.schedule.days[day].split(',').map((r) => r.trim()).filter(Boolean)
          if (!ranges.every(isTimeRange)) add(['schedule', 'days', day], 'Use ranges like 08:00-17:30, separated by commas.')
        }
      }

      if (form.security === 'owe_transition' && form.owe_ssid && utf8Length(form.owe_ssid) > 32) add(['owe_ssid'], 'At most 32 bytes.')
    })
}

/** A warning (not an error): another WLAN's SSID differs only in case. */
export function caseOnlyDuplicate(ssid: string, ctx: Pick<Context, 'others' | 'current'>): string | null {
  const match = ctx.others.find((o) => o.id !== ctx.current?.id && o.ssid !== ssid && o.ssid.toLowerCase() === ssid.toLowerCase())
  return match ? `“${match.ssid}” differs only in case. Some clients treat them as the same network.` : null
}
