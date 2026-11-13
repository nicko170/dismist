package dnsserver

import (
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// BusyBox can send A and AAAA from one UDP socket with the same DNS ID.
// Both replies must retain their question and answer so clients that match
// beyond the ID can use them independently.
func TestConcurrentAAndAAAAWithSameIDBothReceiveAnswers(t *testing.T) {
	upstreamPacket, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	upstream := &dns.Server{PacketConn: upstreamPacket, Handler: dns.HandlerFunc(func(writer dns.ResponseWriter, query *dns.Msg) {
		answer := new(dns.Msg)
		answer.SetReply(query)
		switch query.Question[0].Qtype {
		case dns.TypeA:
			answer.Answer = []dns.RR{&dns.A{
				Hdr: dns.RR_Header{Name: "google.com.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 30},
				A:   net.ParseIP("203.0.113.9"),
			}}
		case dns.TypeAAAA:
			answer.Answer = []dns.RR{&dns.AAAA{
				Hdr:  dns.RR_Header{Name: "google.com.", Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 30},
				AAAA: net.ParseIP("2001:db8::9"),
			}}
		}
		_ = writer.WriteMsg(answer)
	})}
	go func() { _ = upstream.ActivateAndServe() }()
	t.Cleanup(func() { _ = upstream.Shutdown() })

	resolver, err := NewResolver("127.0.0.1", "ep-terminator.mistsys.net", "192.0.2.10", upstreamPacket.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	service, err := Start("127.0.0.1:0", resolver)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	client, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	serverAddr, err := net.ResolveUDPAddr("udp", service.Addr())
	if err != nil {
		t.Fatal(err)
	}
	for _, queryType := range []uint16{dns.TypeA, dns.TypeAAAA} {
		query := new(dns.Msg)
		query.SetQuestion("google.com.", queryType)
		query.Id = 0x5a5a
		wire, err := query.Pack()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.WriteTo(wire, serverAddr); err != nil {
			t.Fatal(err)
		}
	}
	seen := make(map[uint16]bool)
	for range 2 {
		buffer := make([]byte, 4096)
		count, _, err := client.ReadFrom(buffer)
		if err != nil {
			t.Fatal(err)
		}
		var reply dns.Msg
		if err := reply.Unpack(buffer[:count]); err != nil {
			t.Fatal(err)
		}
		if reply.Id != 0x5a5a || reply.Rcode != dns.RcodeSuccess || len(reply.Question) != 1 || len(reply.Answer) != 1 {
			t.Fatalf("incomplete same-ID reply: %+v", reply)
		}
		queryType := reply.Question[0].Qtype
		if seen[queryType] || reply.Answer[0].Header().Rrtype != queryType {
			t.Fatalf("short or duplicate same-ID answer: %+v", reply)
		}
		seen[queryType] = true
	}
	if !seen[dns.TypeA] || !seen[dns.TypeAAAA] {
		t.Fatalf("missing A or AAAA reply: %v", seen)
	}
}
