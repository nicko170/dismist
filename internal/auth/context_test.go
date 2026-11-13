package auth

import (
	"encoding/hex"
	"testing"
)

func TestVersion2ContextMatchesIndependentVector(t *testing.T) {
	got := Version2Context(ContextInputs{
		Version: 2, ID: "ap-1", Model: "AP33-WW", Firmware: "fw-1",
		Suite: 1, Cert: []byte{0, 1, 255}, CipherSuite: 4865, Entity: "SERVER",
	})
	want := "9d63027796cccbff2cf22efb722dc5a80743061c4b72e55da50ea2744028205d"
	if hex.EncodeToString(got[:]) != want {
		t.Fatalf("unexpected context hash %x", got)
	}
}
