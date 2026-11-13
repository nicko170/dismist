package dnsserver

import (
	"net"
	"sync"

	"github.com/miekg/dns"
)

type Service struct {
	addr string
	udp  *dns.Server
	tcp  *dns.Server
	once sync.Once
}

func Start(addr string, handler dns.Handler) (*Service, error) {
	packet, err := net.ListenPacket("udp", addr)
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", packet.LocalAddr().String())
	if err != nil {
		_ = packet.Close()
		return nil, err
	}
	service := &Service{
		addr: packet.LocalAddr().String(),
		udp:  &dns.Server{PacketConn: packet, Handler: handler},
		tcp:  &dns.Server{Listener: listener, Handler: handler},
	}
	go func() { _ = service.udp.ActivateAndServe() }()
	go func() { _ = service.tcp.ActivateAndServe() }()
	return service, nil
}

func (s *Service) Addr() string { return s.addr }

func (s *Service) Close() {
	s.once.Do(func() {
		_ = s.udp.Shutdown()
		_ = s.tcp.Shutdown()
	})
}
