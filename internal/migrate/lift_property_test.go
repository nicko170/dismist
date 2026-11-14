package migrate

import (
	"bytes"
	"math/rand"
	"testing"

	"github.com/nicko170/dismist/internal/compile"
	"github.com/nicko170/dismist/internal/compile/effective"
	"github.com/nicko170/dismist/internal/compile/legacyv0"
	"github.com/nicko170/dismist/internal/model"
	"github.com/nicko170/dismist/internal/rfplan"
)

// randomTypedState builds a random but valid schema-2 site.
func randomTypedState(random *rand.Rand) model.State {
	pick := func(values ...string) string { return values[random.Intn(len(values))] }
	coin := func() bool { return random.Intn(2) == 0 }
	site := model.Site{ID: homeSite, Name: "Home", Country: "AU",
		SSH:        model.SiteSSH{Enabled: true, Keys: []model.SSHKey{{Key: siteKey}}},
		Management: model.ManagementDefaults{VLANID: 1},
		APDefaults: model.APDefaults{LEDBrightness: random.Intn(256), PoEConstrained: pick(model.PoEKeepRadios, model.PoEDisableRadios),
			KeepWLANsUpOnUplinkDown: coin(), WiredClientStats: coin(), BLEDisabled: coin(), HostnameFromName: false},
		Protection: model.Protection{ProxyARP: coin(), ARPSpoofCheck: coin(), DHCPFingerprint: coin()},
		RF: model.RFProfile{Managed: true,
			Band24: model.BandProfile{Enabled: true, Width: 20, Channels: []int{1, 6, 11}, Power: model.PowerPolicy{Mode: model.PowerFixed, DBm: 1 + random.Intn(20)},
				Preamble: pick("auto", "short", "long"), Advanced: model.BandAdvanced{TAF: pick("off", "on")}},
			Band5: model.BandProfile{Enabled: true, Width: []int{20, 40, 80}[random.Intn(3)], Channels: []int{36, 40, 44, 48, 149, 153, 157, 161},
				Power: model.PowerPolicy{Mode: model.PowerFixed, DBm: 1 + random.Intn(20)}, Preamble: pick("auto", "short", "long")}},
	}
	security := pick(model.SecurityWPA2Personal, model.SecurityWPA2WPA3Personal, model.SecurityWPA3Personal)
	network := model.Network{ID: homeNetwork, SiteID: homeSite, SSID: "Example", PSK: "example-passphrase-01", VLANID: 1 + random.Intn(100), Enabled: true,
		Security: security, Bands: []string{"2.4", "5"}, BSSIDIndex: 1, BandSteering: coin(), Rates: pick("compatible", "no_legacy", "high_density"),
		DTIM: 1 + random.Intn(5), IdleTimeout: 60 + random.Intn(4000), ReclaimTimeoutSec: 300, GroupRekeySec: 3600 + random.Intn(80000),
		Hidden: coin(), Isolation: pick("", "clients", "clients_and_lan"), HonourBlocklist: coin(), MaxClients: []int{0, 30, 100}[random.Intn(3)],
		Roaming:   model.Roaming{Fast: "none", NeighborReport: coin(), BSSTransition: coin(), APSteering: coin(), BSSLoad: coin()},
		Broadcast: model.Broadcast{Limit: coin(), AllowMDNS: coin(), AllowSSDP: coin(), AllowIPv6ND: coin(), ARPFilter: coin(), WirelessBridgeDHCP: coin()},
		DHCPOnly:  coin(), DHCPDNSOnly: coin(), WMMDisabled: coin(), UAPSDDisabled: coin(), Disable11ax: coin(), Disable11nac: coin(), EAPOLv1: coin(),
		QoSClass: pick("", "voice", "video")}
	if security == model.SecurityWPA2Personal {
		network.PMF = pick("disabled", "optional", "required")
	}
	ap := model.APRecord{ID: liveAP, Name: "Hallway", SiteID: homeSite, Secret: secret, LocalFiles: []model.LocalFile{}, ConfigUpdatedAt: liveTime,
		RF: map[string]*model.RadioOverride{"2.4": {Channel: []int{1, 6, 11}[random.Intn(3)]}, "5": {Channel: []int{36, 149}[random.Intn(2)]}}}
	state := model.State{Schema: 2, UpdatedAt: liveTime, Sites: []model.Site{site}, APs: []model.APRecord{ap}, Networks: []model.Network{network}}
	rfplan.ApplyInitial(&state, liveTime)
	return state
}

// The "compile, lift, compile" property (design §8.5): lifting a typed
// compile's effective config gives typed values that compile to the same
// effective config.
func TestCompileLiftCompileProperty(t *testing.T) {
	random := rand.New(rand.NewSource(42))
	template := legacyv0.BundledTemplate()
	for iteration := 0; iteration < 150; iteration++ {
		state := randomTypedState(random)
		input, err := compile.InputFor(template, state, liveAP)
		if err != nil {
			t.Fatal(err)
		}
		output, err := compile.AP(input)
		if err != nil {
			t.Fatalf("iteration %d: %v", iteration, err)
		}
		ctx := compile.EffectiveContext(state.APs[0])
		first, _, err := effective.Normalise(output.Config, ctx)
		if err != nil {
			t.Fatal(err)
		}
		eff, _ := compile.DecodePatch(first)
		folded, _ := compile.FoldConfig(output.Config)
		lifted := state
		lifted.Sites = []model.Site{{ID: homeSite, Name: "Home", Country: "AU"}}
		lifted.Networks = []model.Network{{ID: homeNetwork, SiteID: homeSite, SSID: "Example", PSK: "example-passphrase-01",
			VLANID: state.Networks[0].VLANID, Enabled: true}}
		lifted.APs = []model.APRecord{{ID: liveAP, Name: "Hallway", SiteID: homeSite, Secret: secret, LocalFiles: []model.LocalFile{}, ConfigUpdatedAt: liveTime}}
		var siteLift SiteLift
		liftSite(&lifted.Sites[0], legacyv0.Site{SSHPublicKeys: []string{siteKey}}, eff, &siteLift)
		if !lifted.Sites[0].RF.Managed {
			t.Fatalf("iteration %d: RF lifted unmanaged: %s", iteration, siteLift.RFReason)
		}
		var networkLift NetworkLift
		if err := liftNetwork(&lifted.Networks[0], eff, []string{liveAP}, nil, &networkLift); err != nil {
			t.Fatalf("iteration %d: %v", iteration, err)
		}
		liftAP(&lifted.APs[0], lifted.Sites[0], eff, folded)
		input, err = compile.InputFor(template, lifted, liveAP)
		if err != nil {
			t.Fatal(err)
		}
		again, err := compile.AP(input)
		if err != nil {
			t.Fatalf("iteration %d: recompile: %v", iteration, err)
		}
		second, _, _ := effective.Normalise(again.Config, ctx)
		if !bytes.Equal(first, second) {
			t.Fatalf("iteration %d: lift is not an inverse: %v\nsite %+v\nnetwork %+v", iteration, compile.DiffPaths(first, second), state.Sites[0], state.Networks[0])
		}
	}
}
