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
		if addr, ok := peer(request); ok {
			for _, prefix := range trusted {
				if prefix.Contains(addr) {
					request = request.WithContext(context.WithValue(request.Context(), contextKey{}, true))
					break
				}
			}
		}
		next.ServeHTTP(writer, request)
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
