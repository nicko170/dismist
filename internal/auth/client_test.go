package auth

import (
	"encoding/hex"
	"testing"
)

// TestClientProofMatchesControllerVector pins ClientProof to the AP proof
// vector the controller's Reply3 test accepts (internal/controller
// auth_test.go): secret 0..31, QC 32..63, QS 64..95, counter 42.
func TestClientProofMatchesControllerVector(t *testing.T) {
	var in Inputs
	for i := range in.Secret {
		in.Secret[i] = byte(i)
		in.QC[i] = byte(i + 32)
		in.QS[i] = byte(i + 64)
	}
	in.Counter = 42
	in.DateTimeNS = 1234567890123456789
	in.Context = Version2Context(ContextInputs{
		Version: 2, ID: "ap-1", Model: "AP33-WW", Firmware: "fw-1", Suite: 1, Entity: "CLIENT",
	})
	got := ClientProof(in)
	if hex.EncodeToString(got[:]) != "023850a74f03051f70959826190422f473d760550f3ebbd0601eb7847b148fe2" {
		t.Fatalf("unexpected client proof %x", got)
	}
	if server := ServerProof(in); server == got {
		t.Fatal("client and server proofs must differ")
	}
}
