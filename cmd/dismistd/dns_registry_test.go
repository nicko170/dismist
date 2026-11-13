package main

import (
	"net"
	"testing"

	"github.com/miekg/dns"
)

func TestNewAPDNSResolverHandlesEmptyAndMultipleAPs(t *testing.T) {
	hostname := "ep-terminator.mistsys.net"
	controllerIP := "192.0.2.10"
	resolver, err := newAPDNSResolver(nil, hostname, controllerIP, "1.1.1.1:53")
	if err != nil {
		t.Fatal(err)
	}
	query := new(dns.Msg)
	query.SetQuestion(dns.Fqdn(hostname), dns.TypeA)
	if got := resolver.Resolve(query, net.ParseIP("192.0.2.20")); got.Rcode != dns.RcodeRefused {
		t.Fatalf("empty inventory should refuse unknown AP: %+v", got)
	}
	resolver, err = newAPDNSResolver([]string{"192.0.2.20", "192.0.2.21"}, hostname, controllerIP, "1.1.1.1:53")
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"192.0.2.20", "192.0.2.21"} {
		if got := resolver.Resolve(query, net.ParseIP(address)); got.Rcode != dns.RcodeSuccess || len(got.Answer) != 1 {
			t.Fatalf("registered AP %s missing DNS answer: %+v", address, got)
		}
	}
}
