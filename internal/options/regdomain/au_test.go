package regdomain

import (
	"reflect"
	"testing"
)

func TestAUTableMatchesAppendixA(t *testing.T) {
	if ccrev, ok := CCRev("au"); !ok || ccrev != "AU/890" {
		t.Fatalf("ccrev %q %v", ccrev, ok)
	}
	if _, ok := CCRev("US"); ok {
		t.Fatal("only AU is supported (§9 Q9)")
	}
	au, _ := ForCountry("AU")
	for _, test := range []struct {
		band       string
		width, ch  int
		valid, dfs bool
	}{
		{Band24, 20, 1, true, false}, {Band24, 20, 13, true, false}, {Band24, 20, 14, false, false},
		{Band5, 80, 149, true, false}, // the live AP
		{Band5, 20, 116, true, true}, {Band5, 40, 116, false, false}, {Band5, 80, 116, false, false},
		{Band5, 20, 165, true, false}, {Band5, 40, 165, false, false},
		{Band5, 20, 120, false, false}, {Band5, 20, 124, false, false}, {Band5, 20, 128, false, false},
		{Band5, 80, 52, true, true}, {Band5, 80, 144, true, true}, {Band5, 80, 36, true, false},
		{Band5, 160, 36, true, false}, {Band5, 160, 100, false, false}, {Band5, 160, 64, true, true},
		{Band5, 20, 34, false, false},
	} {
		dfs, ok := au.Valid(test.band, test.width, test.ch)
		if ok != test.valid || (ok && dfs != test.dfs) {
			t.Errorf("%s/%d ch%d: valid %v dfs %v", test.band, test.width, test.ch, ok, dfs)
		}
	}
	if got := au.Block(Band5, 80, 153); !reflect.DeepEqual(got, []int{149, 153, 157, 161}) {
		t.Fatalf("block %v", got)
	}
	if got := au.Block(Band5, 20, 36); !reflect.DeepEqual(got, []int{36}) {
		t.Fatalf("20 MHz block %v", got)
	}
	if !au.Table(Band24, 40).ExpertOnly || !au.Table(Band5, 160).NeedsLabTest {
		t.Fatal("width flags")
	}
	if !reflect.DeepEqual(au.Widths(Band5), []int{20, 40, 80, 160}) {
		t.Fatal("widths")
	}
	for _, ch := range au.DefaultAuto[Band5] {
		if dfs, ok := au.Valid(Band5, 80, ch); !ok || dfs {
			t.Fatalf("default Auto channel %d must be non-DFS at 80 MHz", ch)
		}
	}
}
