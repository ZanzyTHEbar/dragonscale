package security

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ErrBlockedURL is returned when a URL targets a blocked network.
var ErrBlockedURL = fmt.Errorf("URL targets a blocked network")

// ValidateURL checks that a URL is safe to fetch, blocking internal/private
// IPs, cloud metadata endpoints, and loopback addresses that could be used
// for SSRF attacks.
func ValidateURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("%w: only http/https schemes allowed, got %q", ErrBlockedURL, scheme)
	}

	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("%w: empty hostname", ErrBlockedURL)
	}

	if isBlockedHost(host) {
		return fmt.Errorf("%w: host %q is blocked", ErrBlockedURL, host)
	}

	ips, err := net.LookupHost(host)
	if err != nil {
		return fmt.Errorf("%w: DNS resolution failed for %q: %v", ErrBlockedURL, host, err)
	}

	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			continue
		}
		if isBlockedIP(ip) {
			return fmt.Errorf("%w: resolved IP %s is in a blocked range", ErrBlockedURL, ipStr)
		}
	}

	return nil
}

// isBlockedHost checks hostnames that are always blocked regardless of resolution.
func isBlockedHost(host string) bool {
	lower := strings.ToLower(host)

	blockedHosts := []string{
		"localhost",
		"metadata.google.internal",
		"metadata.google",
		"169.254.169.254",
	}
	for _, blocked := range blockedHosts {
		if lower == blocked {
			return true
		}
	}

	blockedSuffixes := []string{
		".internal",
		".local",
		".localhost",
	}
	for _, suffix := range blockedSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}

	return false
}

// isBlockedIP returns true if the IP is in a private, loopback, link-local,
// or otherwise blocked range.
func isBlockedIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}

	blocked := []struct {
		network string
		cidr    string
	}{
		{"AWS metadata", "169.254.169.254/32"},
		{"CGNAT", "100.64.0.0/10"},
		{"Benchmarking", "198.18.0.0/15"},
		{"Documentation", "192.0.2.0/24"},
		{"Documentation2", "198.51.100.0/24"},
		{"Documentation3", "203.0.113.0/24"},
		{"IPv6 unique local", "fc00::/7"},
	}

	for _, b := range blocked {
		_, cidr, err := net.ParseCIDR(b.cidr)
		if err != nil {
			continue
		}
		if cidr.Contains(ip) {
			return true
		}
	}

	return false
}
