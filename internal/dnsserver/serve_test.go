package dnsserver

import (
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestServiceAnswersOnUDPAndTCP(t *testing.T) {
	r, _ := NewResolver("127.0.0.1", "ep-terminator.mistsys.net", "198.51.100.19", "1.1.1.1:53")
	service, err := Start("127.0.0.1:0", r)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	for _, network := range []string{"udp", "tcp"} {
		client := &dns.Client{Net: network, Timeout: time.Second}
		query := new(dns.Msg)
		query.SetQuestion("ep-terminator.mistsys.net.", dns.TypeA)
		answer, _, err := client.Exchange(query, service.Addr())
		if err != nil {
			t.Fatalf("%s query: %v", network, err)
		}
		if len(answer.Answer) != 1 || !answer.Answer[0].(*dns.A).A.Equal(net.ParseIP("198.51.100.19")) {
			t.Fatalf("%s short answer: %+v", network, answer.Answer)
		}
	}
}
