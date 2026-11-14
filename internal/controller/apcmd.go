package controller

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/nicko170/dismist/internal/protocol"
	"github.com/nicko170/dismist/internal/telemetry"
)

// AP commands over ap-cmd-rx (design §5.6 "Actions").
//
// Wire format, recovered statically from the 0.15 ep-control binary
// (apfw-0.15.35064): main.main registers the subject "ap-cmd-rx" with the
// connector and hands each message (connector.Msg {Body, Handle, Sla}) to
// apcmd.HandleApCmd, which unmarshals the body into
//
//	struct { Cmd string; Params json.RawMessage; Clients []ethernet.MAC;
//	         Sender string; Reason string }
//
// ("Can't unmarshal ap_cmd_rx %s" on failure), logs "%s cmd from %s
// because %s" (Cmd, Sender, Reason) and switches on Cmd:
//
//	reboot        Params struct { Reason string } (default "unknown"); an
//	              unmarshal failure is logged ("Can't unmarshal reboot
//	              params") and the AP reboots anyway: "Rx reboot command",
//	              "Reboot reason: %s", apcmd.rebootAP
//	disconnect    Params struct { Clients []ethernet.MAC; Reason int }
//	radio_reinit  Params struct { Radio string }
//	ping          Params struct { Host string; AddrFamily int; Count int;
//	              Session string }, output on ap-cmd-output-
//	traceroute    Params struct { Host string; AddrFamily int; Session
//	              string; Protocol string; Port uint16 }
//	arp           Params struct { Session string }
//
// and others dismist never sends (zeroize, robo_reset, switch-cloud,
// fips-zeroize, pprof-report, esl-cmd, uwb-cmd, ...). ethernet.MAC
// marshals as "aa-bb-cc-dd-ee-ff". The connector dispatches a cloud
// message by subject; a non-zero handle that matches none of the AP's own
// requests is logged as "failed to find conversation", so commands use
// handle 0, and service class 2 like every captured cloud message.
//
// None of this has been seen on a live AP, so it is needs-live-test: every
// command is refused unless the AP's ap_cmd protocol feature is on, and
// reboot stays available over SSH ("dismist ap33 ssh reboot").

// Commands dismist can send.
const (
	CommandReboot       = "reboot"
	CommandRadioReinit  = "radio_reinit"
	CommandDisconnect   = "disconnect"
	CommandPing         = "ping"
	CommandTraceroute   = "traceroute"
	CommandARP          = "arp"
	commandSubject      = "ap-cmd-rx"
	commandSender       = "dismist"
	maxCommandSessions  = 16
	maxCommandOutputAge = 30 * time.Minute
)

// Command errors.
var (
	// ErrUnverifiedCommand means ap-cmd-rx commands are not enabled for the
	// AP (feature ap_cmd off); the API answers 501 unverified_command.
	ErrUnverifiedCommand = errors.New("AP commands are not verified for this AP (protocol feature ap_cmd is off)")
	// ErrNotConnected means the AP has no session to send a command on.
	ErrNotConnected = errors.New("AP is not connected")
	// ErrInvalidCommand means the command or its arguments are invalid.
	ErrInvalidCommand = errors.New("invalid AP command")
)

// apCommand is the ap-cmd-rx body.
type apCommand struct {
	Cmd     string          `json:"Cmd"`
	Params  json.RawMessage `json:"Params,omitempty"`
	Clients []string        `json:"Clients,omitempty"`
	Sender  string          `json:"Sender"`
	Reason  string          `json:"Reason,omitempty"`
}

// CommandResult describes a command sent to the AP.
type CommandResult struct {
	Command string    `json:"command"`
	At      time.Time `json:"at"`
	// Session identifies the output of ping, traceroute and arp
	// (ap-cmd-output-).
	Session string `json:"session,omitempty"`
}

// commandState keeps diagnostic command output by session.
type commandState struct {
	outputs map[string]*commandOutput
}

type commandOutput struct {
	output string
	at     time.Time
}

func (c *commandState) record(output telemetry.CommandOutput, now time.Time) {
	if output.Session == "" {
		return
	}
	if c.outputs == nil {
		c.outputs = make(map[string]*commandOutput)
	}
	for session, entry := range c.outputs {
		if now.Sub(entry.at) > maxCommandOutputAge {
			delete(c.outputs, session)
		}
	}
	entry, ok := c.outputs[output.Session]
	if !ok {
		if len(c.outputs) >= maxCommandSessions {
			return
		}
		entry = &commandOutput{}
		c.outputs[output.Session] = entry
	}
	entry.output = truncateText(entry.output+output.Output, telemetry.MaxCommandOutput)
	entry.at = now
}

// CommandOutput returns the output received so far for a diagnostic
// session.
func (s *Server) CommandOutput(session string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.commands.outputs[session]
	if !ok {
		return "", false
	}
	return entry.output, true
}

// Reboot asks the AP to reboot (ap-cmd-rx "reboot").
func (s *Server) Reboot(reason string) (CommandResult, error) {
	params, _ := json.Marshal(struct{ Reason string }{Reason: commandReason(reason)})
	return s.sendCommand(apCommand{Cmd: CommandReboot, Params: params, Reason: commandReason(reason)}, "")
}

// RestartRadio asks the AP to re-initialise one radio (ap-cmd-rx
// "radio_reinit"); band is "2.4" or "5" and maps to the radio device
// through the model's radio table.
func (s *Server) RestartRadio(band, reason string) (CommandResult, error) {
	s.mu.RLock()
	model := s.status.Model
	s.mu.RUnlock()
	device := ""
	for index, role := range telemetry.RadioTableFor(model).Radios {
		if role.Band == band && role.Role == telemetry.RoleAccess {
			device = fmt.Sprintf("r%d", index)
		}
	}
	if device == "" {
		return CommandResult{}, fmt.Errorf("%w: no %s GHz radio on this model", ErrInvalidCommand, band)
	}
	params, _ := json.Marshal(struct{ Radio string }{Radio: device})
	return s.sendCommand(apCommand{Cmd: CommandRadioReinit, Params: params, Reason: commandReason(reason)}, "")
}

// DisconnectClients deauthenticates clients (ap-cmd-rx "disconnect").
// reasonCode is the 802.11 reason (0 lets the AP use 1).
func (s *Server) DisconnectClients(macs []string, reasonCode int, reason string) (CommandResult, error) {
	if len(macs) == 0 || len(macs) > 64 || reasonCode < 0 || reasonCode > 65535 {
		return CommandResult{}, ErrInvalidCommand
	}
	clients := make([]string, 0, len(macs))
	for _, mac := range macs {
		parsed, err := net.ParseMAC(strings.ReplaceAll(mac, "-", ":"))
		if err != nil || len(parsed) != 6 {
			return CommandResult{}, fmt.Errorf("%w: client MAC %q", ErrInvalidCommand, mac)
		}
		clients = append(clients, strings.ReplaceAll(parsed.String(), ":", "-"))
	}
	params, _ := json.Marshal(struct {
		Clients []string
		Reason  int
	}{Clients: clients, Reason: reasonCode})
	return s.sendCommand(apCommand{Cmd: CommandDisconnect, Params: params, Clients: clients, Reason: commandReason(reason)}, "")
}

// Ping runs ping on the AP (phase 4); the output arrives on
// ap-cmd-output- under the returned session.
func (s *Server) Ping(host string, count int) (CommandResult, error) {
	family, err := hostFamily(host)
	if err != nil || count < 1 || count > 20 {
		return CommandResult{}, ErrInvalidCommand
	}
	session := commandSession()
	params, _ := json.Marshal(struct {
		Host       string
		AddrFamily int
		Count      int
		Session    string
	}{Host: host, AddrFamily: family, Count: count, Session: session})
	return s.sendCommand(apCommand{Cmd: CommandPing, Params: params, Reason: "diagnostics"}, session)
}

// Traceroute runs traceroute on the AP (phase 4).
func (s *Server) Traceroute(host, protocolName string, port uint16) (CommandResult, error) {
	family, err := hostFamily(host)
	if err != nil {
		return CommandResult{}, ErrInvalidCommand
	}
	switch protocolName {
	case "", "udp", "icmp", "tcp":
	default:
		return CommandResult{}, ErrInvalidCommand
	}
	session := commandSession()
	params, _ := json.Marshal(struct {
		Host       string
		AddrFamily int
		Session    string
		Protocol   string
		Port       uint16
	}{Host: host, AddrFamily: family, Session: session, Protocol: protocolName, Port: port})
	return s.sendCommand(apCommand{Cmd: CommandTraceroute, Params: params, Reason: "diagnostics"}, session)
}

// ARP dumps the AP's ARP table (phase 4).
func (s *Server) ARP() (CommandResult, error) {
	session := commandSession()
	params, _ := json.Marshal(struct{ Session string }{Session: session})
	return s.sendCommand(apCommand{Cmd: CommandARP, Params: params, Reason: "diagnostics"}, session)
}

// hostFamily validates a diagnostic target (an IP address or a DNS name)
// and returns its address family (4 or 6; 4 for names).
func hostFamily(host string) (int, error) {
	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() != nil {
			return 4, nil
		}
		return 6, nil
	}
	if len(host) == 0 || len(host) > 253 {
		return 0, ErrInvalidCommand
	}
	for _, char := range host {
		if !(char == '.' || char == '-' || char >= '0' && char <= '9' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z') {
			return 0, ErrInvalidCommand
		}
	}
	return 4, nil
}

func commandSession() string {
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	return "dismist-" + hex.EncodeToString(raw[:])
}

func commandReason(reason string) string {
	reason = cleanHelloString(reason, 120)
	if reason == "" {
		return "requested in dismist"
	}
	return reason
}

// sendCommand writes one ap-cmd-rx frame (class 2, handle 0) and records
// an action event.
func (s *Server) sendCommand(command apCommand, session string) (CommandResult, error) {
	if !s.FeatureEnabled(FeatureAPCmd) {
		return CommandResult{}, ErrUnverifiedCommand
	}
	command.Sender = commandSender
	body, err := json.Marshal(command)
	if err != nil {
		return CommandResult{}, err
	}
	now := time.Now()
	s.mu.Lock()
	conn := s.active
	if conn == nil {
		s.mu.Unlock()
		return CommandResult{}, ErrNotConnected
	}
	s.emitLocked(Event{At: now, Kind: EventAction, Severity: SeverityInfo,
		Title: fmt.Sprintf("Sent %s command to the AP", command.Cmd), Detail: command.Reason})
	s.mu.Unlock()
	s.flush()
	if err := conn.writeMessage(protocol.Message{Version: 1, ServiceClass: 2, Subject: commandSubject}, body); err != nil {
		return CommandResult{}, err
	}
	s.counters.command(command.Cmd)
	return CommandResult{Command: command.Cmd, At: now, Session: session}, nil
}
