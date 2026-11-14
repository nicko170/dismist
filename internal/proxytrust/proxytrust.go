// Package proxytrust decides whether an operator UI request reached the
// operator over HTTPS, either directly or through a reverse proxy that the
// operator trusts to report the original scheme.
package proxytrust

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
)

type contextKey struct{}

// prefixesKey carries the trusted prefixes, for ClientAddr.
type prefixesKey struct{}

// ParsePrefixes reads a comma-separated list of CIDR prefixes or single IP
// addresses. An empty string yields no prefixes.
func ParsePrefixes(value string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	for _, field := range strings.Split(value, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(field); err == nil {
			prefixes = append(prefixes, prefix.Masked())
			continue
		}
		addr, err := netip.ParseAddr(field)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy %q: want a CIDR prefix or IP address", field)
		}
		addr = addr.Unmap()
		prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return prefixes, nil
}

// Middleware marks requests whose immediate peer is inside one of the trusted
// prefixes, so HTTPS honours the X-Forwarded-Proto header they carry.
func Middleware(trusted []netip.Prefix, next http.Handler) http.Handler {
	if len(trusted) == 0 {
		return next
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ctx := context.WithValue(request.Context(), prefixesKey{}, trusted)
		if addr, ok := peer(request); ok {
			for _, prefix := range trusted {
				if prefix.Contains(addr) {
					ctx = context.WithValue(ctx, contextKey{}, true)
					break
				}
			}
		}
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

// HTTPS reports whether the client used HTTPS. X-Forwarded-Proto is honoured
// only from a loopback peer or a peer marked trusted by Middleware.
func HTTPS(request *http.Request) bool {
	if request.TLS != nil {
		return true
	}
	if request.Header.Get("X-Forwarded-Proto") != "https" {
		return false
	}
	if trusted, _ := request.Context().Value(contextKey{}).(bool); trusted {
		return true
	}
	addr, ok := peer(request)
	return ok && addr.IsLoopback()
}

func peer(request *http.Request) (netip.Addr, bool) {
	addrPort, err := netip.ParseAddrPort(request.RemoteAddr)
	if err != nil {
		return netip.Addr{}, false
	}
	return addrPort.Addr().Unmap(), true
}

// ClientAddr returns the address of the client that sent the request. It
// is the peer's address unless the peer is loopback or a trusted proxy
// (Middleware); then X-Forwarded-For is read from the right, skipping
// entries that are themselves loopback or trusted, and the first other
// address is the client. A malformed entry ends the walk at the last
// trusted hop, so a client cannot choose its own address by sending the
// header. The zero Addr means RemoteAddr does not parse.
func ClientAddr(request *http.Request) netip.Addr {
	addr, ok := peer(request)
	if !ok {
		return netip.Addr{}
	}
	prefixes, _ := request.Context().Value(prefixesKey{}).([]netip.Prefix)
	trusted := func(addr netip.Addr) bool {
		if addr.IsLoopback() {
			return true
		}
		for _, prefix := range prefixes {
			if prefix.Contains(addr) {
				return true
			}
		}
		return false
	}
	if !trusted(addr) {
		return addr
	}
	var hops []string
	for _, value := range request.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(value, ",")...)
	}
	for index := len(hops) - 1; index >= 0; index-- {
		hop, err := netip.ParseAddr(strings.TrimSpace(hops[index]))
		if err != nil {
			break
		}
		addr = hop.Unmap().WithZone("")
		if !trusted(addr) {
			break
		}
	}
	return addr
}
