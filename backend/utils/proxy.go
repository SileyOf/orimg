package utils

import (
	"net"
	"net/http"
	"strings"
)

func trustedIP(ip net.IP, proxies string) bool {
	if ip == nil {
		return false
	}
	for _, entry := range strings.Split(proxies, ",") {
		entry = strings.TrimSpace(entry)
		if candidate := net.ParseIP(entry); candidate != nil && candidate.Equal(ip) {
			return true
		}
		if _, network, err := net.ParseCIDR(entry); err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

func peerIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}

func ClientIP(r *http.Request, proxies string) string {
	peer := peerIP(r)
	if peer == nil {
		return r.RemoteAddr
	}
	if !trustedIP(peer, proxies) {
		return peer.String()
	}
	forwarded := r.Header.Get("X-Forwarded-For")
	if forwarded == "" {
		forwarded = r.Header.Get("X-Real-IP")
	}
	chain := strings.Split(forwarded, ",")
	for i := len(chain) - 1; i >= 0; i-- {
		ip := net.ParseIP(strings.TrimSpace(chain[i]))
		if ip == nil {
			return peer.String()
		}
		if !trustedIP(ip, proxies) || i == 0 {
			return ip.String()
		}
	}
	return peer.String()
}

func RequestScheme(r *http.Request, proxies string) string {
	if r.TLS != nil {
		return "https"
	}
	if trustedIP(peerIP(r), proxies) && strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https") {
		return "https"
	}
	return "http"
}

func SecureSetupRequest(r *http.Request, proxies string) bool {
	if RequestScheme(r, proxies) == "https" {
		return true
	}
	peer := peerIP(r)
	host := r.Host
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	ip := net.ParseIP(host)
	return peer != nil && peer.IsLoopback() && (host == "localhost" || ip != nil && ip.IsLoopback())
}
