package dnsserver

import (
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestOnlyBenchAPGetsLocalControllerAddress(t *testing.T) {
	r, err := NewResolver("192.0.2.101", "ep-terminator.mistsys.net", "198.51.100.19", "1.1.1.1:53")
	if err != nil {
		t.Fatal(err)
	}
	q := new(dns.Msg)
	q.SetQuestion("Ep-Terminator.MistSys.Net.", dns.TypeA)
	answer := r.Resolve(q, net.ParseIP("192.0.2.101"))
	if answer.Rcode != dns.RcodeSuccess || len(answer.Answer) != 1 {
		t.Fatalf("unexpected answer: %+v", answer)
	}
	a, ok := answer.Answer[0].(*dns.A)
	if !ok || !a.A.Equal(net.ParseIP("198.51.100.19")) {
		t.Fatalf("short controller address: %+v", answer.Answer)
	}
	unknown := r.Resolve(q, net.ParseIP("192.0.2.102"))
	if unknown.Rcode != dns.RcodeRefused || len(unknown.Answer) != 0 {
		t.Fatalf("non-bench client received answer: %+v", unknown)
	}
}

func TestIPv6QuestionGetsNoLocalAddress(t *testing.T) {
	r, _ := NewResolver("192.0.2.101", "ep-terminator.mistsys.net", "198.51.100.19", "1.1.1.1:53")
	q := new(dns.Msg)
	q.SetQuestion("ep-terminator.mistsys.net.", dns.TypeAAAA)
	answer := r.Resolve(q, net.ParseIP("192.0.2.101"))
	if answer.Rcode != dns.RcodeSuccess || len(answer.Answer) != 0 {
		t.Fatalf("expected empty AAAA answer: %+v", answer)
	}
}

func TestOtherNamesAreForwardedForBenchAP(t *testing.T) {
	r, _ := NewResolver("192.0.2.101", "ep-terminator.mistsys.net", "198.51.100.19", "1.1.1.1:53")
	called := false
	r.exchange = func(q *dns.Msg, upstream string) (*dns.Msg, time.Duration, error) {
		called = true
		if upstream != "1.1.1.1:53" || q.Question[0].Name != "pool.ntp.org." {
			t.Fatalf("short forwarded query: %+v %s", q, upstream)
		}
		resp := new(dns.Msg)
		resp.SetReply(q)
		resp.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: "pool.ntp.org.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 30}, A: net.ParseIP("203.0.113.7")}}
		return resp, 0, nil
	}
	q := new(dns.Msg)
	q.SetQuestion("pool.ntp.org.", dns.TypeA)
	answer := r.Resolve(q, net.ParseIP("192.0.2.101"))
	if !called || answer.Rcode != dns.RcodeSuccess || len(answer.Answer) != 1 {
		t.Fatalf("forwarded answer missing: %+v", answer)
	}
}

func TestResolverCanAddAndRemoveRegisteredAPs(t *testing.T) {
	r, _ := NewResolver("192.0.2.101", "ep-terminator.mistsys.net", "198.51.100.19", "1.1.1.1:53")
	q := new(dns.Msg)
	q.SetQuestion("ep-terminator.mistsys.net.", dns.TypeA)
	second := net.ParseIP("192.0.2.102")
	if r.Resolve(q, second).Rcode != dns.RcodeRefused {
		t.Fatal("unregistered AP got DNS override")
	}
	if err := r.AllowAP("192.0.2.102"); err != nil {
		t.Fatal(err)
	}
	if r.Resolve(q, second).Rcode != dns.RcodeSuccess {
		t.Fatal("registered second AP did not get DNS override")
	}
	r.RemoveAP("192.0.2.101")
	if r.Resolve(q, net.ParseIP("192.0.2.101")).Rcode != dns.RcodeRefused {
		t.Fatal("removed AP retained DNS override")
	}
}

func TestResolverStartsWithNoRegisteredAP(t *testing.T) {
	r, err := NewResolver("", "ep-terminator.mistsys.net", "192.0.2.10", "1.1.1.1:53")
	if err != nil {
		t.Fatal(err)
	}
	query := new(dns.Msg)
	query.SetQuestion("ep-terminator.mistsys.net.", dns.TypeA)
	ap := net.ParseIP("192.0.2.20")
	if got := r.Resolve(query, ap); got.Rcode != dns.RcodeRefused {
		t.Fatalf("unknown AP should be refused, got %+v", got)
	}
	if err := r.AllowAP(ap.String()); err != nil {
		t.Fatal(err)
	}
	if got := r.Resolve(query, ap); got.Rcode != dns.RcodeSuccess || len(got.Answer) != 1 {
		t.Fatalf("registered AP should receive the controller address, got %+v", got)
	}
}
