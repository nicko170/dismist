package main

import (
	"bytes"
	"encoding/binary"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/nicko170/dismist/internal/protocol"
)

func TestICMPv6EchoReply(t *testing.T) {
	source := net.ParseIP("fd00:db8:0:1::2").To16()
	destination := net.ParseIP("fd00:db8:0:1::37").To16()
	packet := make([]byte, 40+8+4)
	packet[0] = 0x60
	binary.BigEndian.PutUint16(packet[4:6], uint16(len(packet)-40))
	packet[6], packet[7] = 58, 64
	copy(packet[8:24], source)
	copy(packet[24:40], destination)
	packet[40] = 128
	packet[44], packet[45], packet[46], packet[47] = 1, 2, 3, 4
	copy(packet[48:], []byte("test"))
	binary.BigEndian.PutUint16(packet[42:44], icmpv6Checksum(packet))
	reply, ok, err := icmpv6EchoReply(packet, destination)
	if err != nil || !ok || reply[40] != 129 ||
		!bytes.Equal(reply[8:24], destination) || !bytes.Equal(reply[24:40], source) ||
		icmpv6Checksum(reply) != 0 {
		t.Fatalf("bad IPv6 echo reply: ok=%t err=%v", ok, err)
	}
	if _, ok, err := icmpv6EchoReply(packet, source); err != nil || ok {
		t.Fatal("answered packet for a different AP address")
	}
}

func TestPrivateCapturedMistEchoRequest(t *testing.T) {
	dir := os.Getenv("DISMIST_PRIVATE_EVIDENCE")
	if dir == "" {
		t.Skip("DISMIST_PRIVATE_EVIDENCE not set (private Mist AP tunnel capture)")
	}
	apAddress, err := os.ReadFile(filepath.Join(dir, "cloudprobe-ip-register.bin"))
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "cloudprobe-014-consistent", "*-ip6.bin"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		message, err := protocol.Decode(raw)
		if err != nil {
			t.Fatal(err)
		}
		reply, ok, err := icmpv6EchoReply(message.Body, apAddress)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			continue
		}
		if reply[40] != 129 || icmpv6Checksum(reply) != 0 {
			t.Fatal("invalid response to genuine Mist echo")
		}
		return
	}
	t.Fatal("no Mist echo request to registered AP address found")
}

func TestIP6BridgeScopesPacketsToRegisteredAP(t *testing.T) {
	ap := net.ParseIP("fd00:db8:0:1:0:2aa:0:1").To16()
	cloud := net.ParseIP("fd00:db8:0:1:1:2:3:4").To16()
	packet := make([]byte, 40)
	packet[0] = 0x60
	copy(packet[8:24], cloud)
	copy(packet[24:40], ap)
	if !validBridgeInbound(packet, ap) || validBridgeOutbound(packet, ap) {
		t.Fatal("Mist packet was not scoped to AP destination")
	}
	copy(packet[8:24], ap)
	copy(packet[24:40], cloud)
	if !validBridgeOutbound(packet, ap) || validBridgeInbound(packet, ap) {
		t.Fatal("software AP packet was not scoped to AP source")
	}
	packet[0] = 0x40
	if validBridgeInbound(packet, ap) || validBridgeOutbound(packet, ap) {
		t.Fatal("non-IPv6 packet entered bridge")
	}
}

func TestIP6BridgeRequiresLoopback(t *testing.T) {
	if !bridgeAddressAllowed("127.0.0.1:9233") || !bridgeAddressAllowed("[::1]:9233") {
		t.Fatal("rejected loopback packet bridge")
	}
	for _, address := range []string{"192.0.2.1:9233", "example.com:9233", "127.0.0.1:0"} {
		if bridgeAddressAllowed(address) {
			t.Fatalf("allowed nonlocal packet bridge %q", address)
		}
	}
}
