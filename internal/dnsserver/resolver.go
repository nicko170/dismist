package dnsserver

import (
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
)

type Resolver struct {
	mu         sync.RWMutex
	allowedIPs map[string]bool
	hostname   string
	targetIP   net.IP
	upstream   string
	exchange   func(*dns.Msg, string) (*dns.Msg, time.Duration, error)
}

func NewResolver(apIP, hostname, targetIP, upstream string) (*Resolver, error) {
	target := net.ParseIP(targetIP).To4()
	if target == nil || hostname == "" {
		return nil, errors.New("invalid AP DNS configuration")
	}
	allowedIPs := make(map[string]bool)
	if apIP != "" {
		ap := net.ParseIP(apIP)
		if ap == nil {
			return nil, errors.New("invalid AP DNS configuration")
		}
		allowedIPs[ap.String()] = true
	}
	if _, _, err := net.SplitHostPort(upstream); err != nil {
		return nil, err
	}
	client := &dns.Client{Net: "udp", Timeout: 3 * time.Second}
	return &Resolver{
		allowedIPs: allowedIPs, hostname: strings.ToLower(dns.Fqdn(hostname)),
		targetIP: target, upstream: upstream, exchange: client.Exchange,
	}, nil
}

func (r *Resolver) AllowAP(address string) error {
	ip := net.ParseIP(address)
	if ip == nil {
		return errors.New("invalid AP address")
	}
	r.mu.Lock()
	r.allowedIPs[ip.String()] = true
	r.mu.Unlock()
	return nil
}

func (r *Resolver) RemoveAP(address string) {
	ip := net.ParseIP(address)
	if ip == nil {
		return
	}
	r.mu.Lock()
	delete(r.allowedIPs, ip.String())
	r.mu.Unlock()
}

func (r *Resolver) Resolve(query *dns.Msg, source net.IP) *dns.Msg {
	answer := new(dns.Msg)
	answer.SetReply(query)
	answer.RecursionAvailable = true
	r.mu.RLock()
	allowed := source != nil && r.allowedIPs[source.String()]
	r.mu.RUnlock()
	if !allowed {
		answer.Rcode = dns.RcodeRefused
		return answer
	}
	if query.Opcode != dns.OpcodeQuery || len(query.Question) != 1 {
		answer.Rcode = dns.RcodeFormatError
		return answer
	}
	question := query.Question[0]
	if strings.ToLower(question.Name) == r.hostname {
		answer.Authoritative = true
		if question.Qtype == dns.TypeA && question.Qclass == dns.ClassINET {
			answer.Answer = []dns.RR{&dns.A{
				Hdr: dns.RR_Header{Name: question.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 30},
				A:   r.targetIP,
			}}
		}
		return answer
	}
	forwarded, _, err := r.exchange(query, r.upstream)
	if err != nil || forwarded == nil {
		answer.Rcode = dns.RcodeServerFailure
		return answer
	}
	forwarded.Id = query.Id
	return forwarded
}

func (r *Resolver) ServeDNS(writer dns.ResponseWriter, query *dns.Msg) {
	var source net.IP
	switch addr := writer.RemoteAddr().(type) {
	case *net.UDPAddr:
		source = addr.IP
	case *net.TCPAddr:
		source = addr.IP
	}
	_ = writer.WriteMsg(r.Resolve(query, source))
}
