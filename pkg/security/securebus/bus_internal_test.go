package securebus

import (
	"context"
	"fmt"
	"testing"

	"github.com/ZanzyTHEbar/dragonscale/pkg/itr"
	"github.com/ZanzyTHEbar/dragonscale/pkg/security"
	"github.com/ZanzyTHEbar/dragonscale/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBus_PolicyViolation_DNSResolvedPrivateURLBeforeSecretsAndExecution(t *testing.T) {
	t.Parallel()
	called := false
	capLookup := func(name string) (tools.ToolCapabilities, bool) {
		if name == "web_fetch" {
			return tools.ToolCapabilities{Network: []tools.EndpointRule{{Pattern: "https://**"}}}, true
		}
		return tools.ZeroCapabilities(), false
	}
	executor := func(ctx context.Context, name string, args map[string]interface{}) *tools.ToolResult {
		called = true
		return &tools.ToolResult{ForLLM: "should not run"}
	}
	bus := New(DefaultBusConfig(), nil, capLookup, executor)
	defer bus.Close()
	bus.policy.validateURL = func(rawURL string) error {
		return fmt.Errorf("%w: resolved IP 10.0.0.42 is in a blocked range", security.ErrBlockedURL)
	}

	req := itr.NewToolExecRequest("req-dns-ssrf", "sess", "tc-dns-ssrf", "web_fetch", `{"url":"https://public-looking.example/private"}`)
	resp := bus.Execute(t.Context(), req)

	assert.True(t, resp.IsError)
	assert.False(t, called, "policy violation should happen before tool execution")
	assert.Contains(t, resp.Result, "policy violation")
	events := bus.AuditLog().Events()
	require.Len(t, events, 1)
	assert.NotEmpty(t, events[0].PolicyViolation)
	assert.Empty(t, events[0].SecretsAccessed)
}
