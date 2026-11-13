package controller

import (
	"encoding/hex"
	"testing"
)

func testAuthFixture() (Authenticator, Hello, [32]byte, uint64) {
	var secret, qs [32]byte
	qc := make([]byte, 32)
	for i := 0; i < 32; i++ {
		secret[i] = byte(i)
		qc[i] = byte(i + 32)
		qs[i] = byte(i + 64)
	}
	return Authenticator{ID: "ap-1", Secret: secret, ServerName: "example-controller"}, Hello{
		Version: 2, ID: "ap-1", AuthType: "ap", Model: "AP33-WW",
		Firmware: "fw-1", Suite: 1, Counter: 42, QC: qc, ENV: "production",
	}, qs, 1234567890123456789
}

func TestReply1MatchesIndependentProofVector(t *testing.T) {
	a, hello, qs, now := testAuthFixture()
	reply, err := a.Reply1(hello, qs, now)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(reply.RS) != "3af9c659cbc74dd4741d9fc26421cee19a792124ec96b07c57fd71e33b234ac3" {
		t.Fatalf("short server proof: %x", reply.RS)
	}
	if reply.Version != 2 || reply.Suite != 1 || reply.DateTimeNS != now || reply.ServerName != "example-controller" {
		t.Fatalf("short reply fields: %+v", reply)
	}
}

func TestReply3VerifiesClientAndAdvancesCounter(t *testing.T) {
	a, hello, qs, now := testAuthFixture()
	reply1, _ := a.Reply1(hello, qs, now)
	rc, _ := hex.DecodeString("023850a74f03051f70959826190422f473d760550f3ebbd0601eb7847b148fe2")
	reply2 := Reply2{Version: 2, NextDateTimeNS: now + 1, Authed: true, RC: rc}
	reply3, err := a.Reply3(hello, reply1, reply2)
	if err != nil {
		t.Fatal(err)
	}
	if !reply3.Authed || reply3.NextCounter != 43 {
		t.Fatalf("short final auth result: %+v", reply3)
	}
	reply2.RC = make([]byte, 32)
	if _, err := a.Reply3(hello, reply1, reply2); err == nil {
		t.Fatal("accepted short client proof")
	}
}

func TestReply1RejectsUnknownAP(t *testing.T) {
	a, hello, qs, now := testAuthFixture()
	hello.ID = "other"
	if _, err := a.Reply1(hello, qs, now); err == nil {
		t.Fatal("accepted unknown AP")
	}
}
