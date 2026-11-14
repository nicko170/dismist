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

No typed options are declared in this build. The registry (`internal/options`) gains them in phase 2; this section is regenerated from it.

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
| 5 GHz | 160 MHz | 36 40 44 48 52 56 60 64 | 52 56 60 64 | not offered (unverified on AP33) |

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
