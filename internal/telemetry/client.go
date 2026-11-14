package telemetry

import (
	"fmt"
	"net/netip"
)

// qosAccessCategories names the four 33.85 entries (Broadcom AC order,
// unverified).
var qosAccessCategories = [4]string{"BE", "BK", "VI", "VO"}

// decodeClient reads ClientStats (33), the full record (design §5.3).
func decodeClient(r reader, f field) (Client, bool) {
	fields, ok := r.message(f)
	if !ok {
		return Client{}, false
	}
	var client Client
	for _, item := range fields {
		client.present.add(item.number)
		switch item.number {
		case 1:
			client.MAC = r.mac(item)
		case 2:
			client.RadioIndex = int(min(r.uint(item), 255))
		case 3:
			client.BandCode = r.int(item)
		case 4:
			client.BSSID = r.mac(item)
		case 5:
			sub, ok := r.message(item)
			if !ok {
				continue
			}
			for _, value := range sub {
				switch value.number {
				case 1:
					client.WLANID = r.uuid(value)
				case 2:
					client.SSID = r.str(value, 32)
				}
			}
		case 6:
			client.ConnectedSeconds = r.uint(item)
		case 7:
			client.InactiveMillis = r.uint(item)
		case 8:
			client.RSSI = clampDBm(r.zigzag(item))
		case 9:
			for _, value := range r.varints(item) {
				if len(client.PerAntennaRSSI) < 8 {
					client.PerAntennaRSSI = append(client.PerAntennaRSSI, clampDBm(zigzag(value)))
				}
			}
		case 10:
			client.AverageRSSI = clampDBm(r.zigzag(item))
		case 11:
			client.TxMbps = float64(r.uint(item)) / 10
		case 12:
			client.TxBytes = r.uint(item)
		case 13:
			client.TxPackets = r.uint(item)
		case 14:
			client.TxPacketsSent = r.uint(item)
		case 15:
			client.TxRetries = r.uint(item)
		case 16:
			client.TxRetried = r.uint(item)
		case 17:
			client.TxFailed = r.uint(item)
		case 18:
			client.TxBps = r.uint(item)
		case 19:
			client.TxPps = r.uint(item)
		case 22:
			client.RxMbps = float64(r.uint(item)) / 10
		case 23:
			client.RxBytes = r.uint(item)
		case 24:
			client.RxPackets = r.uint(item)
		case 25:
			client.RxMcastPackets = r.uint(item)
		case 26:
			client.RxMcastBytes = r.uint(item)
		case 27:
			client.RxRetried = r.uint(item)
		case 28:
			client.RxRetries = r.uint(item)
		case 29:
			client.RxDups = r.uint(item)
		case 30:
			client.RxDecryptFails = r.uint(item)
		case 31:
			client.RxBps = r.uint(item)
		case 32:
			client.RxPps = r.uint(item)
		case 44:
			client.Name = r.str(item, 64)
		case 45:
			client.UserAgent = r.str(item, 256)
		case 47:
			client.VLANID = min(r.uint(item), 4095)
		case 48:
			client.Username = r.str(item, 128)
		case 52:
			client.Protocol = r.str(item, 8)
		case 53:
			client.Streams = int(min(r.uint(item), 16))
		case 54:
			client.MultiPSKName = r.str(item, 64)
		case 55:
			client.IP = r.addr(item)
		case 58:
			client.DualBand = r.bool(item)
		case 66:
			client.Auth = r.str(item, 32)
		case 67:
			client.Cipher = r.str(item, 32)
		case 80:
			client.BandwidthMHz = int(min(r.uint(item), 320))
		case 83:
			client.LastStatsAgo = r.int(item)
		case 85:
			if len(client.QoS) < len(qosAccessCategories) {
				if qos, ok := decodeQoS(r, item); ok {
					qos.AC = qosAccessCategories[len(client.QoS)]
					client.QoS = append(client.QoS, qos)
				}
			}
		case 90:
			sub, ok := r.message(item)
			if !ok {
				continue
			}
			for _, value := range sub {
				for _, address := range r.addrs(value) {
					if len(client.IPv6) < 8 {
						client.IPv6 = append(client.IPv6, address)
					}
				}
			}
		}
	}
	if client.MAC == "" {
		r.fail()
		return Client{}, false
	}
	client.SelfAssigned = selfAssigned(client.IP)
	return client, true
}

func decodeQoS(r reader, f field) (QoS, bool) {
	fields, ok := r.message(f)
	if !ok {
		return QoS{}, false
	}
	var qos QoS
	for _, item := range fields {
		switch item.number {
		case 1:
			qos.TxPackets = r.uint(item)
		case 2:
			qos.TxBytes = r.uint(item)
		case 3:
			qos.RxPackets = r.uint(item)
		case 4:
			qos.RxBytes = r.uint(item)
		}
	}
	return qos, true
}

func selfAssigned(ip string) bool {
	address, err := netip.ParseAddr(ip)
	return err == nil && netip.MustParsePrefix("169.254.0.0/16").Contains(address)
}

// fastClientFields are the ClientStats fields a fast frame carries
// (measured on 6,850 fast frames): in a fast frame their absence means
// zero, like in a full frame. Every other field is full-only, so a fast
// frame that omits it keeps the previous record's value (design §5.3).
// RadioIndex (2) is handled on its own: it is omitted when 0.
var fastClientFields = func() FieldSet {
	var set FieldSet
	for _, number := range []uint64{1, 3, 4, 5, 6, 7, 8, 9, 10, 12, 13, 14, 23, 24, 25, 26, 28, 31, 47, 49, 50, 51, 52, 53, 55, 66, 67} {
		set.add(number)
	}
	return set
}()

// MergeClients returns the client set of the newest frame (incoming) with
// each client's full-only fields taken from its previous record when the
// frame is a fast one and did not carry them (design §5.3: link rates,
// hostnames and the rest of the full-only fields no longer read 0 for 58
// of every 60 s). Clients in previous that are not in incoming are gone.
func MergeClients(previous map[string]Client, incoming []Client, fast bool) map[string]Client {
	next := make(map[string]Client, len(incoming))
	for _, client := range incoming {
		if client.MAC == "" {
			continue
		}
		if old, ok := previous[client.MAC]; ok && fast {
			client.carryFrom(old)
		}
		next[client.MAC] = client
	}
	return next
}

// carryFrom fills the full-only fields this fast-frame record did not
// carry from old.
func (c *Client) carryFrom(old Client) {
	absent := func(number uint64) bool {
		return !fastClientFields.Has(number) && !c.present.Has(number)
	}
	if !c.present.Has(2) && c.BandCode == old.BandCode {
		c.RadioIndex = old.RadioIndex
	}
	if absent(11) {
		c.TxMbps = old.TxMbps
	}
	if absent(15) {
		c.TxRetries = old.TxRetries
	}
	if absent(16) {
		c.TxRetried = old.TxRetried
	}
	if absent(17) {
		c.TxFailed = old.TxFailed
	}
	if absent(18) {
		c.TxBps = old.TxBps
	}
	if absent(19) {
		c.TxPps = old.TxPps
	}
	if absent(22) {
		c.RxMbps = old.RxMbps
	}
	if absent(27) {
		c.RxRetried = old.RxRetried
	}
	if absent(29) {
		c.RxDups = old.RxDups
	}
	if absent(30) {
		c.RxDecryptFails = old.RxDecryptFails
	}
	if absent(32) {
		c.RxPps = old.RxPps
	}
	if absent(44) {
		c.Name = old.Name
	}
	if absent(45) {
		c.UserAgent = old.UserAgent
	}
	if absent(48) {
		c.Username = old.Username
	}
	if absent(54) {
		c.MultiPSKName = old.MultiPSKName
	}
	if absent(58) {
		c.DualBand = old.DualBand
	}
	if absent(80) {
		c.BandwidthMHz = old.BandwidthMHz
	}
	if absent(83) {
		c.LastStatsAgo = old.LastStatsAgo
	}
	if absent(85) {
		c.QoS = append([]QoS(nil), old.QoS...)
	}
	if absent(90) {
		c.IPv6 = append([]string(nil), old.IPv6...)
	}
	// Fast-frame fields that the AP leaves out when it does not know them
	// yet: keep the last known value rather than blanking the UI.
	if c.SSID == "" {
		c.SSID, c.WLANID = old.SSID, old.WLANID
	}
	if c.IP == "" {
		c.IP, c.SelfAssigned = old.IP, old.SelfAssigned
	}
	if c.Auth == "" {
		c.Auth = old.Auth
	}
	if c.Cipher == "" {
		c.Cipher = old.Cipher
	}
	if c.BSSID == "" {
		c.BSSID = old.BSSID
	}
}

// Present reports whether the client's frame carried ClientStats field n.
func (c Client) Present(n uint64) bool { return c.present.Has(n) }

// JoinClients derives each client's radio, band, channel, width and SNR
// from the radio with index 33.2 RadioIndex (design §5.3), never from the
// band code: the scan radio also reports band 0 and would collide with
// the 2.4 GHz radio. The scan radio is excluded from every join. With no
// radios (a fast frame) the derived fields are left as they are.
func JoinClients(clients []Client, radios []RadioStats) {
	if len(radios) == 0 {
		return
	}
	for index := range clients {
		client := &clients[index]
		client.Radio = fmt.Sprintf("r%d", client.RadioIndex)
		client.Band, client.Channel, client.WidthMHz, client.SNR = BandLabel(client.BandCode), 0, client.BandwidthMHz, nil
		for _, radio := range radios {
			if radio.Index != client.RadioIndex || radio.Dev == "" {
				continue
			}
			if radio.Role == RoleScan {
				break
			}
			client.Band, client.Channel = radio.Band, radio.Channel
			if client.WidthMHz == 0 {
				client.WidthMHz = int(radio.WidthMHz)
			}
			if client.RSSI != 0 && radio.NoiseFloorDBm != 0 {
				snr := int(client.RSSI) - radio.NoiseFloorDBm
				client.SNR = &snr
			}
			break
		}
	}
}
