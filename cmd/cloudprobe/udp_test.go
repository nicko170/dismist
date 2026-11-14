package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/nicko170/dismist/internal/protocol"
)

func TestUDPPacketFramingAndAuthentication(t *testing.T) {
	secret := make([]byte, 25)
	secret[0] = 1
	for i := 1; i < len(secret); i++ {
		secret[i] = byte(i)
	}
	aad := []byte("Example!")
	frame, err := protocol.Encode(protocol.Message{Version: 1, ServiceClass: 2,
		Subject: "udp-test", Handle: 42, Body: []byte(`{"version":3,"reply":false}`)})
	if err != nil {
		t.Fatal(err)
	}
	packet, err := sealUDPPacket(secret, aad, 3, frame)
	if err != nil {
		t.Fatal(err)
	}
	if len(packet) != len(frame)+28 || !bytes.Equal(packet[:4], []byte{0, 0, 0, 3}) ||
		!bytes.Equal(packet[len(packet)-8:], aad) {
		t.Fatalf("unexpected UDP wire framing: %d bytes", len(packet))
	}
	decoded, err := openUDPPacket(secret, aad, packet)
	if err != nil || !bytes.Equal(decoded, frame) {
		t.Fatalf("UDP packet did not authenticate: %v", err)
	}
	if _, err := openUDPPacket(secret, []byte("WRONGAAD"), packet); err == nil {
		t.Fatal("UDP packet accepted wrong authenticated trailer")
	}
}

func TestPrivateCapturedAPUDPPacket(t *testing.T) {
	dir := os.Getenv("DISMIST_PRIVATE_EVIDENCE")
	if dir == "" {
		t.Skip("DISMIST_PRIVATE_EVIDENCE not set (private local AP capture)")
	}
	files, err := filepath.Glob(filepath.Join(dir, "ap-frames-udp-probe", "*.bin"))
	if err != nil {
		t.Fatal(err)
	}
	var secret []byte
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		message, err := protocol.Decode(raw)
		if err != nil || message.Subject != "udp-rekey" {
			continue
		}
		var request struct {
			Secrets []byte `json:"secrets"`
		}
		if err := json.Unmarshal(message.Body, &request); err != nil {
			t.Fatal(err)
		}
		secret = request.Secrets
		break
	}
	if len(secret) != 25 {
		t.Fatal("missing private rekey capture")
	}
	packet, err := os.ReadFile(filepath.Join(dir, "udp-probe", "packet-0001.bin"))
	if err != nil {
		t.Fatal(err)
	}
	aad := []byte(os.Getenv("DISMIST_PRIVATE_UDP_AAD")) // the 8-byte AAD the capture used
	plaintext, err := openUDPPacket(secret, aad, packet)
	if err != nil {
		t.Fatalf("cannot decrypt genuine AP packet: %v", err)
	}
	message, err := protocol.Decode(plaintext)
	if err != nil || message.Subject != "udp-test" {
		t.Fatalf("wrong genuine AP payload: %+v %v", message, err)
	}
	rebuilt, err := sealUDPPacket(secret, aad, binary.BigEndian.Uint32(packet[:4]), plaintext)
	if err != nil || !bytes.Equal(rebuilt, packet) {
		t.Fatalf("software packet differs from AP packet: %v", err)
	}
}

func TestUDPTestFrameIsPadded(t *testing.T) {
	frame, err := makeUDPTestFrame((1<<32)|42, bytes.Repeat([]byte{7}, 16))
	if err != nil {
		t.Fatal(err)
	}
	message, err := protocol.Decode(frame)
	if err != nil || message.Subject != "udp-test" || len(message.Body) != 1428 || len(frame) != 1444 {
		t.Fatalf("wrong UDP test frame: %+v %v", message, err)
	}
	var test struct {
		Version int    `json:"version"`
		Blob    []byte `json:"blob"`
		Reply   bool   `json:"reply"`
	}
	if err := json.Unmarshal(message.Body, &test); err != nil || test.Version != 3 ||
		test.Reply || !bytes.Equal(test.Blob, bytes.Repeat([]byte{7}, 16)) {
		t.Fatalf("wrong UDP test content: %+v %v", test, err)
	}
}
