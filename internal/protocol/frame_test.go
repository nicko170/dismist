package protocol

import (
	"bytes"
	"testing"
)

func TestDecodeCapturedShapeAndEncodeRoundTrip(t *testing.T) {
	raw := append([]byte{1, 2, 8, 'g', 'e', 't', '-', 't', 'i', 'm', 'e', 0xac, 0x02}, []byte{0, 1, 2, 3}...)
	got, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || got.ServiceClass != 2 || got.Subject != "get-time" || got.Handle != 300 || !bytes.Equal(got.Body, []byte{0, 1, 2, 3}) {
		t.Fatalf("unexpected decoded frame: %+v", got)
	}
	encoded, err := Encode(got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, raw) {
		t.Fatalf("round trip mismatch: %x != %x", encoded, raw)
	}
}

func TestDecodeRejectsTruncatedAndOverflowingFrames(t *testing.T) {
	cases := [][]byte{
		{1, 2, 0x80},
		{1, 2, 5, 'a', 'b'},
		{1, 2, 1, 'x', 0x80},
		{1, 2, 1, 'x', 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02},
	}
	for _, raw := range cases {
		if _, err := Decode(raw); err == nil {
			t.Fatalf("accepted malformed frame %x", raw)
		}
	}
}

func TestEncodeRejectsNonASCIIAndOversizeSubject(t *testing.T) {
	for _, subject := range []string{"é", string(bytes.Repeat([]byte{'x'}, 257))} {
		if _, err := Encode(Message{Version: 1, ServiceClass: 2, Subject: subject}); err == nil {
			t.Fatalf("accepted invalid subject %q", subject)
		}
	}
}
