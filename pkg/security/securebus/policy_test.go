package securebus

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/itr"
	"github.com/sipeed/picoclaw/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPolicyValidateRecursionDepth(t *testing.T) {
	pe := NewPolicyEngine(PolicyConfig{MaxRecursionDepth: 5})

	req := itr.ToolRequest{Depth: 3}
	assert.NoError(t, pe.Validate(req, tools.ZeroCapabilities()))

	req.Depth = 6
	err := pe.Validate(req, tools.ZeroCapabilities())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "recursion depth")
}

func TestPolicyValidateNoDepthLimit(t *testing.T) {
	pe := NewPolicyEngine(PolicyConfig{MaxRecursionDepth: 0})

	req := itr.ToolRequest{Depth: 255}
	assert.NoError(t, pe.Validate(req, tools.ZeroCapabilities()))
}

func TestPolicyValidateNetworkSSRFBlocked(t *testing.T) {
	pe := NewPolicyEngine(DefaultPolicyConfig())

	ssrfURLs := []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.1/internal",
		"http://172.16.0.1/private",
		"http://192.168.1.1/admin",
		"http://localhost:8080/health",
		"http://127.0.0.1:3000/api",
		"https://169.254.169.254/token",
	}

	rules := []tools.EndpointRule{{Pattern: "*"}}

	for _, url := range ssrfURLs {
		t.Run(url, func(t *testing.T) {
			err := pe.ValidateNetwork(url, rules)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "SSRF blocklist")
		})
	}
}

func TestPolicyValidateNetworkAllowed(t *testing.T) {
	pe := NewPolicyEngine(DefaultPolicyConfig())
	rules := []tools.EndpointRule{{Pattern: "https://api.github.com/*"}}

	assert.NoError(t, pe.ValidateNetwork("https://api.github.com/repos", rules))
}

func TestPolicyValidateNetworkNoRules(t *testing.T) {
	pe := NewPolicyEngine(DefaultPolicyConfig())
	err := pe.ValidateNetwork("https://example.com", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no EndpointRules")
}

func TestPolicyValidateNetworkNoMatchingRule(t *testing.T) {
	pe := NewPolicyEngine(DefaultPolicyConfig())
	rules := []tools.EndpointRule{{Pattern: "https://api.github.com/*"}}

	err := pe.ValidateNetwork("https://evil.com/steal", rules)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match")
}

func TestPolicyValidateFilesystemAllowed(t *testing.T) {
	pe := NewPolicyEngine(PolicyConfig{AllowedWorkspace: "/workspace"})
	rules := []tools.PathRule{{Pattern: "src/*", Mode: "rw"}}

	assert.NoError(t, pe.ValidateFilesystem("/workspace/src/main.go", "r", rules))
	assert.NoError(t, pe.ValidateFilesystem("/workspace/src/main.go", "w", rules))
	assert.NoError(t, pe.ValidateFilesystem("/workspace/src/main.go", "rw", rules))
}

func TestPolicyValidateFilesystemNoRules(t *testing.T) {
	pe := NewPolicyEngine(PolicyConfig{})
	err := pe.ValidateFilesystem("/etc/passwd", "r", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no PathRules")
}

func TestPolicyValidateFilesystemModeMismatch(t *testing.T) {
	pe := NewPolicyEngine(PolicyConfig{AllowedWorkspace: "/workspace"})
	rules := []tools.PathRule{{Pattern: "data/*", Mode: "r"}}

	assert.NoError(t, pe.ValidateFilesystem("/workspace/data/file.csv", "r", rules))

	err := pe.ValidateFilesystem("/workspace/data/file.csv", "w", rules)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match")
}
