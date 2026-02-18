package security

import (
	"regexp"
	"strings"
)

// Redactor strips sensitive patterns from text before it reaches logs or storage.
type Redactor struct {
	patterns []*redactPattern
}

type redactPattern struct {
	re    *regexp.Regexp
	label string
}

// NewRedactor builds a Redactor with the default set of PII/secret patterns.
func NewRedactor() *Redactor {
	return &Redactor{patterns: defaultPatterns()}
}

// Redact replaces all matched secrets/PII in text with [REDACTED:<label>].
func (r *Redactor) Redact(text string) string {
	for _, p := range r.patterns {
		text = p.re.ReplaceAllString(text, "[REDACTED:"+p.label+"]")
	}
	return text
}

// ContainsSensitive returns true if any pattern matches the text.
func (r *Redactor) ContainsSensitive(text string) bool {
	for _, p := range r.patterns {
		if p.re.MatchString(text) {
			return true
		}
	}
	return false
}

func defaultPatterns() []*redactPattern {
	defs := []struct {
		pattern string
		label   string
	}{
		// Anthropic API keys (must come before generic sk- pattern)
		{`sk-ant-[A-Za-z0-9_-]{20,}`, "ANTHROPIC_KEY"},

		// OpenAI API keys (sk-..., sk-proj-...)
		{`sk-[A-Za-z0-9_-]{20,}`, "OPENAI_KEY"},

		// Generic Bearer tokens in header-like context
		{`Bearer\s+[A-Za-z0-9._~+/=-]{20,}`, "BEARER_TOKEN"},

		// AWS access key IDs (AKIA...)
		{`AKIA[0-9A-Z]{16}`, "AWS_ACCESS_KEY"},

		// AWS secret keys (40 chars base64-ish after common prefixes)
		{`(?i)aws_secret_access_key\s*[=:]\s*[A-Za-z0-9/+=]{40}`, "AWS_SECRET_KEY"},

		// GitHub tokens (ghp_, gho_, ghu_, ghs_, ghr_) — must come before generic SECRET_VALUE
		{`gh[pousr]_[A-Za-z0-9_]{36,}`, "GITHUB_TOKEN"},

		// Slack tokens (xoxb-, xoxp-, xoxs-, xoxa-)
		{`xox[bpsa]-[A-Za-z0-9-]{10,}`, "SLACK_TOKEN"},

		// Generic secret/password in key=value or key: value lines
		{`(?i)(password|secret|token|api_key|apikey|api-key)\s*[=:]\s*\S{8,}`, "SECRET_VALUE"},

		// SSH private key markers
		{`-----BEGIN (RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----`, "SSH_PRIVATE_KEY"},

		// JWT tokens (three base64url segments separated by dots)
		{`eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`, "JWT"},

		// Email addresses (basic)
		{`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`, "EMAIL"},

		// Credit card numbers (13-19 digits with optional separators)
		{`\b(?:\d[ -]*?){13,19}\b`, "CREDIT_CARD"},

		// US Social Security Numbers (XXX-XX-XXXX)
		{`\b\d{3}-\d{2}-\d{4}\b`, "SSN"},

		// IP addresses (to catch accidental logging of internal IPs)
		// Only match private ranges to avoid over-redacting
		{`\b(?:10\.\d{1,3}\.\d{1,3}\.\d{1,3}|172\.(?:1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3}|192\.168\.\d{1,3}\.\d{1,3})\b`, "PRIVATE_IP"},
	}

	patterns := make([]*redactPattern, 0, len(defs))
	for _, d := range defs {
		re, err := regexp.Compile(d.pattern)
		if err != nil {
			continue
		}
		patterns = append(patterns, &redactPattern{re: re, label: d.label})
	}
	return patterns
}

// RedactMap redacts all values in a string map, returning a new map.
func (r *Redactor) RedactMap(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		switch val := v.(type) {
		case string:
			out[k] = r.Redact(val)
		case map[string]interface{}:
			out[k] = r.RedactMap(val)
		default:
			out[k] = v
		}
	}
	return out
}

// MaskKey partially masks a key, showing only a prefix for identification.
func MaskKey(key string) string {
	if len(key) <= 8 {
		return strings.Repeat("*", len(key))
	}
	return key[:8] + strings.Repeat("*", len(key)-8)
}
