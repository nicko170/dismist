package telemetry

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

const maxPayload = 4 << 20

var ErrMalformed = errors.New("malformed AP telemetry")

type VAP struct {
	Interface string `json:"interface"`
	SSID      string `json:"ssid"`
	BSSID     string `json:"bssid"`
}

type Radio struct {
	Interface string `json:"interface"`
	WidthMHz  uint64 `json:"width_mhz"`
}

type Client struct {
	MAC              string  `json:"mac"`
	Name             string  `json:"name,omitempty"`
	IP               string  `json:"ip,omitempty"`
	SSID             string  `json:"ssid"`
	BSSID            string  `json:"bssid"`
	VLANID           uint64  `json:"vlan_id"`
	RSSI             int64   `json:"rssi"`
	AverageRSSI      int64   `json:"average_rssi"`
	ConnectedSeconds uint64  `json:"connected_seconds"`
	InactiveMillis   uint64  `json:"inactive_millis"`
	TxMbps           float64 `json:"tx_mbps"`
	RxMbps           float64 `json:"rx_mbps"`
	TxBytes          uint64  `json:"tx_bytes"`
	RxBytes          uint64  `json:"rx_bytes"`
	Auth             string  `json:"auth,omitempty"`
	Cipher           string  `json:"cipher,omitempty"`
	Protocol         string  `json:"protocol,omitempty"`
}

type Event struct {
	When      time.Time `json:"when"`
	Type      int       `json:"type"`
	Title     string    `json:"title"`
	Text      string    `json:"text"`
	ClientMAC string    `json:"client_mac,omitempty"`
	ClientIP  string    `json:"client_ip,omitempty"`
}

type Update struct {
	Topic           string   `json:"topic"`
	APID            string   `json:"ap_id"`
	Model           string   `json:"model,omitempty"`
	Firmware        string   `json:"firmware,omitempty"`
	UptimeSeconds   uint64   `json:"uptime_seconds,omitempty"`
	ConfigTimestamp float64  `json:"config_timestamp,omitempty"`
	Error           string   `json:"error,omitempty"`
	VAPs            []VAP    `json:"vaps,omitempty"`
	Radios          []Radio  `json:"radios,omitempty"`
	Clients         []Client `json:"clients,omitempty"`
	Event           *Event   `json:"event,omitempty"`
}

func Decode(body []byte) (Update, error) {
	if len(body) > maxPayload || len(body) == 0 {
		return Update{}, ErrMalformed
	}
	if len(body) >= 3 && body[0] == 2 && body[1] == 0x1f && body[2] == 0x8b {
		reader, err := gzip.NewReader(bytes.NewReader(body[1:]))
		if err != nil {
			return Update{}, err
		}
		uncompressed, err := io.ReadAll(io.LimitReader(reader, maxPayload+1))
		_ = reader.Close()
		if err != nil || len(uncompressed) > maxPayload {
			return Update{}, ErrMalformed
		}
		body = uncompressed
	}
	switch body[0] {
	case '{':
		return decodeJSON(body)
	case 'P':
		return decodeProtobuf(body[1:])
	default:
		return Update{}, ErrMalformed
	}
}

func decodeJSON(body []byte) (Update, error) {
	var raw struct {
		Topic           string   `json:"Topic"`
		Topics          []string `json:"Topics"`
		ID              string   `json:"ID"`
		ConfigTimestamp float64  `json:"ConfigTimestamp"`
		Firmware        string   `json:"Firmware"`
		Error           string   `json:"Error"`
		Type            int      `json:"Type"`
		Title           string   `json:"Title"`
		Text            string   `json:"Text"`
		When            string   `json:"When"`
		ClientMAC       string   `json:"impacted_client_mac"`
		ClientIP        string   `json:"impacted_client_ip"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return Update{}, err
	}
	update := Update{
		Topic: raw.Topic, APID: raw.ID, ConfigTimestamp: raw.ConfigTimestamp,
		Firmware: raw.Firmware, Error: raw.Error,
	}
	if update.Topic == "" {
		for _, topic := range raw.Topics {
			if topic == "ep-pace-event-" {
				update.Topic = topic
				break
			}
			if topic == "client-pace-event-" {
				update.Topic = topic
			}
		}
	}
	if update.Topic == "ep-pace-event-" || update.Topic == "client-pace-event-" {
		when, _ := time.Parse(time.RFC3339Nano, raw.When)
		mac := strings.ToLower(strings.ReplaceAll(raw.ClientMAC, "-", ":"))
		if parsed, err := net.ParseMAC(mac); err != nil || bytes.Equal(parsed, make([]byte, 6)) {
			mac = ""
		}
		ip := ""
		if parsed := net.ParseIP(raw.ClientIP); parsed != nil && !parsed.IsUnspecified() {
			ip = parsed.String()
		}
		update.Event = &Event{
			When: when, Type: raw.Type, Title: truncate(raw.Title, 120),
			Text: truncate(raw.Text, 500), ClientMAC: mac, ClientIP: ip,
		}
	}
	return update, nil
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

type field struct {
	number uint64
	value  uint64
	bytes  []byte
}

func scan(data []byte) ([]field, error) {
	fields := make([]field, 0, 16)
	for len(data) > 0 {
		key, n := binary.Uvarint(data)
		if n <= 0 || key>>3 == 0 {
			return nil, ErrMalformed
		}
		data = data[n:]
		item := field{number: key >> 3}
		switch key & 7 {
		case 0:
			value, n := binary.Uvarint(data)
			if n <= 0 {
				return nil, ErrMalformed
			}
			item.value = value
			data = data[n:]
		case 1:
			if len(data) < 8 {
				return nil, ErrMalformed
			}
			item.bytes, data = data[:8], data[8:]
		case 2:
			length, n := binary.Uvarint(data)
			if n <= 0 || length > uint64(len(data)-n) {
				return nil, ErrMalformed
			}
			item.bytes = data[n : n+int(length)]
			data = data[n+int(length):]
		case 5:
			if len(data) < 4 {
				return nil, ErrMalformed
			}
			item.bytes, data = data[:4], data[4:]
		default:
			return nil, ErrMalformed
		}
		fields = append(fields, item)
	}
	return fields, nil
}

func decodeProtobuf(body []byte) (Update, error) {
	fields, err := scan(body)
	if err != nil {
		return Update{}, err
	}
	var update Update
	for _, item := range fields {
		switch item.number {
		case 1:
			if err := parseAPStats(item.bytes, &update); err != nil {
				return Update{}, err
			}
		case 2:
			update.Topic = string(item.bytes)
		case 3:
			update.APID = string(item.bytes)
		}
	}
	if update.Topic == "" {
		return Update{}, ErrMalformed
	}
	return update, nil
}

func parseAPStats(body []byte, update *Update) error {
	fields, err := scan(body)
	if err != nil {
		return err
	}
	for _, item := range fields {
		switch item.number {
		case 18:
			update.UptimeSeconds = item.value
		case 19:
			update.Model = string(item.bytes)
		case 20:
			update.Firmware = string(item.bytes)
		case 32:
			vap, err := parseVAP(item.bytes)
			if err != nil {
				return err
			}
			update.VAPs = append(update.VAPs, vap)
		case 36:
			radio, err := parseRadio(item.bytes)
			if err != nil {
				return err
			}
			update.Radios = append(update.Radios, radio)
		case 33:
			client, err := parseClient(item.bytes)
			if err != nil {
				return err
			}
			if client.MAC != "" {
				update.Clients = append(update.Clients, client)
			}
		}
	}
	return nil
}

func parseClient(body []byte) (Client, error) {
	fields, err := scan(body)
	if err != nil {
		return Client{}, err
	}
	var client Client
	for _, item := range fields {
		switch item.number {
		case 1:
			if len(item.bytes) == 6 {
				client.MAC = net.HardwareAddr(item.bytes).String()
			}
		case 4:
			if len(item.bytes) == 6 {
				client.BSSID = net.HardwareAddr(item.bytes).String()
			}
		case 5:
			wlanFields, err := scan(item.bytes)
			if err != nil {
				return Client{}, err
			}
			for _, field := range wlanFields {
				if field.number == 2 {
					client.SSID = string(field.bytes)
				}
			}
		case 6:
			client.ConnectedSeconds = item.value
		case 7:
			client.InactiveMillis = item.value
		case 8:
			client.RSSI = zigzag(item.value)
		case 10:
			client.AverageRSSI = zigzag(item.value)
		case 11:
			client.TxMbps = float64(item.value) / 10
		case 12:
			client.TxBytes = item.value
		case 22:
			client.RxMbps = float64(item.value) / 10
		case 23:
			client.RxBytes = item.value
		case 44:
			client.Name = string(item.bytes)
		case 47:
			client.VLANID = item.value
		case 52:
			client.Protocol = string(item.bytes)
		case 55:
			if len(item.bytes) == 4 {
				client.IP = net.IP(item.bytes).String()
			}
		case 66:
			client.Auth = string(item.bytes)
		case 67:
			client.Cipher = string(item.bytes)
		}
	}
	return client, nil
}

func zigzag(value uint64) int64 {
	return int64(value>>1) ^ -int64(value&1)
}

func parseVAP(body []byte) (VAP, error) {
	fields, err := scan(body)
	if err != nil {
		return VAP{}, err
	}
	var vap VAP
	for _, item := range fields {
		switch item.number {
		case 3:
			if len(item.bytes) == 6 {
				vap.BSSID = net.HardwareAddr(item.bytes).String()
			}
		case 4:
			vap.Interface = string(item.bytes)
		case 5:
			vap.SSID = string(item.bytes)
		}
	}
	return vap, nil
}

func parseRadio(body []byte) (Radio, error) {
	fields, err := scan(body)
	if err != nil {
		return Radio{}, err
	}
	var radio Radio
	for _, item := range fields {
		switch item.number {
		case 1:
			radio.Interface = string(item.bytes)
		case 4:
			radio.WidthMHz = item.value
		}
	}
	if radio.Interface == "" {
		return Radio{}, fmt.Errorf("%w: radio missing interface", ErrMalformed)
	}
	return radio, nil
}
