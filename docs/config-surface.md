# AP33 configuration surface

What dismist can set on an AP33 running firmware 0.15, and how. The tables
below the "generated" marker come from the code: the option registry
(`internal/options`), the AU regulatory table and the 0.15 config path list.
They are field names, types and values only; no passphrases, keys or device
secrets appear here.

## How a config is built

For each AP dismist compiles one JSON document in Mist's order: the model
template, then the site's typed settings, then the site expert patch, then
the AP's typed overrides, then the AP expert patch. WLANs are compiled
separately (template WLAN, then the WLAN's typed settings, then its expert
patch) and inserted afterwards. An expert patch wins over the typed fields
of its own and earlier scopes, and the screen of every typed field it
overrides says so. Keys in patches are folded to the firmware's spelling
(`roamMode` becomes `RoamMode`) before merging, because the firmware itself
matches keys case-insensitively.

Typed options write the post-normalisation form the firmware runs: the full
16-flag `Auth.AKM` object, values already clamped, canonical key case. A key
the firmware ignores is never presented as a working option.

## Fields dismist always sets

These are written after every patch, so no patch can change them:

| Path | Value |
| --- | --- |
| `ID` | The AP's ID: its base MAC in dashed form, the ID it authenticates with. |
| `UUID` | `00000000-0000-0000-1000-` followed by the ID's 12 hex digits. |
| `Name`, `Hostname` | The AP name, when one is set. |
| `SiteID`, `SiteName` | The AP's site. |
| `Timestamp` | The config revision time. |
| `WLANs[].ID`, `SSID`, `Enabled`, `Vlans`, `Auth` | From the WLAN's typed settings. |
| `RootAuthorizedSSHKeys[]`, `SSH.AuthorizedKeys[]` | The template's keys plus the site's operator SSH public keys. Kept in both the legacy and the typed fields, so the LAN recovery path stays open. |

Neither the config nor any API response carries the AP's 32-byte EEPROM
authentication secret: the AP proves it holds it during authentication. The
WLAN passphrase is in the config the AP receives but redacted in every view.

## Live findings on AP33 0.15 (AU/890)

Measured read-only on a production AP33-WW, `apfw-0.15.35064-noro-84df`, on
2026-11-14 (see [verification](verification.md) for how to repeat them):

- **No 160 MHz.** Under AU/890 the radio lists no 160 MHz chanspec, so
  dismist never offers 160 MHz. 80 MHz blocks are 36-64, 100-112, 132-144
  and 149-161; 2.4 GHz has channels 1 to 13.
- **Power** equals what is configured: `Power`/`RRM.Power` 17 dBm on 5 GHz
  and 12 dBm on 2.4 GHz showed as `txpwr1` 17 and 12.
- **WPA2/WPA3 transition** (`Auth.AKM {WPA2_PSK, WPA3_PSK}` with
  `Dot11w {Enabled: true, Required: false}`) runs as `wpa_auth WPA2-PSK SAE`
  with `mfp 1`; iOS joins with SAE.
- **A WLAN security change restarts the BSS**: clients on that WLAN
  reconnect.
- **Losing the controller session does not drop Wi-Fi clients.** A pod
  restart or a reconnect-based config push alone is invisible to clients;
  only a config that restarts a WLAN or radio is not.

## Config the AP merges locally

ep-control also merges optional files from `/rw/etc/mist/ep-control/`:
`local_defaults.config` before the controller's config, and
`local_overrides`, `local_radio_overrides`, `local_wlan_overrides` and
`local_l2tp_overrides` after it. They do not normally exist. When they do,
the AP runs something other than what dismist compiled, and a key dismist
leaves out may still be set locally. `dismist ap33 inspect` lists any such
file with its size, SHA-256 and key paths; `dismist ap33 onboard` reports
them to the controller (names and hashes only) and can move them aside
(`--remove-local-files`). The schema-2 migration refuses to claim an AP's
config is unchanged while it has local files.

<!-- BEGIN GENERATED: go test ./cmd/dismist -run TestConfigSurfaceDoc -update-docs -->

## Typed options

137 options. *Experimental* options and values are accepted only on a site with the experimental switch on, until a live test promotes them (see [verification](verification.md)). *Legacy default* is what an object that existed before the schema-2 migration keeps.

### WLAN

| Option | Writes | Values | Default | Legacy default | Tier | Status | Disruption |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Band steering, prefer 5 GHz (`wlan.band_steering`) | `WLANs[].BandConfigs[].Steer` | bool | `false` | `true` | advanced | likely | wlan_restart |
| Bands (`wlan.bands`) | `WLANs[].Bands`, `WLANs[].RateSets`, `WLANs[].BandConfigs` | `2.4`, `5` | `["2.4","5"]` | same | basic | confirmed | wlan_restart |
| Allow IPv6 ND (`wlan.broadcast.allow_ipv6_nd`) | `WLANs[].BcastFilter.AllowICMPv6NeighborDiscovery` | bool | `true` | same | advanced | likely | wlan_restart |
| Allow mDNS (`wlan.broadcast.allow_mdns`) | `WLANs[].BcastFilter.Mdns_allow` | bool | `false` | same | advanced | likely | wlan_restart |
| Allow SSDP (`wlan.broadcast.allow_ssdp`) | `WLANs[].BcastFilter.Ssdp_allow` | bool | `false` | same | advanced | likely | wlan_restart |
| ARP filter (`wlan.broadcast.arp_filter`) | `WLANs[].BcastFilter.Arp` | bool | `false` | same | advanced | likely | wlan_restart |
| Limit broadcast and multicast (`wlan.broadcast.limit`) | `WLANs[].BcastFilter.Mcast` | bool | `false` | same | advanced | likely | wlan_restart |
| Multicast to unicast (`wlan.broadcast.mcast_to_ucast`) | `WLANs[].Bcast2Ucast.ForcedEnabled` | bool | `false` | same | advanced | experimental | wlan_restart |
| DHCP for wireless bridges (`wlan.broadcast.wireless_bridge_dhcp`) | `WLANs[].BcastFilter.AllowDHCPtoWirelessBridge` | bool | `false` | same | advanced | likely | wlan_restart |
| BSSID index (`wlan.bssid_index`) | `WLANs[].BandConfigs[].BSSIDIndex` | 1 to 15 | – | same | readonly | confirmed | none |
| Only DHCP-provided DNS (`wlan.dhcp_dns_only`) | `WLANs[].RestrictDNS.FromDHCP` | bool | `false` | same | advanced | likely | wlan_restart |
| Only DHCP-addressed clients (`wlan.dhcp_only`) | `WLANs[].NoStaticIP` | bool | `false` | same | advanced | likely | wlan_restart |
| Disable 802.11ax (`wlan.disable_11ax`) | `WLANs[].DisabledIEs.DisableHeIes` | bool | `false` | same | advanced | likely | wlan_restart |
| Disable 802.11n/ac (`wlan.disable_11nac`) | `WLANs[].DisabledIEs.DisableHTIes`, `WLANs[].DisabledIEs.DisableVHTIes` | bool | `false` | same | advanced | likely | wlan_restart |
| DTIM (`wlan.dtim`) | `WLANs[].DTIM.Period` | 1 to 255 | `2` | same | advanced | confirmed | wlan_restart |
| EAPOL v1 (`wlan.eapol_v1`) | `WLANs[].Auth.EAPOLVersion` | bool | `false` | same | advanced | likely | wlan_restart |
| Enabled (`wlan.enabled`) | `WLANs[].Enabled` | bool | `true` | same | basic | confirmed | wlan_restart |
| Group key rekey (`wlan.group_rekey_sec`) | `WLANs[].Auth.GroupRekeyIntervalSec`, `WLANs[].Auth.GroupMasterRekeyIntervalSec` | 60 to 86400, s | `86400` | same | advanced | likely | wlan_restart |
| Hidden SSID (`wlan.hidden`) | `WLANs[].Hidden` | bool | `false` | same | basic | likely | wlan_restart |
| Honour blocked clients (`wlan.honour_blocklist`) | `WLANs[].BlackList.Enabled` | bool | `true` | same | advanced | likely | wlan_restart |
| Idle timeout (`wlan.idle_timeout_sec`) | `WLANs[].WCInactivity.TimeoutSec` | 60 to 86400, s | `1800` | `3600` | advanced | confirmed | wlan_restart |
| Client isolation (`wlan.isolation`) | `WLANs[].Isolation`, `WLANs[].NetIsolation` | `none`, `clients`, `clients_and_lan` | `"none"` | same | basic | likely | wlan_restart |
| Max clients (`wlan.max_clients`) | `WLANs[].MaxStaAllowed` | 2 to 512 | – | same | advanced | confirmed | wlan_restart |
| Mobility domain (802.11r) (`wlan.mdid`) | `WLANs[].MDID` | int | – | same | readonly | confirmed | none |
| Minimum client RSSI, 2.4 GHz (`wlan.min_rssi.band_24`) | `WLANs[].BandConfigs[].GeoFence` | -95 to -50, dBm | – | same | advanced | experimental | wlan_restart |
| Minimum client RSSI, 5 GHz (`wlan.min_rssi.band_5`) | `WLANs[].BandConfigs[].GeoFence` | -95 to -50, dBm | – | same | advanced | experimental | wlan_restart |
| Per-device passphrases (`wlan.mpsk.enabled`) | `WLANs[].Auth.MultiPSK.Enabled` | bool | `false` | same | advanced | experimental | wlan_restart |
| Keys (`wlan.mpsk.keys`) | `WLANs[].Auth.MultiPSK.PSKs`, `SwitchPorts[].Vlans` | object | – | same | advanced | experimental | wlan_restart |
| Isolate each key's devices (`wlan.mpsk.private`) | `WLANs[].Auth.MultiPSK.PrivateWlan` | bool | `false` | same | advanced | experimental | wlan_restart |
| Option 82 circuit ID (`wlan.option82.circuit_id`) | `WLANs[].DhcpOption82Data` | ≤ 64 bytes | `""` | same | advanced | likely | wlan_restart |
| DHCP Option 82 (`wlan.option82.enabled`) | `WLANs[].DhcpOption82` | bool | `false` | same | advanced | likely | wlan_restart |
| OWE companion SSID (`wlan.owe_ssid`) | `WLANs[].OWE.SSID` | ≤ 32 bytes | `""` | same | advanced | experimental | wlan_restart |
| PMF (802.11w) (`wlan.pmf`) | `WLANs[].Dot11w`, `WLANs[].Auth.AKM` | `disabled`, `optional`, `required` | `"optional"` | same | advanced | confirmed | wlan_restart |
| Passphrase (`wlan.psk`) | `WLANs[].Auth.PSK` | string | – | same | basic | confirmed | wlan_restart |
| QoS override (`wlan.qos_class`) | `WLANs[].QOS` | `none`, `best_effort`, `background`, `video`, `voice` | `"none"` | same | advanced | experimental | wlan_restart |
| Accounting servers (`wlan.radius.acct`) | `WLANs[].Acct_servers` | object | – | same | advanced | likely | wlan_restart |
| Interim accounting (`wlan.radius.acct_interim_sec`) | `WLANs[].Acct_Interim_Interval` | 0 to 65535, s | – | same | advanced | likely | wlan_restart |
| RADIUS servers (`wlan.radius.auth`) | `WLANs[].Auth_servers` | object | – | same | basic | likely | wlan_restart |
| Change of Authorisation (`wlan.radius.coa`) | `WLANs[].COA.Enabled`, `WLANs[].COA.COAServers`, `WLANs[].COA.EventTimeStampOptional` | object | – | same | advanced | experimental | wlan_restart |
| Dynamic VLAN (`wlan.radius.dynamic_vlan`) | `WLANs[].Dynamic_vlan`, `SwitchPorts[].Vlans` | object | – | same | advanced | experimental | wlan_restart |
| MAC authentication (`wlan.radius.mac_auth`) | `WLANs[].MacAuth.Enabled` | bool | `false` | same | advanced | likely | wlan_restart |
| NAS-Identifier (`wlan.radius.nas_id`) | `WLANs[].Nas_ID` | ≤ 48 bytes | – | same | advanced | likely | wlan_restart |
| NAS-IP-Address (`wlan.radius.nas_ip`) | `WLANs[].NasIPAddress` | string | – | same | advanced | likely | wlan_restart |
| Re-authenticate when Session-Timeout expires (`wlan.radius.reauth_on_session_timeout`) | `WLANs[].Auth.ReauthOnSessionTimeout` | bool | `false` | same | advanced | likely | wlan_restart |
| Periodic re-authentication (`wlan.radius.reauth_sec`) | `WLANs[].Auth.EapReauthIntervalSec` | 0 to 86400, s | – | same | advanced | likely | wlan_restart |
| Per-client rate limit, down (`wlan.rate_limit.client_down`) | `WLANs[].RateLimit.ClientDown` | 1 to 999000, kbps | – | same | advanced | experimental | wlan_restart |
| Per-client rate limit, up (`wlan.rate_limit.client_up`) | `WLANs[].RateLimit.ClientUp` | 1 to 999000, kbps | – | same | advanced | experimental | wlan_restart |
| WLAN rate limit, down (`wlan.rate_limit.wlan_down`) | `WLANs[].RateLimit.WlanDown` | 1 to 999000, kbps | – | same | advanced | experimental | wlan_restart |
| WLAN rate limit, up (`wlan.rate_limit.wlan_up`) | `WLANs[].RateLimit.WlanUp` | 1 to 999000, kbps | – | same | advanced | experimental | wlan_restart |
| Data rate profile (`wlan.rates`) | `WLANs[].RateSets` | `compatible`, `no_legacy`, `high_density` (experimental), `legacy_only` (experimental) | `"compatible"` | same | advanced | confirmed | wlan_restart |
| `wlan.reclaim_timeout_sec` | `WLANs[].WCInactivity.ReclaimTimeoutSec` | s | `300` | `600` | expert | likely | wlan_restart |
| AP-to-AP key caching (`wlan.roaming.ap_key_cache`) | `WLANs[].KeyCache.LocalAPkeyCache`, `WLANs[].KeyCache.KeyEncryptKey` | bool | `false` | same | advanced | experimental | wlan_restart |
| AP-initiated steering (BTM) (`wlan.roaming.ap_steering`) | `WLANs[].WNM.BSSTransitionManagement.Active` | bool | `false` | same | advanced | likely | wlan_restart |
| BSS Load element (`wlan.roaming.bss_load`) | `WLANs[].WNM.BSSLoad` | bool | `true` | same | advanced | confirmed | wlan_restart |
| 802.11v BSS transition (`wlan.roaming.bss_transition`) | `WLANs[].WNM.BSSTransitionManagement.Passive` | bool | `true` | same | advanced | confirmed | wlan_restart |
| Fast roaming (`wlan.roaming.fast`) | `WLANs[].RoamMode`, `WLANs[].MDID`, `WLANs[].OverDS`, `WLANs[].Auth.AKM` | `none`, `okc` (experimental), `11r` (experimental) | `"none"` | same | advanced | likely | wlan_restart |
| 802.11k neighbour reports (`wlan.roaming.neighbor_report`) | `WLANs[].WNM.NeighborReport` | bool | `true` | same | advanced | confirmed | wlan_restart |
| SAE H2E only (`wlan.sae_h2e_only`) | `WLANs[].Auth.SAEHash2ElementOnly` | bool | `false` | same | advanced | likely | wlan_restart |
| Schedule (`wlan.schedule`) | `WLANs[].BSS` | object | – | same | advanced | experimental | wlan_restart |
| Security (`wlan.security`) | `WLANs[].Auth.Type`, `WLANs[].Auth.AKM`, `WLANs[].Auth.Pairwise`, `WLANs[].Auth.PSK`, `WLANs[].Dot11w`, `WLANs[].OWE`, `WLANs[].Auth.TransitionDisableIndication`, `WLANs[].Auth.SAEHash2ElementOnly`, `WLANs[].Auth_servers` | `wpa2_personal`, `wpa2_wpa3_personal`, `wpa3_personal`, `open`, `owe` (experimental), `owe_transition` (experimental), `wpa2_enterprise`, `wpa2_wpa3_enterprise`, `wpa3_enterprise` | `"wpa2_wpa3_personal"` | `"wpa2_personal"` | basic | confirmed | wlan_restart |
| SSID (`wlan.ssid`) | `WLANs[].SSID` | ≤ 32 bytes | `""` | same | basic | confirmed | wlan_restart |
| WPA3 transition disable (`wlan.transition_disable`) | `WLANs[].Auth.TransitionDisableIndication` | bool | `false` | same | advanced | likely | wlan_restart |
| Disable U-APSD (`wlan.uapsd_disabled`) | `WLANs[].WME.DisableApsd` | bool | `false` | same | advanced | likely | wlan_restart |
| `wlan.ucast_disassoc_on_bss_down` | `WLANs[].UcastDisassocOnBssDown` | bool | `true` | `"absent"` | expert | likely | wlan_restart |
| VLAN (`wlan.vlan_id`) | `WLANs[].Vlans`, `SwitchPorts[].Vlans` | 1 to 4094 | `1` | same | basic | confirmed | wlan_restart |
| Disable WMM (`wlan.wmm_disabled`) | `WLANs[].WME.Disabled` | bool | `false` | same | advanced | likely | wlan_restart |

### Site

| Option | Writes | Values | Default | Legacy default | Tier | Status | Disruption |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Turn off the Bluetooth (BLE) radio (`site.ap_defaults.ble_disabled`) | `BLE.Disabled` | bool | `false` | same | advanced | likely | none |
| Config auto-recovery (`site.ap_defaults.config_recovery`) | `APRecovery.Disabled`, `APRecovery.RecoveryTimeMin` | object | – | same | advanced | experimental | none |
| eth1 default mode (`site.ap_defaults.eth1`) | `SwitchPorts[].Vlans`, `SwitchPorts[].PortVlanID`, `SwitchPorts[].Phy.Shutdown` | `same_as_uplink`, `access`, `trunk`, `disabled` | `"same_as_uplink"` | same | advanced | experimental | none |
| Use the AP name as hostname and DHCP hostname (`site.ap_defaults.hostname_from_name`) | `Hostname`, `SVIs[].DHCP.Hostname` | bool | `true` | `false` | basic | confirmed | none |
| Keep WLANs up when the uplink is down (`site.ap_defaults.keep_wlans_up_on_uplink_down`) | `Monitor.EthUplink.Enabled` | bool | `false` | same | advanced | likely | none |
| LED brightness (`site.ap_defaults.led_brightness`) | `LED.Brightness` | 0 to 255 | `255` | same | basic | confirmed | none |
| When PoE power is insufficient (`site.ap_defaults.poe_constrained`) | `PoePwrConstrained.EnableRadios` | `keep_radios`, `disable_radios` | `"keep_radios"` | `"disable_radios"` | advanced | likely | none |
| Report wired clients on eth1 (`site.ap_defaults.wired_client_stats`) | `EnableWiredClientStats` | bool | `false` | same | advanced | likely | none |
| Country (`site.country`) | `Country`, `Radios[].CCRev`, `Radios[].CCRevSP`, `Monitor.CCRev` | `AU` | – | same | basic | confirmed | all_radios_bounce |
| Experimental options (`site.experimental`) | – | bool | `false` | same | advanced | confirmed | none |
| DNS servers (`site.management.dns`) | `DNS` | []string | – | same | advanced | likely | none |
| NTP servers (`site.management.ntp`) | `NTP` | []string | – | same | advanced | likely | none |
| Management VLAN (`site.management.vlan_id`) | `SVIs[].Vlan_ID`, `SwitchPorts[].Vlans` | 1 to 4094 | `1` | same | advanced | experimental | stranding_risk |
| Name (`site.name`) | – | ≤ 64 bytes | – | same | basic | confirmed | none |
| ARP spoof check (`site.protection.arp_spoof_check`) | `ProxyArp.Disable_ReqSpoofCheck`, `ProxyArp.Disable_RespSpoofCheck` | bool | `false` | same | advanced | likely | none |
| Blocked clients (`site.protection.blocked_clients`) | `BlackList.WCMacs` | object | – | same | advanced | likely | none |
| DHCP fingerprinting (`site.protection.dhcp_fingerprinting`) | `Fingerprint.DHCPPayload` | bool | `false` | same | advanced | likely | none |
| Repeated auth-failure penalty box (`site.protection.penalty_box`) | `WIDS` | object | – | same | advanced | experimental | none |
| Proxy ARP (`site.protection.proxy_arp`) | `ProxyArp.Enabled` | bool | `true` | same | advanced | likely | none |
| SSH access (`site.ssh.enabled`) | `SSH.Disabled`, `SSH.External`, `SSH.AuthorizedKeys` | bool | `false` | same | basic | confirmed | none |
| Root SSH keys (`site.ssh.keys`) | `SSH.AuthorizedKeys` | []string | – | same | basic | confirmed | none |
| Read-only stats keys (`site.ssh.readonly_keys`) | `SSH.RestrictedKeys` | []string | – | same | advanced | experimental | none |
| Remote syslog (`site.syslog`) | `Syslogs` | object | – | same | advanced | experimental | none |
| Timezone (`site.timezone`) | – | string | – | same | basic | confirmed | none |

### Site radio profile

| Option | Writes | Values | Default | Legacy default | Tier | Status | Disruption |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 2.4 GHz antenna mode (`site.rf.band_24.antenna`) | `Radios[].ChainSelectMode.RxChain`, `Radios[].ChainSelectMode.TxChain` | `default`, `2x2`, `1x1` | `"default"` | same | advanced | likely | radio_bounce |
| 2.4 GHz channels for Auto (`site.rf.band_24.channels`) | – | []int | `[1,6,11]` | same | basic | confirmed | none |
| 2.4 GHz enabled (`site.rf.band_24.enabled`) | `Radios[].Disabled` | bool | `true` | same | basic | confirmed | radio_bounce |
| 2.4 GHz minimum probe-response RSSI (`site.rf.band_24.min_probe_rssi`) | `Radios[].ProbeControl.NoProbeRespRSSI`, `Radios[].ProbeControl.NoProbeRespRSSIThresh` | -95 to -50, dBm | – | same | advanced | likely | radio_bounce |
| 2.4 GHz MU-MIMO (`site.rf.band_24.mu_mimo`) | `Radios[].MuMimo.Enabled` | bool | – | same | advanced | experimental | radio_bounce |
| 2.4 GHz transmit power (`site.rf.band_24.power`) | `Radios[].Power`, `Radios[].RRM.Power` | dBm | `{"max":15,"min":8,"mode":"auto"}` | same | basic | likely | radio_bounce |
| 2.4 GHz preamble (`site.rf.band_24.preamble`) | `Radios[].PreambleLength` | `auto`, `short`, `long` | `"short"` | same | advanced | confirmed | radio_bounce |
| 2.4 GHz airtime fairness (`site.rf.band_24.taf`) | `Radios[].TAFMode` | `default`, `off`, `on` | `"default"` | same | advanced | confirmed | radio_bounce |
| 2.4 GHz target wake time (TWT) (`site.rf.band_24.twt`) | `Radios[].TWTDisabled` | bool | `true` | same | advanced | likely | radio_bounce |
| 2.4 GHz transmit beamforming (`site.rf.band_24.txbf`) | `Radios[].TxBf.Enabled` | bool | – | same | advanced | experimental | radio_bounce |
| 2.4 GHz width (`site.rf.band_24.width`) | `Radios[].Width`, `Radios[].RRM.Width` | `20`, MHz | `20` | same | basic | confirmed | radio_bounce |
| 5 GHz antenna mode (`site.rf.band_5.antenna`) | `Radios[].ChainSelectMode.RxChain`, `Radios[].ChainSelectMode.TxChain` | `default`, `4x4`, `3x3`, `2x2`, `1x1` | `"default"` | same | advanced | likely | radio_bounce |
| 5 GHz channels for Auto (`site.rf.band_5.channels`) | – | []int | `[36,40,44,48,149,153,157,161]` | same | basic | confirmed | none |
| Use DFS channels (`site.rf.band_5.dfs`) | `Radios[].DFSjumplist` | bool | `false` | same | basic | likely | radio_bounce |
| DFS fallback (`site.rf.band_5.dfs_fallback`) | `Radios[].DFSjumplist.ChanSpecs` | int | `0` | same | advanced | confirmed | radio_bounce |
| 5 GHz enabled (`site.rf.band_5.enabled`) | `Radios[].Disabled` | bool | `true` | same | basic | confirmed | radio_bounce |
| 5 GHz minimum probe-response RSSI (`site.rf.band_5.min_probe_rssi`) | `Radios[].ProbeControl.NoProbeRespRSSI`, `Radios[].ProbeControl.NoProbeRespRSSIThresh` | -95 to -50, dBm | – | same | advanced | likely | radio_bounce |
| 5 GHz MU-MIMO (`site.rf.band_5.mu_mimo`) | `Radios[].MuMimo.Enabled` | bool | – | same | advanced | experimental | radio_bounce |
| 5 GHz transmit power (`site.rf.band_5.power`) | `Radios[].Power`, `Radios[].RRM.Power` | dBm | `{"max":20,"min":12,"mode":"auto"}` | same | basic | likely | radio_bounce |
| 5 GHz preamble (`site.rf.band_5.preamble`) | `Radios[].PreambleLength` | `auto`, `short`, `long` | `"short"` | same | advanced | confirmed | radio_bounce |
| 5 GHz airtime fairness (`site.rf.band_5.taf`) | `Radios[].TAFMode` | `default`, `off`, `on` | `"default"` | same | advanced | confirmed | radio_bounce |
| 5 GHz target wake time (TWT) (`site.rf.band_5.twt`) | `Radios[].TWTDisabled` | bool | `true` | same | advanced | likely | radio_bounce |
| 5 GHz transmit beamforming (`site.rf.band_5.txbf`) | `Radios[].TxBf.Enabled` | bool | – | same | advanced | experimental | radio_bounce |
| 5 GHz width (`site.rf.band_5.width`) | `Radios[].Width`, `Radios[].RRM.Width` | `20`, `40`, `80`, MHz | `80` | same | basic | confirmed | radio_bounce |
| Manage RF (`site.rf.managed`) | `Radios` | bool | `true` | `"lifted"` | basic | confirmed | all_radios_bounce |
| Scanning radio (`site.rf.scanning_radio`) | `Monitor.Enabled` | bool | `true` | same | advanced | experimental | radio_bounce |

### AP

| Option | Writes | Values | Default | Legacy default | Tier | Status | Disruption |
| --- | --- | --- | --- | --- | --- | --- | --- |
| eth1 mode (`ap.eth1`) | `SwitchPorts[].Vlans`, `SwitchPorts[].PortVlanID`, `SwitchPorts[].Phy.Shutdown` | `inherit`, `same_as_uplink`, `access`, `trunk`, `disabled` | – | same | advanced | experimental | none |
| LED brightness (`ap.led_brightness`) | `LED.Brightness` | 0 to 255 | – | same | basic | confirmed | none |
| Locate (blink LED) (`ap.locate`) | `LED.Locator` | bool | – | same | readonly | experimental | none |
| Address / prefix / gateway (`ap.management.address`) | `SVIs[].IP`, `SVIs[].Mask`, `SVIs[].Gw` | object | – | same | advanced | experimental | stranding_risk |
| DNS servers (`ap.management.dns`) | `DNS` | []string | – | same | advanced | experimental | stranding_risk |
| IP mode (`ap.management.ip_mode`) | `SVIs[].DHCP.Enabled`, `SVIs[].DHCP_Ena` | `dhcp`, `static` | `"dhcp"` | same | advanced | experimental | stranding_risk |
| NTP servers (`ap.management.ntp`) | `NTP` | []string | – | same | advanced | likely | none |
| Management VLAN (`ap.management.vlan_id`) | `SVIs[].Vlan_ID`, `SwitchPorts[].Vlans` | 1 to 4094 | – | same | advanced | experimental | stranding_risk |
| Pinned source IP (`ap.management_ip`) | – | string | – | same | basic | confirmed | none |
| Name (`ap.name`) | `Name`, `Hostname`, `SVIs[].DHCP.Hostname` | ≤ 64 bytes | – | same | basic | confirmed | none |
| Site (`ap.site_id`) | – | string | – | same | basic | confirmed | none |
| SSH access (`ap.ssh_enabled`) | `SSH.Disabled`, `SSH.External`, `SSH.AuthorizedKeys` | bool | – | same | advanced | confirmed | none |
| Report wired clients on eth1 (`ap.wired_client_stats`) | `EnableWiredClientStats` | bool | – | same | advanced | likely | none |

### AP radio overrides

| Option | Writes | Values | Default | Legacy default | Tier | Status | Disruption |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 2.4 GHz channel (`ap.rf.band_24.channel`) | `Radios[].Channel`, `Radios[].RRM.Channel`, `Radios[].DFSjumplist` | int | `0` | same | advanced | confirmed | radio_bounce |
| 2.4 GHz enabled (`ap.rf.band_24.enabled`) | `Radios[].Disabled` | bool | – | same | advanced | confirmed | radio_bounce |
| 2.4 GHz transmit power (`ap.rf.band_24.power_dbm`) | `Radios[].Power`, `Radios[].RRM.Power` | 1 to 25, dBm | – | same | advanced | likely | radio_bounce |
| 2.4 GHz width (`ap.rf.band_24.width`) | `Radios[].Width`, `Radios[].RRM.Width` | `20`, MHz | – | same | advanced | confirmed | radio_bounce |
| 5 GHz channel (`ap.rf.band_5.channel`) | `Radios[].Channel`, `Radios[].RRM.Channel`, `Radios[].DFSjumplist` | int | `0` | same | advanced | confirmed | radio_bounce |
| 5 GHz enabled (`ap.rf.band_5.enabled`) | `Radios[].Disabled` | bool | – | same | advanced | confirmed | radio_bounce |
| 5 GHz transmit power (`ap.rf.band_5.power_dbm`) | `Radios[].Power`, `Radios[].RRM.Power` | 1 to 25, dBm | – | same | advanced | likely | radio_bounce |
| 5 GHz width (`ap.rf.band_5.width`) | `Radios[].Width`, `Radios[].RRM.Width` | `20`, `40`, `80`, MHz | – | same | advanced | confirmed | radio_bounce |

## Never written

No typed option writes these paths, and an expert patch that sets one is refused (a path matches at or below an entry; `*` ends a name prefix):

`Ipsec`, `Calea`, `SiteString`, `PAPIOpaque`, `DeIdent`, `AVS`, `BootConfig`, `Firmware`, `MapID`, `XYZ`, `Default`, `LicenseExpired`, `RegulatoryOrg`, `RadioSwap`, `IndoorUse`, `DirAntenna`, `ManagementTunnels`, `Monitor.PcapWLAN`, `WLANs[].WLCmds`, `WLANs[].Wxlan.Portal*`, `WLANs[].ISE`, `WLANs[].AirWatch`, `WLANs[].DPI`, `WLANs[].COA.CloudEnabled`, `WLANs[].BlackList.Cloud`, `WLANs[].Auth.WEP`, `WLANs[].Auth.SecondaryType`, `WLANs[].Auth.AKM.WPA_PSK`, `WLANs[].Auth.AKM.WPA_EAP`, `WLANs[].Auth.Pairwise.TKIP`, `WLANs[].Auth.Pairwise.WPA1`, `WLANs[].Mesh`, `WLANs[].ForMesh`.

## AU regulatory table (AU/890)

What the forms and the validator allow for a site in Australia. Widths marked *not offered* are kept out of typed forms.

| Band | Width | Primary channels | DFS channels | Offered |
| --- | --- | --- | --- | --- |
| 2.4 GHz | 20 MHz | 1 2 3 4 5 6 7 8 9 10 11 12 13 | none | yes |
| 2.4 GHz | 40 MHz | 1 2 3 4 5 6 7 8 9 10 11 12 13 | none | expert patch only |
| 5 GHz | 20 MHz | 36 40 44 48 52 56 60 64 100 104 108 112 116 132 136 140 144 149 153 157 161 165 | 52 56 60 64 100 104 108 112 116 132 136 140 144 | yes |
| 5 GHz | 40 MHz | 36 40 44 48 52 56 60 64 100 104 108 112 132 136 140 144 149 153 157 161 | 52 56 60 64 100 104 108 112 132 136 140 144 | yes |
| 5 GHz | 80 MHz | 36 40 44 48 52 56 60 64 100 104 108 112 132 136 140 144 149 153 157 161 | 52 56 60 64 100 104 108 112 132 136 140 144 | yes |

## AP33 0.15 config sections

Top-level keys of the config ep-control 0.15 decodes, with the number of paths below each (from `internal/options/firmware/ap33-0.15.paths`, field names and types only). Keys are matched case-insensitively by the firmware; dismist writes the canonical spelling.

| Key | Paths | Key | Paths | Key | Paths |
| --- | --- | --- | --- | --- | --- |
| `AAA` | 8 | `ALS` | 8 | `APRecovery` | 3 |
| `APState` | 20 | `AVS` | 5 | `AirIQ` | 2 |
| `AppDef` | 1 | `BLE` | 134 | `BlackList` | 3 |
| `BonjourGw` | 25 | `BootConfig` | 1 | `Calea` | 8 |
| `Certs` | 3 | `ClientBridge` | 15 | `Cloud` | 9 |
| `CnaHostnames` | 2 | `Country` | 1 | `DNS` | 2 |
| `DNSTtlCfg` | 3 | `DPI` | 17 | `DeIdent` | 3 |
| `Default` | 1 | `DhcpLookupEnabled` | 1 | `DirAntenna` | 3 |
| `Dnsmasq` | 7 | `DongleCrashLogsUploadDisabled` | 1 | `ESL_L2GW` | 7 |
| `ESL_SesImagotag` | 15 | `EnableWiredClientStats` | 1 | `EvalDNSFlows` | 1 |
| `ExtIO` | 12 | `FailedRetryAfter` | 1 | `Fingerprint` | 2 |
| `Firmware` | 1 | `GPS` | 4 | `Hostname` | 1 |
| `HttpRedirector` | 2 | `ID` | 1 | `IOTProxy` | 10 |
| `IndoorUse` | 1 | `Ipsec` | 8 | `IrqBalance` | 6 |
| `L2TPDelay` | 1 | `L2TPTunnels` | 48 | `LED` | 5 |
| `Lacp` | 5 | `LibrsnLogLevel` | 1 | `LicenseExpired` | 1 |
| `Lldp` | 2 | `LogRunState` | 2 | `MDNS` | 2 |
| `MQTT` | 26 | `ManagementTunnels` | 49 | `MapID` | 1 |
| `Mesh` | 23 | `MistPortalURL` | 1 | `Monitor` | 135 |
| `NTP` | 2 | `Name` | 1 | `NewFlowRateLimit` | 5 |
| `NotModified` | 1 | `OrgID` | 1 | `OurOUIs` | 2 |
| `PACE_KernelModCounters` | 3 | `PACE_RSNCache` | 4 | `PAPIOpaque` | 1 |
| `Pace` | 3 | `PacketPool` | 4 | `PauseFlowDebug` | 2 |
| `PoePwrConstrained` | 3 | `PowerConstrained` | 1 | `PrivateWlanConfigParams` | 5 |
| `ProxyArp` | 9 | `PwrMgmt` | 12 | `RadioSwap` | 1 |
| `RadioUcodeDump` | 3 | `Radios` | 138 | `RegulatoryOrg` | 1 |
| `RigadoCfg` | 7 | `Routing` | 6 | `RubyAxiOpMode` | 2 |
| `SSH` | 10 | `SVIs` | 50 | `ScanAntennaSelect` | 1 |
| `SignatureMatch` | 27 | `SiteID` | 1 | `SiteString` | 1 |
| `Stats` | 30 | `Steer` | 28 | `SviIPv4Cache` | 3 |
| `SviIPv6Cache` | 3 | `SwitchPorts` | 200 | `Syslogs` | 6 |
| `TcodeDelay` | 1 | `Timestamp` | 1 | `UUID` | 1 |
| `UWB` | 9 | `VLANNames` | 1 | `VLANNames{}` | 1 |
| `WCEventReconnect` | 10 | `WIDS` | 7 | `WLANs` | 715 |
| `WlDebug` | 2 | `Workarounds` | 3 | `Wxlan` | 6 |
| `XYZ` | 2 | `ZigBee` | 5 | `_errors` | 2 |

2004 paths in total.

<!-- END GENERATED -->
