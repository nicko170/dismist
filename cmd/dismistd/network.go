package main

import (
	"errors"
	"fmt"
	"net"
)

// interfaceIPv4s returns addresses on interfaces that are currently up. The
// selection step below rejects local-only addresses and ambiguous hosts.
func interfaceIPv4s() ([]net.IP, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var addresses []net.IP
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		assigned, err := iface.Addrs()
		if err != nil {
			return nil, fmt.Errorf("read interface %s addresses: %w", iface.Name, err)
		}
		for _, address := range assigned {
			if network, ok := address.(*net.IPNet); ok {
				addresses = append(addresses, network.IP)
			}
		}
	}
	return addresses, nil
}

func selectControllerIP(explicit string, addresses []net.IP) (string, error) {
	if explicit != "" {
		ip := net.ParseIP(explicit)
		if ip != nil {
			ip = ip.To4()
		}
		if ip == nil || ip.IsUnspecified() || ip.IsMulticast() {
			return "", errors.New("-controller-ip must be a usable IPv4 address")
		}
		return ip.String(), nil
	}
	unique := make(map[string]bool)
	for _, address := range addresses {
		ip := address.To4()
		if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() {
			continue
		}
		unique[ip.String()] = true
	}
	if len(unique) != 1 {
		return "", fmt.Errorf("found %d usable controller IPv4 addresses; set -controller-ip to the address reachable from APs", len(unique))
	}
	for address := range unique {
		return address, nil
	}
	panic("unreachable")
}
