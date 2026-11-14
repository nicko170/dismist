package telemetry

import "net/netip"

// decodeInterface reads SVIStats (26): 1 Dev, 2 Vlan, 3 IPs []AddrMask6
// {2 prefix length, 3 address}, 16 DADFailed []Addr6. Addresses are in
// CIDR form; an address that does not parse is skipped.
func decodeInterface(r reader, f field) (Interface, bool) {
	fields, ok := r.message(f)
	if !ok {
		return Interface{}, false
	}
	var iface Interface
	for _, item := range fields {
		switch item.number {
		case 1:
			iface.Name = r.str(item, 32)
		case 2:
			iface.VLAN = int(min(r.uint(item), 4095))
		case 3:
			address, prefix := r.addrMask(item)
			if address == "" || len(iface.Addresses) >= 16 {
				continue
			}
			iface.Addresses = append(iface.Addresses, cidr(address, prefix))
		case 16:
			for _, address := range r.addrs(item) {
				if len(iface.DADFailed) < 16 {
					iface.DADFailed = append(iface.DADFailed, address)
				}
			}
		}
	}
	if iface.Name == "" {
		r.fail()
		return Interface{}, false
	}
	return iface, true
}

// cidr formats address/prefix, or the bare address when the prefix is
// absent or out of range.
func cidr(address string, prefix int) string {
	parsed, err := netip.ParseAddr(address)
	if err != nil || prefix < 0 || prefix > parsed.BitLen() {
		return address
	}
	return netip.PrefixFrom(parsed, prefix).String()
}

// decodeDHCP reads DHCPClientStats (28).
func decodeDHCP(r reader, f field) (DHCPLease, bool) {
	fields, ok := r.message(f)
	if !ok {
		return DHCPLease{}, false
	}
	var lease DHCPLease
	for _, item := range fields {
		switch item.number {
		case 1:
			lease.Dev = r.str(item, 32)
		case 2:
			lease.State = r.str(item, 32)
		case 3:
			lease.RenewedAt = r.time(item)
		case 4:
			lease.LeaseSeconds = uint32(min(r.uint(item), 1<<31))
		case 6:
			address, prefix := r.addrMask(item)
			lease.Address = address
			if prefix > 0 {
				lease.Prefix = prefix
			}
		case 9:
			lease.Error = r.str(item, 256)
		case 10:
			lease.Server = r.addr(item)
		case 11:
			lease.Gateway = r.addr(item)
		case 12:
			lease.DNS = appendBounded(lease.DNS, r.addrs(item), 8)
		case 13:
			lease.Domain = r.str(item, 128)
		case 14:
			lease.NTP = appendBounded(lease.NTP, r.addrs(item), 8)
		case 15:
			lease.ProxyURL = r.str(item, 256)
		}
	}
	return lease, true
}

// decodeDHCP6 reads DHCP6ClientStats (87).
func decodeDHCP6(r reader, f field) (DHCP6Lease, bool) {
	fields, ok := r.message(f)
	if !ok {
		return DHCP6Lease{}, false
	}
	var lease DHCP6Lease
	for _, item := range fields {
		switch item.number {
		case 1:
			lease.Dev = r.str(item, 32)
		case 2:
			lease.State = r.str(item, 32)
		case 3:
			lease.RenewedAt = r.time(item)
		case 4:
			lease.LeaseSeconds = uint32(min(r.uint(item), 1<<31))
		case 5:
			lease.Server = r.addr(item)
		case 6:
			lease.Addresses = appendBounded(lease.Addresses, r.addrs(item), 8)
		case 7:
			lease.DNS = appendBounded(lease.DNS, r.addrs(item), 8)
		case 8:
			if domain := r.str(item, 128); domain != "" {
				lease.Domains = appendBounded(lease.Domains, []string{domain}, 8)
			}
		case 9:
			if server := r.str(item, 128); server != "" {
				lease.NTP = appendBounded(lease.NTP, []string{server}, 8)
			}
		case 10:
			lease.ProxyURL = r.str(item, 256)
		case 11:
			lease.Error = r.str(item, 256)
		}
	}
	return lease, true
}

func appendBounded(list, values []string, limit int) []string {
	for _, value := range values {
		if len(list) >= limit {
			break
		}
		list = append(list, value)
	}
	return list
}

// decodeRoute reads IPv4Route (29): 1 Dst Subnet4 {2 prefix, 3 net},
// 3 Gw. An absent destination is the default route ("").
func decodeRoute(r reader, f field) (Route, bool) {
	fields, ok := r.message(f)
	if !ok {
		return Route{}, false
	}
	var route Route
	for _, item := range fields {
		switch item.number {
		case 1:
			address, prefix := r.addrMask(item)
			if address == "" && prefix <= 0 {
				continue
			}
			if address == "" {
				address = "0.0.0.0"
			}
			route.Destination = cidr(address, max(prefix, 0))
			if route.Destination == "0.0.0.0/0" {
				route.Destination = ""
			}
		case 3:
			route.Gateway = r.addr(item)
		}
	}
	return route, true
}

// decodeNeighbor reads IPv4Neighbor (73): 1 IP, 2 MAC.
func decodeNeighbor(r reader, f field) (Neighbor, bool) {
	fields, ok := r.message(f)
	if !ok {
		return Neighbor{}, false
	}
	var neighbor Neighbor
	for _, item := range fields {
		switch item.number {
		case 1:
			neighbor.IP = r.addr(item)
		case 2:
			neighbor.MAC = r.mac(item)
		}
	}
	if neighbor.IP == "" {
		return Neighbor{}, false
	}
	return neighbor, true
}

// decodeVLANs reads VLANStats (79): 1 InactiveWiredVLANs []uint16, packed
// or not.
func decodeVLANs(r reader, f field) []int {
	fields, ok := r.message(f)
	if !ok {
		return nil
	}
	var vlans []int
	for _, item := range fields {
		if item.number != 1 {
			continue
		}
		for _, vlan := range r.varints(item) {
			if vlan >= 1 && vlan <= 4094 && len(vlans) < 64 {
				vlans = append(vlans, int(vlan))
			}
		}
	}
	return vlans
}
