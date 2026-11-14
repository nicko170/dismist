import type { EnumValueMeta, OptionMeta, OptionScope } from '@/gen/options.gen'

// PROVISIONAL option metadata, transcribed from design §3.6 and §3.9 to §3.11
// so F's forms run before workstream A's registry generator fills
// gen/options.gen.ts. `getOption()` always prefers the generated entry; once
// A's registry declares every key below, delete this file. The keys are the
// API paths prefixed with the scope ("wlan.roaming.fast",
// "site.rf.band_5.width"), which is the naming F asks A to use.

type Extra = Partial<Omit<OptionMeta, 'key' | 'scope' | 'label' | 'control' | 'type'>>

function opt(scope: OptionScope, path: string, label: string, control: string, type: string, extra: Extra = {}): OptionMeta {
  const confidence = extra.confidence ?? 'confirmed'
  return {
    key: `${scope}.${path}`,
    scope,
    label,
    help: '',
    control,
    type,
    enum: [],
    min: null,
    max: null,
    maxBytes: null,
    unit: '',
    default: null,
    tier: 'basic',
    confidence,
    disruption: scope === 'wlan' ? 'wlan_restart' : 'none',
    secret: false,
    phase: 2,
    firmwarePaths: [],
    catalogue: '',
    visibleWhen: [],
    ...extra,
    dismistOnly: extra.dismistOnly ?? false,
    experimental: confidence === 'needs-live-test',
  }
}

function value(v: string, label: string, extra: Partial<EnumValueMeta> = {}): EnumValueMeta {
  return { value: v, label, help: '', experimental: false, phase: 2, ...extra }
}

const exp = { confidence: 'needs-live-test' as const }
const adv = { tier: 'advanced' as const }

const PERSONAL = ['wpa2_personal', 'wpa2_wpa3_personal', 'wpa3_personal']
const ENTERPRISE = ['wpa2_enterprise', 'wpa2_wpa3_enterprise', 'wpa3_enterprise']
const WPA3 = ['wpa2_wpa3_personal', 'wpa3_personal', 'wpa2_wpa3_enterprise', 'wpa3_enterprise', 'owe']

// ---- WLAN (§3.9) ----
const wlan: OptionMeta[] = [
  opt('wlan', 'ssid', 'SSID', 'ssid', 'string', { maxBytes: 32, help: 'The network name clients see. Up to 32 bytes (some characters take more than one).', firmwarePaths: ['WLANs[].SSID'], catalogue: 'wlans.md §5.1' }),
  opt('wlan', 'enabled', 'Enabled', 'switch', 'bool', { default: true, firmwarePaths: ['WLANs[].Enabled'] }),
  opt('wlan', 'security', 'Security', 'security', 'string', {
    default: 'wpa2_wpa3_personal',
    firmwarePaths: ['WLANs[].Auth.Type', 'WLANs[].Auth.AKM', 'WLANs[].Dot11w', 'WLANs[].Auth.Pairwise', 'WLANs[].OWE'],
    catalogue: 'wlans.md §7.2',
    enum: [
      value('wpa2_wpa3_personal', 'WPA2/WPA3-Personal', { help: 'Recommended. WPA3 for new devices, WPA2 for older ones.' }),
      value('wpa2_personal', 'WPA2-Personal', { help: 'For devices that cannot join WPA3 networks.' }),
      value('wpa3_personal', 'WPA3-Personal', { help: 'WPA3 only. Older devices cannot join.' }),
      value('open', 'Open (no encryption)', { help: 'Anyone can join and traffic is not encrypted.' }),
      value('owe', 'Enhanced Open (OWE)', { help: 'No password, but traffic is encrypted. Needs recent devices.', experimental: true }),
      value('owe_transition', 'Enhanced Open + Open (transition)', { help: 'An OWE network plus an open companion. Counts as two WLANs.', experimental: true }),
      value('wpa2_enterprise', 'WPA2-Enterprise', { help: 'Sign in with RADIUS (802.1X).', phase: 4 }),
      value('wpa2_wpa3_enterprise', 'WPA2/WPA3-Enterprise', { help: 'RADIUS, with WPA3 for capable devices.', phase: 4 }),
      value('wpa3_enterprise', 'WPA3-Enterprise', { help: 'RADIUS, WPA3 only.', phase: 4 }),
    ],
  }),
  opt('wlan', 'psk', 'Passphrase', 'password', 'string', {
    secret: true,
    help: 'WPA2: 8 to 63 printable characters, or exactly 64 hex digits. WPA3 and WPA2/WPA3: 8 to 63 printable characters.',
    visibleWhen: [{ key: 'wlan.security', in: PERSONAL }],
    firmwarePaths: ['WLANs[].Auth.PSK'],
    catalogue: 'wlans.md §7.3',
  }),
  opt('wlan', 'bands', 'Bands', 'chips', '[]string', {
    default: ['2.4', '5'],
    enum: [value('2.4', '2.4 GHz'), value('5', '5 GHz')],
    firmwarePaths: ['WLANs[].Bands', 'WLANs[].RateSets', 'WLANs[].BandConfigs'],
  }),
  opt('wlan', 'vlan_id', 'VLAN', 'vlan', 'int', {
    default: 1,
    min: 1,
    max: 4094,
    help: 'Untagged puts clients on the switch port’s native VLAN. Tagged sends client traffic with an 802.1Q tag; the switch port must trunk that VLAN.',
    firmwarePaths: ['WLANs[].Vlans'],
  }),
  opt('wlan', 'hidden', 'Hidden SSID', 'switch', 'bool', { default: false, confidence: 'likely', help: 'The AP stops announcing the name. Clients must know it to join.', firmwarePaths: ['WLANs[].Hidden'] }),
  opt('wlan', 'isolation', 'Client isolation', 'select', 'string', {
    default: 'none',
    confidence: 'likely',
    enum: [value('none', 'None'), value('clients', 'Isolate clients'), value('clients_and_lan', 'Isolate clients and LAN')],
    firmwarePaths: ['WLANs[].Isolation', 'WLANs[].NetIsolation'],
  }),
  opt('wlan', 'radius.auth', 'RADIUS servers', 'radius_servers', 'object', {
    confidence: 'likely',
    phase: 4,
    max: 2,
    help: 'One or two authentication servers. Secrets are write-only.',
    visibleWhen: [{ key: 'wlan.security', in: ENTERPRISE }],
    firmwarePaths: ['WLANs[].Auth_servers'],
  }),
  // Advanced: Security
  opt('wlan', 'pmf', 'Protected management frames (802.11w)', 'select', 'string', {
    ...adv,
    default: 'optional',
    enum: [value('disabled', 'Disabled'), value('optional', 'Optional'), value('required', 'Required')],
    visibleWhen: [{ key: 'wlan.security', in: ['wpa2_personal', 'wpa2_enterprise'] }],
    firmwarePaths: ['WLANs[].Dot11w'],
  }),
  opt('wlan', 'sae_h2e_only', 'SAE hash-to-element only', 'switch', 'bool', {
    ...adv,
    default: false,
    confidence: 'likely',
    help: 'Older WPA3 clients can’t join.',
    visibleWhen: [{ key: 'wlan.security', in: ['wpa2_wpa3_personal', 'wpa3_personal'] }],
    firmwarePaths: ['WLANs[].Auth.SAEHash2ElementOnly'],
  }),
  opt('wlan', 'transition_disable', 'WPA3 transition disable', 'switch', 'bool', {
    ...adv,
    default: false,
    confidence: 'likely',
    help: 'Tells WPA3 clients never to fall back to WPA2 on this network. The firmware then forces H2E.',
    visibleWhen: [{ key: 'wlan.security', in: ['wpa2_wpa3_personal', 'wpa2_wpa3_enterprise'] }],
    firmwarePaths: ['WLANs[].Auth.TransitionDisableIndication'],
  }),
  opt('wlan', 'group_rekey_sec', 'Group key rekey', 'number', 'int', { ...adv, default: 86400, min: 60, max: 86400, unit: 's', confidence: 'likely', firmwarePaths: ['WLANs[].Auth.GroupRekeyIntervalSec', 'WLANs[].Auth.GroupMasterRekeyIntervalSec'] }),
  opt('wlan', 'eapol_v1', 'EAPOL version 1', 'switch', 'bool', { ...adv, default: false, confidence: 'likely', help: 'For pre-2004 clients.', firmwarePaths: ['WLANs[].Auth.EAPOLVersion'] }),
  opt('wlan', 'radius.mac_auth', 'MAC authentication', 'switch', 'bool', { ...adv, default: false, confidence: 'likely', phase: 4, help: 'Checks each client’s MAC address with the RADIUS servers.', firmwarePaths: ['WLANs[].MacAuth.Enabled'] }),
  opt('wlan', 'owe_ssid', 'OWE companion SSID', 'text', 'string', { ...adv, ...exp, maxBytes: 32, default: '', visibleWhen: [{ key: 'wlan.security', in: ['owe_transition'] }], firmwarePaths: ['WLANs[].OWE.SSID'] }),
  // Advanced: Roaming
  opt('wlan', 'roaming.fast', 'Fast roaming', 'select', 'string', {
    ...adv,
    default: 'none',
    enum: [value('none', 'None'), value('okc', 'OKC', { experimental: true }), value('11r', '802.11r', { experimental: true })],
    firmwarePaths: ['WLANs[].RoamMode', 'WLANs[].MDID', 'WLANs[].Auth.AKM'],
    catalogue: 'wlans.md §7.4',
  }),
  opt('wlan', 'roaming.ap_key_cache', 'AP-to-AP key caching', 'switch', 'bool', {
    ...adv,
    ...exp,
    default: false,
    phase: 3,
    help: 'Lets clients roam between your APs without the Mist cloud’s key cache.',
    visibleWhen: [{ key: 'wlan.roaming.fast', in: ['okc', '11r'] }],
    firmwarePaths: ['WLANs[].KeyCache.LocalAPkeyCache', 'WLANs[].KeyCache.KeyEncryptKey'],
  }),
  opt('wlan', 'roaming.neighbor_report', '802.11k neighbour reports', 'switch', 'bool', { ...adv, default: true, firmwarePaths: ['WLANs[].WNM.NeighborReport'] }),
  opt('wlan', 'roaming.bss_transition', '802.11v BSS transition', 'switch', 'bool', { ...adv, default: true, firmwarePaths: ['WLANs[].WNM.BSSTransitionManagement.Passive'] }),
  opt('wlan', 'roaming.ap_steering', 'AP-initiated steering (BTM)', 'switch', 'bool', { ...adv, default: false, confidence: 'likely', firmwarePaths: ['WLANs[].WNM.BSSTransitionManagement.Active'] }),
  opt('wlan', 'roaming.bss_load', 'BSS Load element', 'switch', 'bool', { ...adv, default: true, firmwarePaths: ['WLANs[].WNM.BSSLoad'] }),
  // Advanced: Radio and rates
  opt('wlan', 'band_steering', 'Band steering (prefer 5 GHz)', 'switch', 'bool', { ...adv, default: false, confidence: 'likely', help: 'Nudges dual-band clients onto 5 GHz. Needs both bands.', firmwarePaths: ['WLANs[].BandConfigs[].Steer'] }),
  opt('wlan', 'rates', 'Data rate profile', 'select', 'string', {
    ...adv,
    default: 'compatible',
    enum: [
      value('compatible', 'Compatible'),
      value('no_legacy', 'No legacy (no 802.11b)'),
      value('high_density', 'High density', { experimental: true }),
      value('legacy_only', 'Legacy only', { experimental: true }),
    ],
    firmwarePaths: ['WLANs[].RateSets'],
  }),
  opt('wlan', 'min_rssi', 'Minimum client signal', 'band_rssi', 'object', { ...adv, ...exp, min: -95, max: -50, unit: 'dBm', help: 'Refuse clients weaker than this on each band. Empty means off.', firmwarePaths: ['WLANs[].BandConfigs[].GeoFence'] }),
  opt('wlan', 'dtim', 'DTIM period', 'number', 'int', { ...adv, default: 2, min: 1, max: 255, firmwarePaths: ['WLANs[].DTIM.Period'] }),
  opt('wlan', 'wmm_disabled', 'Disable WMM', 'switch', 'bool', { ...adv, default: false, confidence: 'likely', help: 'Breaks 802.11n/ac/ax rates.', firmwarePaths: ['WLANs[].WME.Disabled'] }),
  opt('wlan', 'uapsd_disabled', 'Disable U-APSD', 'switch', 'bool', { ...adv, default: false, confidence: 'likely', firmwarePaths: ['WLANs[].WME.DisableApsd'] }),
  opt('wlan', 'disable_11ax', 'Disable 802.11ax', 'switch', 'bool', { ...adv, default: false, confidence: 'likely', firmwarePaths: ['WLANs[].DisabledIEs.DisableHeIes'] }),
  opt('wlan', 'disable_11nac', 'Disable 802.11n/ac', 'switch', 'bool', { ...adv, default: false, confidence: 'likely', firmwarePaths: ['WLANs[].DisabledIEs.DisableHTIes', 'WLANs[].DisabledIEs.DisableVHTIes'] }),
  // Advanced: Network
  opt('wlan', 'broadcast.limit', 'Limit broadcast and multicast', 'switch', 'bool', { ...adv, default: false, confidence: 'likely', firmwarePaths: ['WLANs[].BcastFilter.Mcast'] }),
  opt('wlan', 'broadcast.allow_mdns', 'Allow mDNS', 'switch', 'bool', { ...adv, default: false, confidence: 'likely', visibleWhen: [{ key: 'wlan.broadcast.limit', in: ['true'] }], firmwarePaths: ['WLANs[].BcastFilter.Mdns_allow'] }),
  opt('wlan', 'broadcast.allow_ssdp', 'Allow SSDP', 'switch', 'bool', { ...adv, default: false, confidence: 'likely', visibleWhen: [{ key: 'wlan.broadcast.limit', in: ['true'] }], firmwarePaths: ['WLANs[].BcastFilter.Ssdp_allow'] }),
  opt('wlan', 'broadcast.allow_ipv6_nd', 'Allow IPv6 neighbour discovery', 'switch', 'bool', { ...adv, default: true, confidence: 'likely', visibleWhen: [{ key: 'wlan.broadcast.limit', in: ['true'] }], firmwarePaths: ['WLANs[].BcastFilter.AllowICMPv6NeighborDiscovery'] }),
  opt('wlan', 'broadcast.arp_filter', 'ARP filter', 'switch', 'bool', { ...adv, default: false, confidence: 'likely', firmwarePaths: ['WLANs[].BcastFilter.Arp'] }),
  opt('wlan', 'broadcast.mcast_to_ucast', 'Multicast to unicast', 'switch', 'bool', { ...adv, ...exp, default: false, firmwarePaths: ['WLANs[].Bcast2Ucast.ForcedEnabled'] }),
  opt('wlan', 'broadcast.wireless_bridge_dhcp', 'DHCP for wireless bridges', 'switch', 'bool', { ...adv, default: false, confidence: 'likely', firmwarePaths: ['WLANs[].BcastFilter.AllowDHCPtoWirelessBridge'] }),
  opt('wlan', 'dhcp_only', 'Only DHCP-addressed clients', 'switch', 'bool', { ...adv, default: false, confidence: 'likely', firmwarePaths: ['WLANs[].NoStaticIP'] }),
  opt('wlan', 'dhcp_dns_only', 'Only DHCP-provided DNS', 'switch', 'bool', { ...adv, default: false, confidence: 'likely', firmwarePaths: ['WLANs[].RestrictDNS.FromDHCP'] }),
  opt('wlan', 'option82.enabled', 'DHCP Option 82', 'switch', 'bool', { ...adv, default: false, confidence: 'likely', phase: 4, firmwarePaths: ['WLANs[].DhcpOption82'] }),
  opt('wlan', 'option82.circuit_id', 'Circuit ID', 'text', 'string', { ...adv, default: '', confidence: 'likely', phase: 4, maxBytes: 63, help: '{SSID} and {AP_MAC} are replaced for each AP.', visibleWhen: [{ key: 'wlan.option82.enabled', in: ['true'] }], firmwarePaths: ['WLANs[].DhcpOption82Data'] }),
  // Advanced: Limits and clients
  opt('wlan', 'max_clients', 'Max clients', 'number', 'int', { ...adv, min: 2, max: 512, help: 'Empty means no limit (512).', firmwarePaths: ['WLANs[].MaxStaAllowed'] }),
  opt('wlan', 'idle_timeout_sec', 'Idle timeout', 'number', 'int', { ...adv, default: 1800, min: 60, max: 86400, unit: 's', firmwarePaths: ['WLANs[].WCInactivity.TimeoutSec'] }),
  opt('wlan', 'rate_limit', 'Rate limits', 'rate_limit', 'object', { ...adv, ...exp, min: 1, max: 999000, unit: 'kbps', help: 'Empty means unlimited.', firmwarePaths: ['WLANs[].RateLimit'] }),
  opt('wlan', 'qos_class', 'QoS override', 'select', 'string', {
    ...adv,
    ...exp,
    default: 'none',
    enum: [value('none', 'None'), value('best_effort', 'Best effort'), value('background', 'Background'), value('video', 'Video'), value('voice', 'Voice')],
    firmwarePaths: ['WLANs[].QOS'],
  }),
  opt('wlan', 'honour_blocklist', 'Honour blocked clients', 'switch', 'bool', { ...adv, default: true, confidence: 'likely', help: 'Refuses the site’s blocked clients on this WLAN.', firmwarePaths: ['WLANs[].BlackList.Enabled'] }),
  // Advanced: RADIUS (phase 4)
  opt('wlan', 'radius.acct', 'Accounting servers', 'radius_servers', 'object', { ...adv, confidence: 'likely', phase: 4, max: 2, firmwarePaths: ['WLANs[].Acct_servers'] }),
  opt('wlan', 'radius.acct_interim_sec', 'Interim accounting', 'number', 'int', { ...adv, confidence: 'likely', phase: 4, min: 60, max: 65535, unit: 's', help: 'Empty uses the server’s value.', firmwarePaths: ['WLANs[].Acct_Interim_Interval'] }),
  opt('wlan', 'radius.nas_id', 'NAS-Identifier', 'text', 'string', { ...adv, confidence: 'likely', phase: 4, maxBytes: 48, default: '', help: 'Empty lets the AP choose.', firmwarePaths: ['WLANs[].Nas_ID'] }),
  opt('wlan', 'radius.nas_ip', 'NAS-IP-Address', 'ip', 'string', { ...adv, confidence: 'likely', phase: 4, default: '', firmwarePaths: ['WLANs[].NasIPAddress'] }),
  opt('wlan', 'radius.dynamic_vlan', 'Dynamic VLAN', 'dynamic_vlan', 'object', { ...adv, ...exp, phase: 4, firmwarePaths: ['WLANs[].Dynamic_vlan'] }),
  opt('wlan', 'radius.reauth_sec', 'Periodic re-authentication', 'number', 'int', { ...adv, confidence: 'likely', phase: 4, min: 300, max: 86400, unit: 's', help: 'Empty means off.', firmwarePaths: ['WLANs[].Auth.EapReauthIntervalSec'] }),
  opt('wlan', 'radius.reauth_on_session_timeout', 'Re-authenticate when Session-Timeout expires', 'switch', 'bool', { ...adv, confidence: 'likely', phase: 4, default: false, firmwarePaths: ['WLANs[].Auth.ReauthOnSessionTimeout'] }),
  opt('wlan', 'radius.coa', 'Change of Authorisation', 'coa', 'object', { ...adv, ...exp, phase: 4, firmwarePaths: ['WLANs[].COA'] }),
  // Multi-PSK, schedule (phase 4, experimental)
  opt('wlan', 'mpsk', 'Per-device passphrases', 'mpsk', 'object', { ...adv, ...exp, phase: 4, max: 32, help: 'WPA2-Personal only, because WPA3 needs the plain passphrase.', firmwarePaths: ['WLANs[].Auth.MultiPSK'] }),
  opt('wlan', 'schedule', 'Schedule', 'schedule', 'object', { ...adv, ...exp, phase: 4, help: 'The WLAN is off outside these hours (site timezone). Needs config push “notify”.', firmwarePaths: ['WLANs[].BSS'] }),
]

// ---- Site (§3.11) ----
const site: OptionMeta[] = [
  opt('site', 'name', 'Name', 'text', 'string', { min: 1, max: 64, help: 'Only dismist uses the site name.' }),
  opt('site', 'country', 'Country', 'country', 'string', {
    disruption: 'all_radios_bounce',
    help: 'Channels and power limits come from the regulatory revision (ccrev), not from the country code.',
    enum: [value('AU', 'Australia (AU/890)')],
    firmwarePaths: ['Country', 'Monitor.CCRev', 'Radios[].CCRev', 'Radios[].CCRevSP'],
  }),
  opt('site', 'timezone', 'Timezone', 'timezone', 'string', { help: 'Used for schedules and the history.' }),
  opt('site', 'experimental', 'Experimental options', 'switch', 'bool', { default: false, help: 'Shows options that are not yet verified on AP33 0.15. Test them on one AP first.' }),
  opt('site', 'ssh.enabled', 'SSH access', 'switch', 'bool', { help: 'Root SSH on every AP of the site. Needs at least one key.', firmwarePaths: ['SSH.Disabled', 'SSH.External'] }),
  opt('site', 'ssh.keys', 'Root SSH keys', 'ssh_keys', '[]string', { max: 32, help: 'OpenSSH public keys, one per line. Comments are kept.', firmwarePaths: ['SSH.AuthorizedKeys', 'RootAuthorizedSSHKeys'] }),
  opt('site', 'ssh.readonly_keys', 'Read-only stats keys', 'ssh_keys', '[]string', { ...exp, max: 32, help: 'Keys that can only read AP statistics.', firmwarePaths: ['SSH.RestrictedKeys'] }),
  opt('site', 'syslog', 'Remote syslog', 'syslog', 'object', { ...exp, max: 16, firmwarePaths: ['Syslogs'] }),
  opt('site', 'management.ntp', 'NTP servers', 'list', '[]string', { confidence: 'likely', max: 6, help: 'Empty uses DHCP, then pool.ntp.org.', firmwarePaths: ['NTP'] }),
  opt('site', 'management.dns', 'DNS servers', 'list', '[]string', { confidence: 'likely', max: 3, help: 'Overrides DNS from DHCP on every AP.', firmwarePaths: ['DNS'] }),
  opt('site', 'management.vlan_id', 'Management VLAN', 'vlan', 'int', { ...exp, default: 1, min: 1, max: 4094, disruption: 'stranding_risk', help: 'A wrong VLAN strands every AP. Needs config auto-recovery.', firmwarePaths: ['SVIs[].Vlan_ID', 'SwitchPorts[].Vlans'] }),
  opt('site', 'ap_defaults.led_brightness', 'LED brightness', 'slider', 'int', { default: 255, min: 0, max: 255, firmwarePaths: ['LED.Brightness'] }),
  opt('site', 'ap_defaults.hostname_from_name', 'Use the AP name as hostname and DHCP hostname', 'switch', 'bool', { default: true, help: 'Your router then shows each AP by name.', firmwarePaths: ['Hostname', 'SVIs[].DHCP.Hostname'] }),
  opt('site', 'ap_defaults.keep_wlans_up_on_uplink_down', 'Keep WLANs up when the uplink is down', 'switch', 'bool', { default: false, confidence: 'likely', firmwarePaths: ['Monitor.EthUplink.Enabled'] }),
  opt('site', 'ap_defaults.poe_constrained', 'When PoE power is insufficient', 'select', 'string', {
    default: 'keep_radios',
    confidence: 'likely',
    enum: [value('keep_radios', 'Keep radios on'), value('disable_radios', 'Disable radios to fit')],
    firmwarePaths: ['PoePwrConstrained.EnableRadios'],
  }),
  opt('site', 'ap_defaults.wired_client_stats', 'Report wired clients on eth1', 'switch', 'bool', { default: false, confidence: 'likely', firmwarePaths: ['EnableWiredClientStats'] }),
  opt('site', 'ap_defaults.eth1', 'eth1 default mode', 'eth1', 'object', { ...exp, phase: 3, disruption: 'stranding_risk', firmwarePaths: ['SwitchPorts[].Vlans'] }),
  opt('site', 'ap_defaults.config_recovery', 'Config auto-recovery', 'recovery', 'object', { ...exp, min: 1, max: 60, unit: 'min', help: 'The AP reverts to its last good config when it loses the controller after a change.', firmwarePaths: ['APRecovery'] }),
  opt('site', 'ap_defaults.ble_enabled', 'Bluetooth (BLE) radio', 'switch', 'bool', { default: true, confidence: 'likely', help: 'dismist does not use Bluetooth; turning it off saves a little power.', firmwarePaths: ['BLE.Disabled'] }),
  opt('site', 'protection.blocked_clients', 'Blocked clients', 'mac_list', 'object', { confidence: 'likely', help: 'Paste MACs in any format; they are stored lower-case and dashed.', firmwarePaths: ['BlackList.WCMacs'] }),
  opt('site', 'protection.proxy_arp', 'Proxy ARP', 'switch', 'bool', { default: true, confidence: 'likely', firmwarePaths: ['ProxyArp.Enabled'] }),
  opt('site', 'protection.arp_spoof_check', 'ARP spoof check', 'switch', 'bool', { default: false, confidence: 'likely', help: 'Needs proxy ARP.', firmwarePaths: ['ProxyArp.Disable_ReqSpoofCheck', 'ProxyArp.Disable_RespSpoofCheck'] }),
  opt('site', 'protection.penalty_box', 'Repeated auth-failure penalty box', 'penalty_box', 'object', { ...exp, phase: 3, firmwarePaths: ['WIDS'] }),
  opt('site', 'protection.dhcp_fingerprinting', 'DHCP fingerprinting', 'switch', 'bool', { default: false, confidence: 'likely', help: 'Sends client DHCP fingerprints in telemetry. dismist does not use them yet.', firmwarePaths: ['Fingerprint.DHCPPayload'] }),
]

// ---- Site RF profile (§3.10) ----
function bandOptions(band: '24' | '5'): OptionMeta[] {
  const b = `band_${band}`
  const label = band === '24' ? '2.4 GHz' : '5 GHz'
  const radio = band === '24' ? 'Radios[2.4]' : 'Radios[5]'
  return [
    opt('site.rf', `${b}.enabled`, `${label} radio`, 'switch', 'bool', { default: true, disruption: 'radio_bounce', firmwarePaths: [`${radio}.Disabled`] }),
    opt('site.rf', `${b}.width`, 'Channel width', 'select', 'int', {
      default: band === '24' ? 20 : 80,
      unit: 'MHz',
      disruption: 'radio_bounce',
      enum: band === '24' ? [value('20', '20 MHz')] : [value('20', '20 MHz'), value('40', '40 MHz'), value('80', '80 MHz')],
      help: band === '24' ? '40 MHz on 2.4 GHz is only available through the expert patch.' : 'AP33 has no 160 MHz channel under AU/890.',
      firmwarePaths: [`${radio}.Width`, `${radio}.RRM.Width`],
    }),
    opt('site.rf', `${b}.channels`, 'Channels for Auto', 'channels', '[]int', {
      default: band === '24' ? [1, 6, 11] : [36, 149],
      confidence: band === '24' ? 'confirmed' : 'likely',
      help: 'The planner picks from these. They are never sent to the AP.',
    }),
    opt('site.rf', `${b}.power`, 'Transmit power', 'power', 'object', {
      default: band === '24' ? { mode: 'auto', min: 8, max: 15 } : { mode: 'auto', min: 12, max: 20 },
      min: 1,
      max: 25,
      unit: 'dBm',
      confidence: 'likely',
      disruption: 'radio_bounce',
      help: '1 to 25 dBm. Above 20 dBm is rarely useful indoors.',
      firmwarePaths: [`${radio}.Power`, `${radio}.RRM.Power`],
    }),
    ...(band === '24'
      ? [
          opt('site.rf', `${b}.preamble`, 'Preamble', 'select', 'string', {
            ...adv,
            default: 'short',
            disruption: 'radio_bounce',
            enum: [value('auto', 'Auto'), value('short', 'Short'), value('long', 'Long')],
            firmwarePaths: [`${radio}.PreambleLength`],
          }),
        ]
      : []),
    opt('site.rf', `${b}.advanced.taf`, 'Airtime fairness', 'select', 'string', { ...adv, default: 'default', disruption: 'radio_bounce', enum: [value('default', 'Default'), value('off', 'Off'), value('on', 'On')], firmwarePaths: [`${radio}.TAFMode`] }),
    opt('site.rf', `${b}.advanced.twt`, 'Target wake time (TWT)', 'switch', 'bool', { ...adv, default: true, confidence: 'likely', disruption: 'radio_bounce', firmwarePaths: [`${radio}.TWTDisabled`] }),
    opt('site.rf', `${b}.advanced.antenna`, 'Antenna mode', 'select', 'string', {
      ...adv,
      default: 'default',
      confidence: 'likely',
      disruption: 'radio_bounce',
      enum:
        band === '24'
          ? [value('default', 'Default'), value('2x2', '2x2'), value('1x1', '1x1')]
          : [value('default', 'Default'), value('4x4', '4x4'), value('3x3', '3x3'), value('2x2', '2x2'), value('1x1', '1x1')],
      firmwarePaths: [`${radio}.ChainSelectMode`],
    }),
    opt('site.rf', `${b}.advanced.min_probe_rssi`, 'Minimum probe-response signal', 'number', 'int', { ...adv, confidence: 'likely', min: -95, max: -50, unit: 'dBm', disruption: 'radio_bounce', help: 'Ignore probes weaker than this. Empty means off.', firmwarePaths: [`${radio}.ProbeControl.NoProbeRespRSSI`, `${radio}.ProbeControl.NoProbeRespRSSIThresh`] }),
    opt('site.rf', `${b}.advanced.mu_mimo`, 'MU-MIMO', 'switch', 'bool', { ...adv, ...exp, disruption: 'radio_bounce', firmwarePaths: [`${radio}.MuMimo.Enabled`] }),
    opt('site.rf', `${b}.advanced.txbf`, 'Transmit beamforming', 'switch', 'bool', { ...adv, ...exp, disruption: 'radio_bounce', firmwarePaths: [`${radio}.TxBf.Enabled`] }),
  ]
}

const siteRF: OptionMeta[] = [
  opt('site.rf', 'managed', 'Manage RF', 'switch', 'bool', { default: true, disruption: 'all_radios_bounce' }),
  ...bandOptions('24'),
  ...bandOptions('5'),
  opt('site.rf', 'band_5.dfs', 'Use DFS channels', 'switch', 'bool', { default: false, confidence: 'likely', help: 'DFS channels need a 60 s radar check (CAC) when the radio comes up and may move on radar.' }),
  opt('site.rf', 'band_5.dfs_fallback', 'DFS fallback', 'select', 'int', { ...adv, default: 0, disruption: 'radio_bounce', help: 'Where the radio goes when radar is detected.', firmwarePaths: ['Radios[5].DFSjumplist'] }),
  opt('site.rf', 'scanning_radio', 'Scanning radio', 'switch', 'bool', { ...adv, ...exp, default: true, disruption: 'radio_bounce', help: 'Turning it off removes the planner’s neighbour and utilisation data.', firmwarePaths: ['Monitor.Enabled'] }),
]

// ---- AP (§3.6) ----
const ap: OptionMeta[] = [
  opt('ap', 'name', 'Name', 'text', 'string', { min: 1, max: 64, firmwarePaths: ['Name', 'Hostname'] }),
  opt('ap', 'site_id', 'Site', 'select', 'string', { disruption: 'all_radios_bounce', help: 'Moving the AP replaces its WLANs with the new site’s.' }),
  opt('ap', 'management_ip', 'Pinned source IP', 'ip', 'string', { default: '', help: 'Only accept this AP from this address. Leave empty to accept it from anywhere, for example behind NAT.' }),
  opt('ap', 'secret', 'AP secret', 'secret_file', 'string', { secret: true, help: 'The AP’s 32-byte EEPROM secret. The dismist ap33 onboard command uploads it for you.' }),
  opt('ap', 'led_brightness', 'LED brightness', 'slider', 'int', { min: 0, max: 255, firmwarePaths: ['LED.Brightness'] }),
  opt('ap', 'ssh_enabled', 'SSH access', 'segmented', 'bool', { ...adv, enum: [value('inherit', 'Inherit'), value('on', 'On'), value('off', 'Off')], firmwarePaths: ['SSH.Disabled', 'SSH.External'] }),
  opt('ap', 'management', 'Management network', 'management', 'object', { ...exp, phase: 3, disruption: 'stranding_risk', firmwarePaths: ['SVIs[]', 'DNS', 'NTP'] }),
  opt('ap', 'eth1', 'eth1 mode', 'eth1', 'object', { ...exp, phase: 3, disruption: 'stranding_risk', firmwarePaths: ['SwitchPorts[]'] }),
  opt('ap', 'wired_client_stats', 'Report wired clients on eth1', 'segmented', 'bool', { ...exp, phase: 3, firmwarePaths: ['EnableWiredClientStats'] }),
  opt('ap.rf', 'enabled', 'Enabled', 'switch', 'bool', { disruption: 'radio_bounce', firmwarePaths: ['Radios[].Disabled'] }),
  opt('ap.rf', 'channel', 'Channel', 'channel', 'int', { disruption: 'radio_bounce', help: 'Auto uses the site’s channel plan.', firmwarePaths: ['Radios[].Channel', 'Radios[].RRM.Channel'] }),
  opt('ap.rf', 'width', 'Width', 'select', 'int', { unit: 'MHz', disruption: 'radio_bounce', firmwarePaths: ['Radios[].Width', 'Radios[].RRM.Width'] }),
  opt('ap.rf', 'power_dbm', 'Transmit power', 'number', 'int', { min: 1, max: 25, unit: 'dBm', confidence: 'likely', disruption: 'radio_bounce', firmwarePaths: ['Radios[].Power', 'Radios[].RRM.Power'] }),
]

export const provisionalOptions: Record<string, OptionMeta> = Object.fromEntries(
  [...wlan, ...site, ...siteRF, ...ap].map((meta) => [meta.key, meta]),
)

export const SECURITY_GROUPS = { PERSONAL, ENTERPRISE, WPA3 }
