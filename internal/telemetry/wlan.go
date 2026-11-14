package telemetry

import "fmt"

// decodeVAP reads WLANStats (32): 1 ID (the WLAN UUID, which is the
// dismist network ID), 2 RadioIndex, 3 BSSID, 4 Dev, 5 SSID, 6 NumClients,
// 7 TxMcastPkts, 8 TxMcastBytes, 14 OWETransition, 22 EapolStats.
func decodeVAP(r reader, f field) (VAP, bool) {
	fields, ok := r.message(f)
	if !ok {
		return VAP{}, false
	}
	var vap VAP
	for _, item := range fields {
		switch item.number {
		case 1:
			vap.WLANID = r.uuid(item)
		case 2:
			vap.RadioIndex = int(min(r.uint(item), 255))
		case 3:
			vap.BSSID = r.mac(item)
		case 4:
			vap.Interface = r.str(item, 16)
		case 5:
			vap.SSID = r.str(item, 32)
		case 6:
			vap.Clients = int(min(r.uint(item), 1<<16))
		case 7:
			vap.TxMcastPkts = r.uint(item)
		case 8:
			vap.TxMcastBytes = r.uint(item)
		case 14:
			vap.OWE = r.bool(item)
		case 22:
			if eapol, ok := decodeEapol(r, item); ok {
				vap.Eapol = eapol
			}
		}
	}
	if vap.Interface == "" && vap.BSSID == "" {
		r.fail()
		return VAP{}, false
	}
	vap.Radio = fmt.Sprintf("r%d", vap.RadioIndex)
	return vap, true
}

// decodeEapol reads WLANEapolStats (32.22).
func decodeEapol(r reader, f field) (*EapolStats, bool) {
	fields, ok := r.message(f)
	if !ok {
		return nil, false
	}
	var eapol EapolStats
	for _, item := range fields {
		value := r.uint(item)
		switch item.number {
		case 1:
			eapol.RxShortFrame = value
		case 2:
			eapol.RxExceedsLength = value
		case 3:
			eapol.RxIgnored = value
		case 4:
			eapol.RxUnencrypted = value
		case 5:
			eapol.RxMsg2 = value
		case 6:
			eapol.RxMsg4 = value
		case 7:
			eapol.RxGroup2 = value
		case 8:
			eapol.RxDecryptErrors = value
		case 9:
			eapol.RxReplayed = value
		case 10:
			eapol.RxRequests = value
		case 11:
			eapol.RxInvalidMIC = value
		case 12:
			eapol.TxMsg1 = value
		case 13:
			eapol.TxMsg1Retries = value
		case 14:
			eapol.TxMsg3 = value
		case 15:
			eapol.TxMsg3Retries = value
		case 16:
			eapol.TxGroup = value
		case 17:
			eapol.TxGroupRetries = value
		case 18:
			eapol.RxEapStarts = value
		case 19:
			eapol.RxEapStartDrops = value
		case 20:
			eapol.RxEapLogoffs = value
		case 22:
			eapol.RxEapResponses = value
		case 23:
			eapol.RxEapCodeRequest = value
		case 27:
			eapol.RxEapolFrames = value
		case 28:
			eapol.RxEapFrames = value
		}
	}
	return &eapol, true
}

// VAPIndex returns the BSSID index of a VAP device "wlX.N" (N), or -1.
func VAPIndex(dev string) int {
	for index := 0; index < len(dev); index++ {
		if dev[index] != '.' {
			continue
		}
		value := 0
		if index+1 >= len(dev) {
			return -1
		}
		for _, char := range dev[index+1:] {
			if char < '0' || char > '9' || value > 1000 {
				return -1
			}
			value = value*10 + int(char-'0')
		}
		return value
	}
	return -1
}
