package security

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRedactor_APIKeys(t *testing.T) {
	r := NewRedactor()
	tests := []struct {
		name  string
		input string
		label string
	}{
		{"OpenAI key", "my key is sk-abc123XYZdefghijklmnopqrs", "OPENAI_KEY"},
		{"OpenAI proj key", "using sk-proj-abc123XYZdefghijklmnopqrs", "OPENAI_KEY"},
		{"Anthropic key", "key: sk-ant-abc123XYZdefghijklmnopqrs", "ANTHROPIC_KEY"},
		{"GitHub token", "using ghp_abcdefghijklmnopqrstuvwxyz1234567890 here", "GITHUB_TOKEN"},
		{"Slack bot token", "token xoxb-123456789-abcdefghijk", "SLACK_TOKEN"},
		{"AWS access key", "AKIAIOSFODNN7EXAMPLE", "AWS_ACCESS_KEY"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := r.Redact(tc.input)
			assert.Contains(t, out, "[REDACTED:"+tc.label+"]")
			assert.True(t, r.ContainsSensitive(tc.input))
		})
	}
}

func TestRedactor_Bearer(t *testing.T) {
	r := NewRedactor()
	input := "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.xxxxx"
	out := r.Redact(input)
	assert.Contains(t, out, "[REDACTED:")
}

func TestRedactor_JWT(t *testing.T) {
	r := NewRedactor()
	jwt := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"
	out := r.Redact(jwt)
	assert.Contains(t, out, "[REDACTED:JWT]")
}

func TestRedactor_PII(t *testing.T) {
	r := NewRedactor()
	tests := []struct {
		name  string
		input string
		label string
	}{
		{"email", "contact john.doe@example.com please", "EMAIL"},
		{"SSN", "SSN: 123-45-6789", "SSN"},
		{"private IP", "connecting to 192.168.1.100", "PRIVATE_IP"},
		{"10.x IP", "server at 10.0.0.1", "PRIVATE_IP"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := r.Redact(tc.input)
			assert.Contains(t, out, "[REDACTED:"+tc.label+"]")
		})
	}
}

func TestRedactor_SecretValues(t *testing.T) {
	r := NewRedactor()
	tests := []struct {
		name  string
		input string
	}{
		{"password=", "password=SuperSecret123!"},
		{"api_key:", "api_key: sk_live_1234567890"},
		{"token=", "token=abcdefgh12345678"},
		{"SSH key header", "-----BEGIN RSA PRIVATE KEY-----"},
		{"SSH key EC", "-----BEGIN EC PRIVATE KEY-----"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := r.Redact(tc.input)
			assert.Contains(t, out, "[REDACTED:")
		})
	}
}

func TestRedactor_SafeText(t *testing.T) {
	r := NewRedactor()
	safe := "This is a normal log message about processing 42 items."
	assert.Equal(t, safe, r.Redact(safe))
	assert.False(t, r.ContainsSensitive(safe))
}

func TestRedactor_RedactMap(t *testing.T) {
	r := NewRedactor()
	m := map[string]interface{}{
		"command": "curl -H 'Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.xxxxxxxxxxxxxxxxxxxx.yyyyyyyy'",
		"output":  "normal output",
		"nested": map[string]interface{}{
			"secret": "password=hunter2hunter2",
		},
		"count": 42,
	}
	out := r.RedactMap(m)
	assert.Contains(t, out["command"].(string), "[REDACTED:")
	assert.Equal(t, "normal output", out["output"])
	nested := out["nested"].(map[string]interface{})
	assert.Contains(t, nested["secret"].(string), "[REDACTED:")
	assert.Equal(t, 42, out["count"])
}

func TestMaskKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"sk-abc123XYZdefghijklmno", "sk-abc12****************"},
		{"short", "*****"},
		{"12345678", "********"},
		{"123456789", "12345678*"},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			assert.Equal(t, tc.want, MaskKey(tc.input))
		})
	}
}
