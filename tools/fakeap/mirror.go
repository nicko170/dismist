package fakeap

import (
	"encoding/json"
	"fmt"
	"net"
)

// MirrorConfig makes the snapshot report what config it was served, as a
// real AP would after applying it: the access radios take the config's
// channel, width and power, and the VAPs become the enabled WLANs on their
// bands with the configured BSSID index (wl<radio>.<index>). Clients move
// to the first WLAN on their radio. It is a test convenience for live UI
// and post-delivery verification runs; the protocol never needs it.
func MirrorConfig(snapshot *Snapshot, config []byte) error {
	var parsed struct {
		Radios []struct {
			Band    string `json:"Band"`
			Channel int    `json:"Channel"`
			Width   int    `json:"Width"`
			Power   int    `json:"Power"`
			RRM     struct {
				Channel int `json:"Channel"`
				Width   int `json:"Width"`
				Power   int `json:"Power"`
			} `json:"RRM"`
		} `json:"Radios"`
		WLANs []struct {
			ID          string   `json:"ID"`
			SSID        string   `json:"SSID"`
			Enabled     bool     `json:"Enabled"`
			Bands       []string `json:"Bands"`
			BandConfigs []struct {
				Band       string `json:"Band"`
				BSSIDIndex int    `json:"BSSIDIndex"`
			} `json:"BandConfigs"`
		} `json:"WLANs"`
	}
	if err := json.Unmarshal(config, &parsed); err != nil {
		return err
	}
	// The AP33 radio table: r0 is 5 GHz, r1 is 2.4 GHz (index = radio).
	radioOf := map[string]int{"5": 0, "2.4": 1}
	for _, radio := range parsed.Radios {
		index, ok := radioOf[radio.Band]
		if !ok || index >= len(snapshot.Radios) {
			continue
		}
		pick := func(direct, rrm int) int {
			if direct != 0 {
				return direct
			}
			return rrm
		}
		if channel := pick(radio.Channel, radio.RRM.Channel); channel > 0 {
			snapshot.Radios[index].Channel = uint64(channel)
		}
		if width := pick(radio.Width, radio.RRM.Width); width > 0 {
			snapshot.Radios[index].Bandwidth = uint64(width)
		}
		if power := pick(radio.Power, radio.RRM.Power); power > 0 {
			snapshot.Radios[index].MaxTxPower = int64(power)
		}
		// A non-DFS channel has no radar check.
		if channel := snapshot.Radios[index].Channel; channel < 52 || channel > 144 {
			snapshot.Radios[index].CACState = 0
		}
	}
	base := map[int]net.HardwareAddr{}
	for _, wlan := range snapshot.WLANs {
		if len(wlan.BSSID) == 6 {
			if _, ok := base[int(wlan.RadioIndex)]; !ok {
				base[int(wlan.RadioIndex)] = wlan.BSSID
			}
		}
	}
	var wlans []WLAN
	first := map[int]WLAN{}
	for _, wlan := range parsed.WLANs {
		if !wlan.Enabled {
			continue
		}
		for _, band := range wlan.Bands {
			radio, ok := radioOf[band]
			if !ok {
				continue
			}
			index := 1
			for _, config := range wlan.BandConfigs {
				if config.Band == band && config.BSSIDIndex > 0 {
					index = config.BSSIDIndex
				}
			}
			bssid := append(net.HardwareAddr(nil), base[radio]...)
			if len(bssid) == 6 {
				bssid[5] = bssid[5]&0xf0 | byte(index&0x0f)
			} else {
				bssid = net.HardwareAddr{0x02, 0xaa, 0x00, 0x00, byte(0x10 + radio), byte(index)}
			}
			vap := WLAN{ID: wlan.ID, RadioIndex: uint64(radio), BSSID: bssid, Dev: fmt.Sprintf("wl%d.%d", radio, index), SSID: wlan.SSID}
			wlans = append(wlans, vap)
			if _, ok := first[radio]; !ok {
				first[radio] = vap
			}
		}
	}
	snapshot.WLANs = wlans
	for index := range snapshot.Clients {
		client := &snapshot.Clients[index]
		vap, ok := first[int(client.RadioIndex)]
		if !ok {
			continue
		}
		client.WLANID, client.SSID, client.BSSID = vap.ID, vap.SSID, vap.BSSID
	}
	for index := range snapshot.Radios {
		count := 0
		for _, client := range snapshot.Clients {
			if int(client.RadioIndex) == index {
				count++
			}
		}
		if index < 2 {
			snapshot.Radios[index].Clients = uint64(count)
		}
	}
	return nil
}
