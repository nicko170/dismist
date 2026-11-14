package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type ip6Bridge struct {
	conn net.Conn
	ap   []byte
	mu   sync.Mutex
}

func newIP6Bridge(address string, registeredAP []byte) (*ip6Bridge, error) {
	if len(registeredAP) != 16 {
		return nil, errors.New("IPv6 bridge requires a registered AP address")
	}
	if !bridgeAddressAllowed(address) {
		return nil, errors.New("IPv6 bridge must be a loopback address")
	}
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return &ip6Bridge{conn: conn, ap: append([]byte(nil), registeredAP...)}, nil
}

func bridgeAddressAllowed(address string) bool {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	port, err := strconv.Atoi(portText)
	return err == nil && ip != nil && ip.IsLoopback() && port >= 1 && port <= 65535
}

func validBridgeInbound(packet, ap []byte) bool {
	return len(ap) == 16 && len(packet) >= 40 && packet[0]>>4 == 6 &&
		int(binary.BigEndian.Uint16(packet[4:6])) == len(packet)-40 &&
		bytes.Equal(packet[24:40], ap) && bytes.Equal(packet[8:16], ap[:8])
}

func validBridgeOutbound(packet, ap []byte) bool {
	return len(ap) == 16 && len(packet) >= 40 && packet[0]>>4 == 6 &&
		int(binary.BigEndian.Uint16(packet[4:6])) == len(packet)-40 &&
		bytes.Equal(packet[8:24], ap) && bytes.Equal(packet[24:32], ap[:8])
}

func (b *ip6Bridge) sendToVirtualAP(packet []byte) error {
	if !validBridgeInbound(packet, b.ap) {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(packet)))
	if err := writeBridgeBytes(b.conn, length[:]); err != nil {
		return err
	}
	return writeBridgeBytes(b.conn, packet)
}

func writeBridgeBytes(conn net.Conn, raw []byte) error {
	for len(raw) > 0 {
		n, err := conn.Write(raw)
		if err != nil {
			return err
		}
		raw = raw[n:]
	}
	return nil
}

func (b *ip6Bridge) readFromVirtualAP(ws *websocket.Conn, writeMu *sync.Mutex, outDir string) {
	var length [4]byte
	count := 0
	for {
		if _, err := io.ReadFull(b.conn, length[:]); err != nil {
			return
		}
		size := binary.BigEndian.Uint32(length[:])
		if size < 40 || size > 65535 {
			fmt.Printf("virtual AP sent invalid IPv6 length %d\n", size)
			return
		}
		packet := make([]byte, size)
		if _, err := io.ReadFull(b.conn, packet); err != nil {
			return
		}
		if !validBridgeOutbound(packet, b.ap) {
			continue
		}
		count++
		if count <= 5000 {
			name := fmt.Sprintf("%d-ip6-out.bin", time.Now().UnixNano())
			if err := os.WriteFile(filepath.Join(outDir, name), packet, 0o600); err != nil {
				fmt.Printf("cannot capture virtual AP packet: %v\n", err)
			}
		}
		writeMu.Lock()
		err := writeFrameClass(ws, 1, "ip6", 0, packet)
		writeMu.Unlock()
		if err != nil {
			fmt.Printf("virtual AP WSS send stopped: %v\n", err)
			return
		}
		if count <= 5 || count%50 == 0 {
			fmt.Printf("virtual AP IPv6 response: next=%d bytes=%d count=%d\n", packet[6], len(packet), count)
		}
	}
}

func (b *ip6Bridge) Close() error {
	return b.conn.Close()
}
