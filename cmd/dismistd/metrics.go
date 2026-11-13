package main

import (
	"errors"
	"net"
)

// metricsListener requires a numeric loopback address so an unauthenticated
// scrape endpoint cannot accidentally be exposed on the LAN.
func metricsListener(address string) (net.Listener, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("metrics listen address must be a numeric loopback IP")
	}
	return net.Listen("tcp", address)
}
