package securebus

import (
	"fmt"
	"testing"

	"github.com/ZanzyTHEbar/dragonscale/pkg/itr"
	"github.com/ZanzyTHEbar/dragonscale/pkg/security"
	"github.com/ZanzyTHEbar/dragonscale/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPolicyValidateRecursionDepth(t *testing.T) {
	t.Parallel()
	pe := NewPolicyEngine(PolicyConfig{MaxRecursionDepth: 5})

	req := itr.ToolRequest{Depth: 3}
	assert.NoError(t, pe.Validate(req, tools.ZeroCapabilities()))

	req.Depth = 6
	err := pe.Validate(req, tools.ZeroCapabilities())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "recursion depth")
}

func TestPolicyValidateNoDepthLimit(t *testing.T) {
	t.Parallel()
	pe := NewPolicyEngine(PolicyConfig{MaxRecursionDepth: 0})

	req := itr.ToolRequest{Depth: 255}
	assert.NoError(t, pe.Validate(req, tools.ZeroCapabilities()))
}

func TestPolicyValidateNetworkSSRFBlocked(t *testing.T) {
	t.Parallel()
	pe := NewPolicyEngine(DefaultPolicyConfig())

	ssrfURLs := []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.1/internal",
		"http://172.16.0.1/private",
		"http://172.17.0.1/private",
		"https://172.20.1.2/path",
		"https://172.31.0.1:8443/admin",
		"http://192.168.1.1/admin",
		"http://localhost:8080/health",
		"http://localhost./health",
		"http://service.localhost/api",
		"http://127.0.0.1:3000/api",
		"http://[::1]/api",
		"https://169.254.169.254/token",
	}

	rules := []tools.EndpointRule{{Pattern: "http://**"}, {Pattern: "https://**"}}

	for _, url := range ssrfURLs {
		t.Run(url, func(t *testing.T) {
			err := pe.ValidateNetwork(url, rules)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "SSRF blocklist")
		})
	}
}

func TestPolicyValidateNetworkPrivateBoundaryAllowed(t *testing.T) {
	t.Parallel()
	pe := NewPolicyEngine(DefaultPolicyConfig())
	rules := []tools.EndpointRule{{Pattern: "http://**"}}

	assert.NoError(t, pe.ValidateNetwork("http://172.15.0.1/x", rules))
	assert.NoError(t, pe.ValidateNetwork("http://172.32.0.1/x", rules))
}

func TestPolicyValidateNetworkDNSResolvedPrivateIPBlocked(t *testing.T) {
	t.Parallel()
	pe := NewPolicyEngine(DefaultPolicyConfig())
	pe.validateURL = func(rawURL string) error {
		return fmt.Errorf("%w: resolved IP 10.0.0.42 is in a blocked range", security.ErrBlockedURL)
	}
	rules := []tools.EndpointRule{{Pattern: "https://**"}}

	err := pe.ValidateNetwork("https://public-looking.example/path", rules)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SSRF blocklist")
	assert.ErrorIs(t, err, security.ErrBlockedURL)
}

func TestPolicyValidateNetworkAllowed(t *testing.T) {
	t.Parallel()
	pe := NewPolicyEngine(DefaultPolicyConfig())
	rules := []tools.EndpointRule{{Pattern: "https://1.1.1.1/*"}}

	assert.NoError(t, pe.ValidateNetwork("https://1.1.1.1/repos", rules))
}

func TestPolicyValidateNetworkNoRules(t *testing.T) {
	t.Parallel()
	pe := NewPolicyEngine(DefaultPolicyConfig())
	err := pe.ValidateNetwork("https://example.com", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no EndpointRules")
}

func TestPolicyValidateNetworkUnsupportedScheme(t *testing.T) {
	t.Parallel()
	pe := NewPolicyEngine(DefaultPolicyConfig())
	rules := []tools.EndpointRule{{Pattern: "*"}}

	err := pe.ValidateNetwork("file:///etc/passwd", rules)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported scheme")
}

func TestPolicyValidateNetworkNoMatchingRule(t *testing.T) {
	t.Parallel()
	pe := NewPolicyEngine(DefaultPolicyConfig())
	rules := []tools.EndpointRule{{Pattern: "https://8.8.8.8/*"}}

	err := pe.ValidateNetwork("https://1.1.1.1/steal", rules)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match")
}

func TestPolicyValidateFilesystemAllowed(t *testing.T) {
	t.Parallel()
	pe := NewPolicyEngine(PolicyConfig{AllowedWorkspace: "/workspace"})
	rules := []tools.PathRule{{Pattern: "src/*", Mode: "rw"}}

	assert.NoError(t, pe.ValidateFilesystem("/workspace/src/main.go", "r", rules))
	assert.NoError(t, pe.ValidateFilesystem("/workspace/src/main.go", "w", rules))
	assert.NoError(t, pe.ValidateFilesystem("/workspace/src/main.go", "rw", rules))
}

func TestPolicyValidateFilesystemNoRules(t *testing.T) {
	t.Parallel()
	pe := NewPolicyEngine(PolicyConfig{})
	err := pe.ValidateFilesystem("/etc/passwd", "r", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no PathRules")
}

func TestPolicyValidateFilesystemModeMismatch(t *testing.T) {
	t.Parallel()
	pe := NewPolicyEngine(PolicyConfig{AllowedWorkspace: "/workspace"})
	rules := []tools.PathRule{{Pattern: "data/*", Mode: "r"}}

	assert.NoError(t, pe.ValidateFilesystem("/workspace/data/file.csv", "r", rules))

	err := pe.ValidateFilesystem("/workspace/data/file.csv", "w", rules)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match")
}

func TestPolicyValidateFilesystemGlobstarAllowed(t *testing.T) {
	t.Parallel()
	pe := NewPolicyEngine(PolicyConfig{AllowedWorkspace: "/workspace"})
	rules := []tools.PathRule{{Pattern: "**", Mode: "rw"}}

	assert.NoError(t, pe.ValidateFilesystem("/workspace/src/pkg/main.go", "r", rules))
	assert.NoError(t, pe.ValidateFilesystem("/workspace/src/pkg/main.go", "w", rules))
}

func TestPolicyValidateFilesystemGlobstarDeniedOutsideWorkspace(t *testing.T) {
	t.Parallel()
	pe := NewPolicyEngine(PolicyConfig{AllowedWorkspace: "/workspace"})
	rules := []tools.PathRule{{Pattern: "**", Mode: "rw"}}

	err := pe.ValidateFilesystem("/etc/passwd", "r", rules)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outside allowed workspace")
}

func TestPolicyValidateFilesystemAllowsDotDotPrefixedChild(t *testing.T) {
	t.Parallel()
	pe := NewPolicyEngine(PolicyConfig{AllowedWorkspace: "/workspace"})
	rules := []tools.PathRule{{Pattern: "**", Mode: "rw"}}

	assert.NoError(t, pe.ValidateFilesystem("/workspace/..cache/config.json", "r", rules))
}

func TestPolicyValidateNetworkGlobstarAllowed(t *testing.T) {
	t.Parallel()
	pe := NewPolicyEngine(DefaultPolicyConfig())
	rules := []tools.EndpointRule{{Pattern: "https://**"}}

	assert.NoError(t, pe.ValidateNetwork("https://1.1.1.1/repos", rules))
	assert.NoError(t, pe.ValidateNetwork("https://8.8.8.8/deep/path", rules))
}
