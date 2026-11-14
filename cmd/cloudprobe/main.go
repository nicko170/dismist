// Command cloudprobe is a research tool: it acts as a software AP33
// against a Mist-compatible AP endpoint (the Mist cloud, or a dismist
// controller) with a real AP's identity, and captures what the endpoint
// sends: the config reply, subscriptions, file and site-REST replies,
// ip6 frames and any controller-initiated message during -watch-seconds
// (design §5.6: how ap-cmd-rx and ep-config-change bodies would be
// captured against a lab Mist org).
//
// Everything it reads and writes is private: the AP secret, the hello
// template captured from a real AP, the trust bundle and the captures
// (which contain configs with passphrases). Keep them in a private
// directory outside the repository; nothing has a default path.
//
//	cloudprobe -ca private/cacerts.pem -secret private/secret.bin \
//	  -hello private/hello.json -out private/captures -watch-seconds 120
package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nicko170/dismist/internal/auth"
	"github.com/nicko170/dismist/internal/controller"
	"github.com/nicko170/dismist/internal/protocol"
)

func main() {
	url := flag.String("url", "wss://ep-terminator.mistsys.net/ws", "Mist AP control endpoint")
	caFile := flag.String("ca", "", "private copy of the AP controller trust bundle (PEM, required)")
	secretFile := flag.String("secret", "", "private 32-byte device secret (required)")
	helloFile := flag.String("hello", "", "private captured AP hello template (JSON, required)")
	ipRegisterFile := flag.String("ip-register", "", "private captured AP IP registration body (16 bytes, optional)")
	outDir := flag.String("out", "", "private capture directory outside the repository (required)")
	validationTime := flag.String("validation-time", "", "optional AP-compatible TLS verification time in RFC3339")
	watchSeconds := flag.Int("watch-seconds", 0, "seconds to capture cloud control messages after config")
	relayDir := flag.String("relay-telemetry-dir", "", "forward new private AP telemetry captures while watching")
	relayFramesDir := flag.String("relay-ap-frames-dir", "", "forward authenticated AP control frames while watching")
	requestUDP := flag.Bool("request-udp-setup", false, "capture Mist UDP setup parameters without using the physical AP")
	udpRekeyFrame := flag.String("udp-rekey-frame", "", "private captured AP UDP rekey frame to validate with Mist after UDP setup")
	answerUDPControl := flag.Bool("answer-udp-control", false, "answer Mist UDP rekey and test control messages in the software probe")
	softwareUDPTest := flag.Bool("software-udp-test", false, "send a fully software-generated encrypted UDP test to Mist")
	answerIP6Echo := flag.Bool("answer-ip6-echo", false, "answer Mist ICMPv6 echo requests for the software AP")
	ip6BridgeAddress := flag.String("ip6-bridge", "", "loopback packet bridge to an isolated virtual AP TCP/IP stack")
	flag.Parse()
	if *caFile == "" || *secretFile == "" || *helloFile == "" || *outDir == "" {
		fmt.Fprintln(os.Stderr, "cloud probe: -ca, -secret, -hello and -out are required (all private files)")
		os.Exit(2)
	}
	var verifyAt time.Time
	if *validationTime != "" {
		parsed, err := time.Parse(time.RFC3339, *validationTime)
		if err != nil {
			fmt.Fprintln(os.Stderr, "cloud probe: invalid validation time")
			os.Exit(1)
		}
		verifyAt = parsed
	}
	if err := run(*url, *caFile, *secretFile, *helloFile, *ipRegisterFile, *outDir, verifyAt, time.Duration(*watchSeconds)*time.Second, *relayDir, *relayFramesDir, *requestUDP, *udpRekeyFrame, *answerUDPControl, *softwareUDPTest, *answerIP6Echo, *ip6BridgeAddress); err != nil {
		fmt.Fprintln(os.Stderr, "cloud probe:", err)
		os.Exit(1)
	}
}

func run(endpoint, caFile, secretFile, helloFile, ipRegisterFile, outDir string, verifyAt time.Time, watchFor time.Duration, relayDir, relayFramesDir string, requestUDP bool, udpRekeyFrame string, answerUDPControl, softwareUDPTest, answerIP6Echo bool, ip6BridgeAddress string) error {
	if udpRekeyFrame != "" && !requestUDP {
		return errors.New("UDP rekey requires UDP setup")
	}
	if softwareUDPTest && (!requestUDP || !answerUDPControl || watchFor <= 0 || udpRekeyFrame != "") {
		return errors.New("software UDP test needs setup, control replies, watch time, and a fresh rekey")
	}
	if answerIP6Echo && ipRegisterFile == "" {
		return errors.New("IPv6 echo replies require the AP IP registration")
	}
	if ip6BridgeAddress != "" && (ipRegisterFile == "" || watchFor <= 0 || answerIP6Echo) {
		return errors.New("IPv6 bridge requires AP registration, watch time, and no separate echo responder")
	}
	secretBytes, err := os.ReadFile(secretFile)
	if err != nil || len(secretBytes) != 32 {
		return errors.New("cannot load 32-byte AP secret")
	}
	var secret [32]byte
	copy(secret[:], secretBytes)
	helloBytes, err := os.ReadFile(helloFile)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(helloBytes, &fields); err != nil {
		return err
	}
	var qc [32]byte
	if _, err := rand.Read(qc[:]); err != nil {
		return err
	}
	counter := uint64(time.Now().UnixNano())
	fields["counter"], _ = json.Marshal(counter)
	fields["QC"], _ = json.Marshal(qc[:])
	helloBytes, err = json.Marshal(fields)
	if err != nil {
		return err
	}
	var hello controller.Hello
	if err := json.Unmarshal(helloBytes, &hello); err != nil {
		return err
	}
	if hello.Version != 2 || hello.AuthType != "ap" || len(hello.QC) != 32 {
		return errors.New("hello template is not an AP v2 auth hello")
	}
	caBytes, err := os.ReadFile(caFile)
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caBytes) {
		return errors.New("no Mist root certificates loaded")
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	if !verifyAt.IsZero() {
		tlsConfig.Time = func() time.Time { return verifyAt }
	}
	dialer := websocket.Dialer{
		TLSClientConfig: tlsConfig,
		Subprotocols:    []string{"mist-ep-v0"}, HandshakeTimeout: 15 * time.Second,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, response, err := dialer.DialContext(ctx, endpoint, http.Header{"User-Agent": []string{"Go-http-client/1.1"}})
	if err != nil {
		if response != nil {
			return fmt.Errorf("WSS handshake status %d: %w", response.StatusCode, err)
		}
		return err
	}
	defer conn.Close()
	state := conn.UnderlyingConn().(*tls.Conn).ConnectionState()
	if len(state.PeerCertificates) == 0 || conn.Subprotocol() != "mist-ep-v0" {
		return errors.New("unexpected Mist TLS or WebSocket handshake")
	}
	cert := state.PeerCertificates[0].Raw
	fmt.Printf("WSS connected; TLS cipher %d\n", state.CipherSuite)
	var handleBytes [4]byte
	if _, err := rand.Read(handleBytes[:]); err != nil {
		return err
	}
	handle := uint64(binary.BigEndian.Uint32(handleBytes[:]))
	if err := writeFrame(conn, "ep-auth", handle, helloBytes); err != nil {
		return err
	}
	message, err := readSubject(conn, "ep-auth", handle, 15*time.Second)
	if err != nil {
		return err
	}
	var reply1 controller.Reply1
	if err := json.Unmarshal(message.Body, &reply1); err != nil {
		return err
	}
	if reply1.Version != 2 || reply1.Suite != 1 || len(reply1.QS) != 32 || len(reply1.RS) != 32 {
		return errors.New("unexpected Mist auth reply")
	}
	var qs [32]byte
	copy(qs[:], reply1.QS)
	serverContext := auth.Version2Context(auth.ContextInputs{
		Version: 2, ID: hello.ID, Model: hello.Model, Firmware: hello.Firmware,
		Suite: 1, Cert: cert, CipherSuite: state.CipherSuite, Entity: "SERVER",
	})
	expected := auth.ServerProof(auth.Inputs{
		Secret: secret, Counter: counter, QC: qc, QS: qs,
		Context: serverContext, DateTimeNS: reply1.DateTimeNS,
	})
	if !hmac.Equal(expected[:], reply1.RS) {
		return errors.New("Mist server proof did not verify")
	}
	clientContext := auth.Version2Context(auth.ContextInputs{
		Version: 2, ID: hello.ID, Model: hello.Model, Firmware: hello.Firmware,
		Suite: 1, Cert: cert, CipherSuite: state.CipherSuite, Entity: "CLIENT",
	})
	rc := clientProof(secret, counter, qc, qs, clientContext, reply1.DateTimeNS)
	reply2, _ := json.Marshal(controller.Reply2{
		Version: 2, NextDateTimeNS: reply1.DateTimeNS + 1,
		Authed: true, RC: rc[:],
	})
	if err := writeFrame(conn, "ep-auth", handle, reply2); err != nil {
		return err
	}
	message, err = readSubject(conn, "ep-auth", handle, 15*time.Second)
	if err != nil {
		return err
	}
	var reply3 controller.Reply3
	if err := json.Unmarshal(message.Body, &reply3); err != nil {
		return err
	}
	if !reply3.Authed || reply3.NextCounter != counter+1 {
		return errors.New("Mist rejected AP authentication")
	}
	fmt.Println("AP authentication accepted by Mist")
	var registeredAPIP []byte
	if ipRegisterFile != "" {
		body, err := os.ReadFile(ipRegisterFile)
		if err != nil || len(body) != 16 {
			return errors.New("cannot load AP IP registration")
		}
		if err := writeFrame(conn, "ep-ip-register", 0, body); err != nil {
			return err
		}
		registeredAPIP = append([]byte(nil), body...)
		fmt.Println("AP IP registration sent")
	}
	if _, err := rand.Read(handleBytes[:]); err != nil {
		return err
	}
	configHandle := uint64(binary.BigEndian.Uint32(handleBytes[:]))
	requestBody, _ := json.Marshal(map[string]any{
		"Firmware": hello.Firmware, "MfgCountryCode": "WW",
		"Tags": "emptydns,wpa1pairwise,allwlans", "Accept": "gz,json",
		"RecoveredFromBadConfig": false, "AppliedConfigTimestamp": 0,
	})
	if err := writeFrame(conn, "ep-config", configHandle, requestBody); err != nil {
		return err
	}
	message, err = readSubject(conn, "ep-config", configHandle, 15*time.Second)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(outDir, 0o700); err != nil {
		return err
	}
	name := fmt.Sprintf("%d", time.Now().UnixNano())
	if err := os.WriteFile(filepath.Join(outDir, name+"-config.raw"), message.Body, 0o600); err != nil {
		return err
	}
	decoded := message.Body
	if len(decoded) > 3 && decoded[0] == 2 && bytes.Equal(decoded[1:3], []byte{0x1f, 0x8b}) {
		reader, err := gzip.NewReader(bytes.NewReader(decoded[1:]))
		if err != nil {
			return err
		}
		decoded, err = io.ReadAll(io.LimitReader(reader, 4<<20))
		_ = reader.Close()
		if err != nil {
			return err
		}
	}
	if json.Valid(decoded) {
		if err := os.WriteFile(filepath.Join(outDir, name+"-config.json"), decoded, 0o600); err != nil {
			return err
		}
	}
	fmt.Printf("config reply captured: wire=%d bytes, decoded=%d bytes, JSON=%t\n", len(message.Body), len(decoded), json.Valid(decoded))
	var udpParameters udpSetupReply
	if requestUDP {
		if _, err := rand.Read(handleBytes[:]); err != nil {
			return err
		}
		udpHandle := uint64(binary.BigEndian.Uint32(handleBytes[:]))
		body, _ := json.Marshal(map[string]any{"version": 1, "Protocols": []int{1}, "MTU": 1500})
		if err := writeFrame(conn, "udp-setup", udpHandle, body); err != nil {
			return err
		}
		reply, err := readSubject(conn, "udp-setup", udpHandle, 15*time.Second)
		if err != nil {
			return err
		}
		parameters, err := parseUDPSetupReply(reply.Body)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(outDir, "udp-setup.json"), reply.Body, 0o600); err != nil {
			return err
		}
		fmt.Printf("Mist UDP setup captured: %s protocol %d\n", parameters.Address, parameters.Protocol)
		udpParameters = parameters
	}
	var udpTXSecret []byte
	if softwareUDPTest {
		udpTXSecret = make([]byte, 25)
		udpTXSecret[0] = 1
		if _, err := rand.Read(udpTXSecret[1:]); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(outDir, "udp-tx-key.bin"), udpTXSecret, 0o600); err != nil {
			return err
		}
		if _, err := rand.Read(handleBytes[:]); err != nil {
			return err
		}
		rekeyHandle := uint64(binary.BigEndian.Uint32(handleBytes[:]))
		body, err := json.Marshal(struct {
			Version   int    `json:"version"`
			KeyNumber int    `json:"keynum"`
			Secrets   []byte `json:"secrets"`
		}{Version: 2, KeyNumber: 1, Secrets: udpTXSecret})
		if err != nil {
			return err
		}
		if err := writeFrame(conn, "udp-rekey", rekeyHandle, body); err != nil {
			return err
		}
		reply, err := readSubject(conn, "udp-rekey", rekeyHandle, 15*time.Second)
		if err != nil {
			return err
		}
		ack, err := parseUDPRekeyReply(reply.Body, 1)
		if err != nil || !ack.OK {
			return errors.New("Mist did not accept software UDP rekey")
		}
		fmt.Println("Mist accepted software-generated UDP rekey")
	}
	if udpRekeyFrame != "" {
		raw, err := os.ReadFile(udpRekeyFrame)
		if err != nil {
			return err
		}
		frame, err := protocol.Decode(raw)
		if err != nil || frame.Subject != "udp-rekey" || frame.ServiceClass != 2 {
			return errors.New("invalid captured UDP rekey frame")
		}
		var request struct {
			Version   int    `json:"version"`
			KeyNumber int    `json:"keynum"`
			Secrets   []byte `json:"secrets"`
		}
		if err := json.Unmarshal(frame.Body, &request); err != nil || request.Version != 2 || len(request.Secrets) != 25 {
			return errors.New("invalid captured UDP rekey request")
		}
		if _, err := rand.Read(handleBytes[:]); err != nil {
			return err
		}
		rekeyHandle := uint64(binary.BigEndian.Uint32(handleBytes[:]))
		if err := writeFrame(conn, "udp-rekey", rekeyHandle, frame.Body); err != nil {
			return err
		}
		reply, err := readSubject(conn, "udp-rekey", rekeyHandle, 15*time.Second)
		if err != nil {
			return err
		}
		ack, err := parseUDPRekeyReply(reply.Body, request.KeyNumber)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(outDir, "udp-rekey.json"), reply.Body, 0o600); err != nil {
			return err
		}
		fmt.Printf("Mist UDP rekey acknowledged: %t\n", ack.OK)
	}
	var bridge *ip6Bridge
	if ip6BridgeAddress != "" {
		bridge, err = newIP6Bridge(ip6BridgeAddress, registeredAPIP)
		if err != nil {
			return err
		}
		fmt.Println("isolated virtual AP IPv6 bridge connected")
	}
	if watchFor > 0 {
		var configInfo struct {
			SiteID string `json:"SiteID"`
		}
		if json.Unmarshal(decoded, &configInfo) == nil && configInfo.SiteID != "" {
			for _, item := range []struct{ topic, key string }{
				{"site-connected-wc-", configInfo.SiteID},
				{"rogue-clients-request-", hello.ID},
			} {
				body, err := subscriptionRequest(item.topic, item.key, uint64(time.Now().UnixNano()))
				if err != nil {
					return err
				}
				if _, err := rand.Read(handleBytes[:]); err != nil {
					return err
				}
				if err := writeFrame(conn, "ep-subscribe", uint64(binary.BigEndian.Uint32(handleBytes[:])), body); err != nil {
					return err
				}
			}
			fmt.Println("AP site and client-control subscriptions sent")
		}
		if softwareUDPTest {
			stop := make(chan struct{})
			done := make(chan struct{})
			go func() {
				defer close(done)
				runUDPSoftwareProbe(udpParameters, udpTXSecret, outDir, stop)
			}()
			err := watchCloud(conn, outDir, watchFor, relayDir, relayFramesDir, requestUDP, answerUDPControl, registeredAPIP, answerIP6Echo, bridge)
			close(stop)
			<-done
			return err
		}
		return watchCloud(conn, outDir, watchFor, relayDir, relayFramesDir, requestUDP, answerUDPControl, registeredAPIP, answerIP6Echo, bridge)
	}
	return nil
}

type udpSetupReply struct {
	Version  int    `json:"version"`
	Address  string `json:"address"`
	Protocol int    `json:"Protocol"`
	AAD      []byte `json:"aad"`
}

func parseUDPSetupReply(body []byte) (udpSetupReply, error) {
	var reply udpSetupReply
	if err := json.Unmarshal(body, &reply); err != nil {
		return reply, err
	}
	host, portText, err := net.SplitHostPort(reply.Address)
	if err != nil {
		return reply, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || host == "" || port < 1 || port > 65535 ||
		reply.Version != 1 || reply.Protocol != 1 || len(reply.AAD) != 8 {
		return reply, errors.New("invalid UDP setup reply")
	}
	return reply, nil
}

type udpRekeyReply struct {
	Version   int  `json:"version"`
	KeyNumber int  `json:"keynum"`
	OK        bool `json:"ok"`
}

func parseUDPRekeyReply(body []byte, expectedKeyNumber int) (udpRekeyReply, error) {
	var reply udpRekeyReply
	if err := json.Unmarshal(body, &reply); err != nil {
		return reply, err
	}
	if reply.Version != 2 || reply.KeyNumber != expectedKeyNumber {
		return reply, errors.New("invalid UDP rekey reply")
	}
	return reply, nil
}

func udpControlReply(subject string, body []byte) ([]byte, bool, error) {
	switch subject {
	case "udp-test":
		var request struct {
			Version int    `json:"version"`
			Blob    []byte `json:"blob"`
			Reply   bool   `json:"reply"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, false, err
		}
		if request.Reply {
			return nil, false, nil
		}
		if request.Version != 3 || len(request.Blob) != 16 {
			return nil, false, errors.New("invalid UDP test request")
		}
		reply, err := json.Marshal(struct {
			Version int    `json:"version"`
			Blob    []byte `json:"blob"`
			Reply   bool   `json:"reply"`
		}{Version: 3, Blob: request.Blob, Reply: true})
		return reply, true, err
	case "udp-rekey":
		var request struct {
			Version   int    `json:"version"`
			KeyNumber int    `json:"keynum"`
			Secrets   []byte `json:"secrets"`
			OK        bool   `json:"ok"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, false, err
		}
		if len(request.Secrets) == 0 {
			return nil, false, nil
		}
		if request.Version != 2 || request.KeyNumber < 0 || len(request.Secrets) != 25 {
			return nil, false, errors.New("invalid UDP rekey request")
		}
		reply, err := json.Marshal(udpRekeyReply{Version: 2, KeyNumber: request.KeyNumber, OK: true})
		return reply, true, err
	default:
		return nil, false, nil
	}
}

func subscriptionRequest(topic, key string, id uint64) ([]byte, error) {
	return json.Marshal(struct {
		ID        uint64 `json:"ID"`
		Topic     string `json:"Topic"`
		Partition int    `json:"Partition"`
		Key       string `json:"Key"`
		Offset    int    `json:"Offset"`
	}{ID: id, Topic: topic, Partition: -1, Key: key, Offset: -1})
}

func watchCloud(conn *websocket.Conn, outDir string, duration time.Duration, relayDir, relayFramesDir string, skipRelayedUDPSetup, answerUDPControl bool, registeredAPIP []byte, answerIP6Echo bool, bridge *ip6Bridge) error {
	deadline := time.Now().Add(duration)
	fmt.Printf("watching cloud messages for %s\n", duration)
	messageCounts := make(map[string]int)
	echoCount := 0
	var writeMu sync.Mutex
	bridgeDone := make(chan struct{})
	if bridge != nil {
		go func() { defer close(bridgeDone); bridge.readFromVirtualAP(conn, &writeMu, outDir) }()
	} else {
		close(bridgeDone)
	}
	defer func() {
		if bridge != nil {
			_ = bridge.Close()
		}
		<-bridgeDone
	}()
	stopRelay := make(chan struct{})
	relayDone := make(chan struct{})
	if relayFramesDir != "" {
		go func() {
			defer close(relayDone)
			relayAPFrames(conn, relayFramesDir, stopRelay, &writeMu, skipRelayedUDPSetup)
		}()
	} else if relayDir != "" {
		go func() {
			defer close(relayDone)
			relayTelemetry(conn, relayDir, stopRelay, &writeMu)
		}()
	} else {
		close(relayDone)
	}
	defer func() { close(stopRelay); <-relayDone }()
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(deadline)
		messageType, raw, err := conn.ReadMessage()
		if err != nil {
			fmt.Printf("cloud watch ended: %v\n", err)
			return nil
		}
		if messageType != websocket.BinaryMessage {
			continue
		}
		message, err := protocol.Decode(raw)
		if err != nil {
			return err
		}
		subject := make([]rune, 0, len(message.Subject))
		for _, char := range message.Subject {
			if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-' {
				subject = append(subject, char)
			}
		}
		name := fmt.Sprintf("%d-%s.bin", time.Now().UnixNano(), string(subject))
		if err := os.WriteFile(filepath.Join(outDir, name), raw, 0o600); err != nil {
			return err
		}
		messageCounts[message.Subject]++
		quiet := message.Subject == "ip6" || message.Subject == "ep-subscribe" || message.Subject == "ep-config" ||
			message.Subject == "ping" || message.Subject == "udp-setup" || message.Subject == "ep-file" || message.Subject == "ep-telemetry"
		if !quiet || messageCounts[message.Subject] <= 2 || messageCounts[message.Subject]%50 == 0 {
			fmt.Printf("cloud message: subject=%s class=%d bytes=%d count=%d\n", message.Subject, message.ServiceClass, len(message.Body), messageCounts[message.Subject])
		}
		if message.Subject == "ping" && message.ServiceClass == 2 {
			writeMu.Lock()
			err := writeFrameClass(conn, 2, message.Subject, message.Handle, message.Body)
			writeMu.Unlock()
			if err != nil {
				return err
			}
		}
		if answerUDPControl && message.ServiceClass == 2 {
			body, answered, err := udpControlReply(message.Subject, message.Body)
			if err != nil {
				return err
			}
			if answered {
				writeMu.Lock()
				err = writeFrameClass(conn, 2, message.Subject, message.Handle, body)
				writeMu.Unlock()
				if err != nil {
					return err
				}
				fmt.Printf("answered Mist UDP control: %s\n", message.Subject)
			}
		}
		if answerIP6Echo && message.Subject == "ip6" && message.ServiceClass == 1 {
			reply, answered, err := icmpv6EchoReply(message.Body, registeredAPIP)
			if err != nil {
				return err
			}
			if answered {
				writeMu.Lock()
				err = writeFrameClass(conn, 1, "ip6", message.Handle, reply)
				writeMu.Unlock()
				if err != nil {
					return err
				}
				echoCount++
				if echoCount <= 2 || echoCount%50 == 0 {
					fmt.Printf("answered Mist ICMPv6 echo: count=%d\n", echoCount)
				}
			}
		}
		if bridge != nil && message.Subject == "ip6" && message.ServiceClass == 1 {
			if err := bridge.sendToVirtualAP(message.Body); err != nil {
				return err
			}
		}
	}
	return nil
}

func relayAllowed(subject string) bool {
	return subject != "ep-auth" && subject != "get-time" && subject != ""
}

func relayAPFrames(conn *websocket.Conn, dir string, stop <-chan struct{}, writeMu *sync.Mutex, skipUDPSetup bool) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	last := ""
	count := 0
	for {
		select {
		case <-stop:
			fmt.Printf("AP frame relay sent %d frames\n", count)
			return
		case <-ticker.C:
			files, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			names := make([]string, 0, len(files))
			for _, file := range files {
				if !file.IsDir() && file.Name() > last {
					names = append(names, file.Name())
				}
			}
			sort.Strings(names)
			if len(names) > 25 {
				names = names[:25]
			}
			for _, name := range names {
				raw, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil {
					continue
				}
				message, err := protocol.Decode(raw)
				last = name
				if err != nil || !relayAllowed(message.Subject) || (skipUDPSetup && message.Subject == "udp-setup") {
					continue
				}
				writeMu.Lock()
				_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				err = conn.WriteMessage(websocket.BinaryMessage, raw)
				writeMu.Unlock()
				if err != nil {
					fmt.Printf("AP frame relay stopped: %v\n", err)
					return
				}
				count++
			}
		}
	}
}

func telemetryClass(body []byte) (byte, error) {
	if len(body) == 0 {
		return 0, errors.New("empty telemetry")
	}
	if body[0] == 'P' {
		return 0, nil
	}
	if body[0] == '{' || len(body) >= 3 && body[0] == 2 && body[1] == 0x1f && body[2] == 0x8b {
		return 2, nil
	}
	return 0, errors.New("unrecognized telemetry body")
}

func relayTelemetry(conn *websocket.Conn, dir string, stop <-chan struct{}, writeMu *sync.Mutex) {
	files, err := os.ReadDir(dir)
	if err != nil {
		fmt.Printf("telemetry relay unavailable: %v\n", err)
		return
	}
	last := ""
	for _, file := range files {
		if file.Name() > last {
			last = file.Name()
		}
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	count := 0
	for {
		select {
		case <-stop:
			fmt.Printf("telemetry relay sent %d frames\n", count)
			return
		case <-ticker.C:
			files, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			names := make([]string, 0, len(files))
			for _, file := range files {
				if file.Name() > last && !file.IsDir() {
					names = append(names, file.Name())
				}
			}
			sort.Strings(names)
			if len(names) > 20 {
				names = names[:20]
			}
			for _, name := range names {
				body, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil {
					continue
				}
				class, err := telemetryClass(body)
				if err != nil {
					last = name
					continue
				}
				writeMu.Lock()
				err = writeFrameClass(conn, class, "ep-telemetry", 0, body)
				writeMu.Unlock()
				if err != nil {
					fmt.Printf("telemetry relay stopped: %v\n", err)
					return
				}
				last = name
				count++
			}
		}
	}
}

func writeFrame(conn *websocket.Conn, subject string, handle uint64, body []byte) error {
	return writeFrameClass(conn, 2, subject, handle, body)
}

func writeFrameClass(conn *websocket.Conn, serviceClass byte, subject string, handle uint64, body []byte) error {
	frame, err := protocol.Encode(protocol.Message{
		Version: 1, ServiceClass: serviceClass, Subject: subject, Handle: handle, Body: body,
	})
	if err != nil {
		return err
	}
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return conn.WriteMessage(websocket.BinaryMessage, frame)
}

func readSubject(conn *websocket.Conn, subject string, handle uint64, timeout time.Duration) (protocol.Message, error) {
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	for {
		typeCode, frame, err := conn.ReadMessage()
		if err != nil {
			return protocol.Message{}, err
		}
		if typeCode != websocket.BinaryMessage {
			continue
		}
		message, err := protocol.Decode(frame)
		if err != nil {
			return protocol.Message{}, err
		}
		if message.Subject == subject && message.Handle == handle {
			return message, nil
		}
		fmt.Printf("other cloud message: subject=%s bytes=%d\n", message.Subject, len(message.Body))
	}
}
