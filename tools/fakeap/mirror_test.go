package fakeap

import "testing"

func TestMirrorConfigFollowsRadiosAndWLANs(t *testing.T) {
	snapshot := DefaultSnapshot(2)
	config := []byte(`{"Radios":[{"Band":"2.4","Channel":1,"Width":20,"Power":12},{"Band":"5","RRM":{"Channel":149,"Width":80,"Power":17}}],
		"WLANs":[{"ID":"net-1","SSID":"Example","Enabled":true,"Bands":["2.4","5"],"BandConfigs":[{"Band":"2.4","BSSIDIndex":1},{"Band":"5","BSSIDIndex":2}]},
		{"ID":"net-2","SSID":"Off","Enabled":false,"Bands":["5"]}]}`)
	if err := MirrorConfig(&snapshot, config); err != nil {
		t.Fatal(err)
	}
	if r := snapshot.Radios[0]; r.Channel != 149 || r.Bandwidth != 80 || r.MaxTxPower != 17 || r.CACState != 0 {
		t.Fatalf("5 GHz radio %+v", r)
	}
	if r := snapshot.Radios[1]; r.Channel != 1 || r.Bandwidth != 20 || r.MaxTxPower != 12 {
		t.Fatalf("2.4 GHz radio %+v", r)
	}
	if len(snapshot.WLANs) != 2 || snapshot.WLANs[0].Dev != "wl1.1" || snapshot.WLANs[1].Dev != "wl0.2" || snapshot.WLANs[1].ID != "net-1" {
		t.Fatalf("VAPs %+v", snapshot.WLANs)
	}
	for _, client := range snapshot.Clients {
		if client.WLANID != "net-1" {
			t.Fatalf("client %+v", client)
		}
	}
}
