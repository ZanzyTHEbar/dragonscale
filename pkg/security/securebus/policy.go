package securebus

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/ZanzyTHEbar/dragonscale/pkg/itr"
	"github.com/ZanzyTHEbar/dragonscale/pkg/security"
	"github.com/ZanzyTHEbar/dragonscale/pkg/tools"
)

func globstarMatch(pattern, name string) bool {
	if matched, _ := filepath.Match(pattern, name); matched {
		return true
	}

	if !strings.Contains(pattern, "**") {
		return false
	}

	segments := strings.Split(filepath.Clean(pattern), string(filepath.Separator))
	paths := strings.Split(filepath.Clean(name), string(filepath.Separator))
	return globstarMatchSegments(segments, paths)
}

func globstarMatchSegments(patternSegments, pathSegments []string) bool {
	if len(patternSegments) == 0 {
		return len(pathSegments) == 0
	}

	if patternSegments[0] == "**" {
		if globstarMatchSegments(patternSegments[1:], pathSegments) {
			return true
		}
		for i := 0; i < len(pathSegments); i++ {
			if globstarMatchSegments(patternSegments[1:], pathSegments[i+1:]) {
				return true
			}
		}
		return false
	}

	if len(pathSegments) == 0 {
		return false
	}

	matched, err := filepath.Match(patternSegments[0], pathSegments[0])
	if err != nil || !matched {
		return false
	}
	return globstarMatchSegments(patternSegments[1:], pathSegments[1:])
}

// PolicyConfig controls global enforcement settings for the SecureBus.
type PolicyConfig struct {
	// MaxRecursionDepth limits how deep RLM sub-calls may nest.
	// 0 disables the limit (not recommended).
	MaxRecursionDepth uint8

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
		MaxRecursionDepth: 10,
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
	cfg         PolicyConfig
	validateURL func(string) error
}

// NewPolicyEngine creates a PolicyEngine with the given configuration.
func NewPolicyEngine(cfg PolicyConfig) *PolicyEngine {
	return &PolicyEngine{cfg: cfg, validateURL: security.ValidateURL}
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

// ValidateToolExecution checks explicit target arguments against a tool's
// declared capabilities. It intentionally covers only target fields the
// SecureBus can see safely before execution:
//   - url: network endpoint checks
//   - path: filesystem path checks for current first-party file tools
//   - working_dir: filesystem path checks for exec working directory
//
// Tool-internal targets (for example URLs constructed inside web_search or
// paths embedded inside shell commands) must still be enforced by the tool.
func (pe *PolicyEngine) ValidateToolExecution(te itr.ToolExec, args map[string]interface{}, caps tools.ToolCapabilities) error {
	if targetURL, ok := nonEmptyStringArg(args, "url"); ok {
		if err := pe.ValidateNetwork(targetURL, caps.Network); err != nil {
			return err
		}
	}

	if targetPath, ok := nonEmptyStringArg(args, "path"); ok {
		if mode := filesystemModeForTool(te.ToolName); mode != "" {
			if err := pe.ValidateFilesystem(pe.filesystemPolicyTarget(targetPath), mode, caps.Filesystem); err != nil {
				return err
			}
		}
	}

	if te.ToolName == "exec" {
		if workingDir, ok := nonEmptyStringArg(args, "working_dir"); ok {
			if err := pe.ValidateFilesystem(pe.filesystemPolicyTarget(workingDir), "rw", caps.Filesystem); err != nil {
				return err
			}
		}
	}

	return nil
}

func nonEmptyStringArg(args map[string]interface{}, key string) (string, bool) {
	val, ok := args[key].(string)
	if !ok || strings.TrimSpace(val) == "" {
		return "", false
	}
	return val, true
}

func filesystemModeForTool(toolName string) string {
	switch toolName {
	case "read_file", "list_dir":
		return "r"
	case "write_file", "append_file":
		return "w"
	case "edit_file":
		return "rw"
	default:
		return ""
	}
}

func (pe *PolicyEngine) filesystemPolicyTarget(targetPath string) string {
	if pe.cfg.AllowedWorkspace == "" || filepath.IsAbs(targetPath) {
		return targetPath
	}
	return filepath.Join(pe.cfg.AllowedWorkspace, targetPath)
}

// ValidateNetwork checks whether targetURL is permitted given the tool's
// EndpointRules and the global SSRF blocklist.
func (pe *PolicyEngine) ValidateNetwork(targetURL string, rules []tools.EndpointRule) error {
	parsed, err := url.Parse(targetURL)
	if err != nil {
		return fmt.Errorf("network access denied: invalid URL %q: %w", targetURL, err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("network access denied: URL %q uses unsupported scheme %q", targetURL, scheme)
	}
	if len(rules) == 0 {
		return fmt.Errorf("network access denied: tool declares no EndpointRules")
	}

	// Global SSRF check first — highest priority.
	for _, prefix := range pe.cfg.SSRFBlockedPrefixes {
		if strings.HasPrefix(targetURL, prefix) {
			return fmt.Errorf("network access denied: URL %q matches SSRF blocklist", targetURL)
		}
	}

	matchedRule := false
	for _, rule := range rules {
		if globstarMatch(rule.Pattern, targetURL) {
			matchedRule = true
			break
		}
	}
	if !matchedRule {
		return fmt.Errorf("network access denied: URL %q does not match any permitted endpoint pattern", targetURL)
	}

	validateURL := pe.validateURL
	if validateURL == nil {
		validateURL = security.ValidateURL
	}
	if err := validateURL(targetURL); err != nil {
		return fmt.Errorf("network access denied: URL %q matches SSRF blocklist: %w", targetURL, err)
	}
	return nil
}

// ValidateFilesystem checks whether targetPath is permitted given PathRules.
// targetPath should be an absolute path; it is compared lexically against
// patterns rooted at AllowedWorkspace.
//
// This helper is not the filesystem security boundary and does not resolve
// symlinks or eliminate TOCTOU races. Tools that touch the filesystem must
// still enforce path confinement at open/read/write time.
func (pe *PolicyEngine) ValidateFilesystem(targetPath, mode string, rules []tools.PathRule) error {
	if len(rules) == 0 {
		return fmt.Errorf("filesystem access denied: tool declares no PathRules")
	}

	// Normalise path relative to workspace if configured.
	checkPath := targetPath
	if pe.cfg.AllowedWorkspace != "" {
		rel, err := filepath.Rel(pe.cfg.AllowedWorkspace, targetPath)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("filesystem access denied: %q is outside allowed workspace %q", targetPath, pe.cfg.AllowedWorkspace)
		}
		checkPath = rel
	}

	for _, rule := range rules {
		if !globstarMatch(rule.Pattern, checkPath) {
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
