package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/nicko170/dismist/internal/protocol"
)

func udpGCM(secret []byte) (cipher.AEAD, error) {
	if len(secret) != 25 || secret[0] != 1 {
		return nil, errors.New("invalid UDP key material")
	}
	block, err := aes.NewCipher(secret[1:17])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func sealUDPPacket(secret, aad []byte, sequence uint32, plaintext []byte) ([]byte, error) {
	if len(aad) != 8 {
		return nil, errors.New("UDP setup AAD must be eight bytes")
	}
	gcm, err := udpGCM(secret)
	if err != nil {
		return nil, err
	}
	var nonce [12]byte
	copy(nonce[:8], secret[17:25])
	binary.BigEndian.PutUint32(nonce[8:], sequence)
	packet := make([]byte, 4, 4+len(plaintext)+gcm.Overhead()+len(aad))
	binary.BigEndian.PutUint32(packet[:4], sequence)
	packet = gcm.Seal(packet, nonce[:], plaintext, aad)
	packet = append(packet, aad...)
	return packet, nil
}

func openUDPPacket(secret, aad, packet []byte) ([]byte, error) {
	if len(aad) != 8 || len(packet) < 4+16+8 || !bytes.Equal(packet[len(packet)-8:], aad) {
		return nil, errors.New("invalid UDP packet framing")
	}
	gcm, err := udpGCM(secret)
	if err != nil {
		return nil, err
	}
	var nonce [12]byte
	copy(nonce[:8], secret[17:25])
	copy(nonce[8:], packet[:4])
	return gcm.Open(nil, nonce[:], packet[4:len(packet)-8], aad)
}

func makeUDPTestFrame(handle uint64, blob []byte) ([]byte, error) {
	if len(blob) != 16 {
		return nil, errors.New("UDP test blob must be 16 bytes")
	}
	body, err := json.Marshal(struct {
		Version int    `json:"version"`
		Blob    []byte `json:"blob"`
		Reply   bool   `json:"reply"`
	}{Version: 3, Blob: blob, Reply: false})
	if err != nil {
		return nil, err
	}
	if len(body) < 1428 {
		body = append(body, bytes.Repeat([]byte{' '}, 1428-len(body))...)
	}
	return protocol.Encode(protocol.Message{
		Version: 1, ServiceClass: 2, Subject: "udp-test", Handle: handle, Body: body,
	})
}

func runUDPSoftwareProbe(setup udpSetupReply, secret []byte, outDir string, stop <-chan struct{}) {
	address, err := net.ResolveUDPAddr("udp4", setup.Address)
	if err != nil {
		fmt.Printf("software UDP address failed: %v\n", err)
		return
	}
	conn, err := net.DialUDP("udp4", nil, address)
	if err != nil {
		fmt.Printf("software UDP dial failed: %v\n", err)
		return
	}
	defer conn.Close()
	blob := make([]byte, 16)
	var handleBytes [4]byte
	if _, err := rand.Read(blob); err != nil {
		return
	}
	if _, err := rand.Read(handleBytes[:]); err != nil {
		return
	}
	frame, err := makeUDPTestFrame((uint64(1)<<32)|uint64(binary.BigEndian.Uint32(handleBytes[:])), blob)
	if err != nil {
		fmt.Printf("software UDP test frame failed: %v\n", err)
		return
	}
	buffer := make([]byte, 65535)
	for attempt := 0; attempt < 12; attempt++ {
		select {
		case <-stop:
			return
		default:
		}
		sequence := uint32(3)
		if attempt >= 5 {
			sequence = uint32(5 + 2*(attempt-5))
		}
		packet, err := sealUDPPacket(secret, setup.AAD, sequence, frame)
		if err != nil {
			fmt.Printf("software UDP seal failed: %v\n", err)
			return
		}
		if len(packet) > 1472 {
			fmt.Printf("software UDP packet exceeds AP MTU: %d bytes\n", len(packet))
			return
		}
		if _, err := conn.Write(packet); err != nil {
			fmt.Printf("software UDP send failed: %v\n", err)
			return
		}
		fmt.Printf("software UDP test sent: attempt=%d bytes=%d\n", attempt+1, len(packet))
		_ = conn.SetReadDeadline(time.Now().Add(800 * time.Millisecond))
		n, err := conn.Read(buffer)
		if n > 0 {
			name := fmt.Sprintf("%d-udp-reply.bin", time.Now().UnixNano())
			if writeErr := os.WriteFile(filepath.Join(outDir, name), buffer[:n], 0o600); writeErr != nil {
				fmt.Printf("software UDP capture failed: %v\n", writeErr)
			}
			fmt.Printf("Mist UDP reply captured: %d bytes\n", n)
			return
		}
		if err != nil {
			if _, ok := err.(net.Error); ok {
				continue
			}
			fmt.Printf("software UDP read failed: %v\n", err)
			return
		}
	}
	fmt.Println("Mist UDP test received no datagram")
}
