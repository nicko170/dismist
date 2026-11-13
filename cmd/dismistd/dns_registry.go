package main

import "github.com/nicko170/dismist/internal/dnsserver"

func newAPDNSResolver(addresses []string, hostname, targetIP, upstream string) (*dnsserver.Resolver, error) {
	first := ""
	if len(addresses) != 0 {
		first = addresses[0]
	}
	resolver, err := dnsserver.NewResolver(first, hostname, targetIP, upstream)
	if err != nil {
		return nil, err
	}
	for index, address := range addresses {
		if index == 0 {
			continue
		}
		if err := resolver.AllowAP(address); err != nil {
			return nil, err
		}
	}
	return resolver, nil
}
