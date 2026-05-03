package security

import (
	"net"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
)

func TestValidateURL_AllowedURLs(t *testing.T) {
	t.Parallel()
	tests := []string{
		"https://example.com",
		"https://api.openai.com/v1/chat",
		"https://www.google.com/robots.txt",
		"https://github.com/user/repo",
	}
	for _, u := range tests {
		t.Run(u, func(t *testing.T) {
			err := ValidateURL(u)
			assert.NoError(t, err)
		})
	}
}

func TestValidateURL_BlockedSchemes(t *testing.T) {
	t.Parallel()
	tests := []string{
		"file:///etc/passwd",
		"ftp://internal.server/data",
		"gopher://evil.com/payload",
		"javascript:alert(1)",
	}
	for _, u := range tests {
		t.Run(u, func(t *testing.T) {
			err := ValidateURL(u)
			assert.ErrorIs(t, err, ErrBlockedURL)
		})
	}
}

func TestValidateURL_BlockedHosts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		url  string
	}{
		{"localhost", "http://localhost/api"},
		{"metadata google", "http://metadata.google.internal/computeMetadata/v1/"},
		{"AWS metadata IP", "http://169.254.169.254/latest/meta-data/"},
		{"local suffix", "http://app.local/api"},
		{"localhost suffix", "http://service.localhost/api"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateURL(tc.url)
			assert.ErrorIs(t, err, ErrBlockedURL)
		})
	}
}

func TestValidateURL_BlockedIPs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		url  string
	}{
		{"loopback v4", "http://127.0.0.1/api"},
		{"loopback v6", "http://[::1]/api"},
		{"private 10.x", "http://10.0.0.1/secret"},
		{"private 172.x", "http://172.16.0.1/admin"},
		{"private 192.168.x", "http://192.168.1.1/config"},
		{"link-local", "http://169.254.1.1/data"},
		{"unspecified", "http://0.0.0.0/"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateURL(tc.url)
			assert.ErrorIs(t, err, ErrBlockedURL)
		})
	}
}

func TestValidateURL_BlockedDNSResolvedPrivateIP(t *testing.T) {
	err := validateURLWithLookup("https://public-looking.example/path", func(host string) ([]string, error) {
		return []string{"10.0.0.42"}, nil
	})

	assert.ErrorIs(t, err, ErrBlockedURL)
	assert.Contains(t, err.Error(), "resolved IP")
}

func TestValidateURL_EmptyAndInvalid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		url  string
	}{
		{"empty", ""},
		{"no scheme", "example.com"},
		{"whitespace", "   "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateURL(tc.url)
			assert.Error(t, err)
		})
	}
}

func TestIsBlockedIP(t *testing.T) {
	t.Parallel()
	tests := []struct {
		ip      string
		blocked bool
	}{
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"127.0.0.1", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"192.168.0.1", true},
		{"169.254.169.254", true},
		{"100.64.0.1", true},
		{"0.0.0.0", true},
	}
	for _, tc := range tests {
		t.Run(tc.ip, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("invalid IP: %s", tc.ip)
			}
			assert.Empty(t, cmp.Diff(tc.blocked, isBlockedIP(ip)))
		})
	}
}
