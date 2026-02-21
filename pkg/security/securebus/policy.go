package securebus

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ZanzyTHEbar/dragonscale/pkg/itr"
	"github.com/ZanzyTHEbar/dragonscale/pkg/tools"
)

// PolicyConfig controls global enforcement settings for the SecureBus.
type PolicyConfig struct {
	// MaxRecursionDepth limits how deep RLM sub-calls may nest.
	// 0 disables the limit (not recommended).
	MaxRecursionDepth uint8

	// MaxTokensPerRequest is a soft cap on cost_tokens for a single request.
	// 0 disables the cap.
	MaxTokensPerRequest uint32

	// AllowedWorkspace is the filesystem root all PathRule patterns are
	// evaluated relative to. Empty string disables workspace restriction.
	AllowedWorkspace string

	// SSRFBlockedPrefixes is a list of URL prefixes that are never permitted,
	// regardless of what a tool declares in its Network capabilities.
	// Applied in addition to the URL guard in pkg/security.
	SSRFBlockedPrefixes []string
}

// DefaultPolicyConfig returns a secure-by-default configuration.
func DefaultPolicyConfig() PolicyConfig {
	return PolicyConfig{
		MaxRecursionDepth:   10,
		MaxTokensPerRequest: 0, // no hard cap; individual budgets set per call
		SSRFBlockedPrefixes: []string{
			"http://169.254.", // AWS/GCP metadata
			"http://10.",      // RFC 1918
			"http://172.16.",  // RFC 1918
			"http://192.168.", // RFC 1918
			"http://localhost",
			"http://127.",
			"https://169.254.",
			"https://10.",
			"https://172.16.",
			"https://192.168.",
			"https://localhost",
			"https://127.",
		},
	}
}

// PolicyEngine validates requests against a policy config and tool capabilities.
type PolicyEngine struct {
	cfg PolicyConfig
}

// NewPolicyEngine creates a PolicyEngine with the given configuration.
func NewPolicyEngine(cfg PolicyConfig) *PolicyEngine {
	return &PolicyEngine{cfg: cfg}
}

// Validate returns an error if req violates any policy rule.
func (pe *PolicyEngine) Validate(req itr.ToolRequest, caps tools.ToolCapabilities) error {
	if pe.cfg.MaxRecursionDepth > 0 && req.Depth > pe.cfg.MaxRecursionDepth {
		return fmt.Errorf("recursion depth %d exceeds limit %d", req.Depth, pe.cfg.MaxRecursionDepth)
	}

	if te, ok := req.Payload.(itr.ToolExec); ok {
		_ = te // tool name validation happens in Bus.dispatch
	}

	return nil
}

// ValidateNetwork checks whether targetURL is permitted given the tool's
// EndpointRules and the global SSRF blocklist.
func (pe *PolicyEngine) ValidateNetwork(targetURL string, rules []tools.EndpointRule) error {
	// Global SSRF check first — highest priority.
	for _, prefix := range pe.cfg.SSRFBlockedPrefixes {
		if strings.HasPrefix(targetURL, prefix) {
			return fmt.Errorf("network access denied: URL %q matches SSRF blocklist", targetURL)
		}
	}

	// If no rules declared, deny all network access.
	if len(rules) == 0 {
		return fmt.Errorf("network access denied: tool declares no EndpointRules")
	}

	// Must match at least one permit rule.
	for _, rule := range rules {
		matched, _ := filepath.Match(rule.Pattern, targetURL)
		if matched {
			return nil
		}
	}
	return fmt.Errorf("network access denied: URL %q does not match any permitted endpoint pattern", targetURL)
}

// ValidateFilesystem checks whether targetPath is permitted given PathRules.
// targetPath should be an absolute path; it is compared against patterns
// rooted at AllowedWorkspace.
func (pe *PolicyEngine) ValidateFilesystem(targetPath, mode string, rules []tools.PathRule) error {
	if len(rules) == 0 {
		return fmt.Errorf("filesystem access denied: tool declares no PathRules")
	}

	// Normalise path relative to workspace if configured.
	checkPath := targetPath
	if pe.cfg.AllowedWorkspace != "" {
		rel, err := filepath.Rel(pe.cfg.AllowedWorkspace, targetPath)
		if err == nil && !strings.HasPrefix(rel, "..") {
			checkPath = rel
		}
	}

	for _, rule := range rules {
		matched, _ := filepath.Match(rule.Pattern, checkPath)
		if !matched {
			continue
		}
		// Check mode compatibility.
		switch mode {
		case "r":
			if strings.Contains(rule.Mode, "r") {
				return nil
			}
		case "w":
			if strings.Contains(rule.Mode, "w") {
				return nil
			}
		case "rw":
			if rule.Mode == "rw" {
				return nil
			}
		}
	}
	return fmt.Errorf("filesystem access denied: %q (mode %q) does not match any permitted PathRule", targetPath, mode)
}
