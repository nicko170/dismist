# v0.3.0 production-shape fixture

Written by the **v0.3.0 binary itself** (tag `v0.3.0`) through its API, with
`tools/migrationcompat/prodsafety.sh` and `FIXTURE_OUT` set:

- `controller.db`: the schema-1 database: site Home (AU, Australia/Sydney),
  WLAN "Example" on VLAN 1 with the network advanced patch
  `{"Auth":{"AKM":{"WPA2_PSK":true,"WPA3_PSK":true}},"Dot11w":{"Enabled":true,"Required":false}}`,
  the site advanced patch replacing `Radios[]` (AU/890; 2.4 GHz channel 1,
  20 MHz, 12 dBm; 5 GHz channel 149, 80 MHz, 17 dBm; `Channel`/`Width`/`Power`
  and `RRM`) plus `Monitor.CCRev` AU/890, and AP `02-aa-00-00-00-01` with a
  secret and the source pin 127.0.0.1.
- `served-v0.3.0.json`: the config v0.3.0 served that AP (fetched by fakeap).
- `admin.token`, `fixture.json`: the administrator token and the IDs.

Every value is synthetic (documentation IDs, SSID "Example", a counting
secret). `TestProductionDatabaseFromV030` starts this build on a copy and
proves the upgrade does not change what the AP runs.
