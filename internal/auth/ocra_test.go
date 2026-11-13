package auth

import (
	"encoding/hex"
	"testing"
)

func TestServerProofMatchesIndependentVector(t *testing.T) {
	var in Inputs
	for i := range in.Secret {
		in.Secret[i] = byte(i)
		in.QC[i] = byte(i + 32)
		in.QS[i] = byte(i + 64)
		in.Context[i] = byte(i + 96)
	}
	in.Counter = 42
	in.DateTimeNS = 1234567890123456789
	want, _ := hex.DecodeString("d4428832a4593e2d689f583d23b41157048fe9d0abff571f1fd1dc20b44fd170")
	got := ServerProof(in)
	if hex.EncodeToString(got[:]) != hex.EncodeToString(want) {
		t.Fatalf("unexpected proof: %x", got)
	}
}
