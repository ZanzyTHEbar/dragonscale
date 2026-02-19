// Package security provides hardened input handling for LLM-generated content.
package security

import (
	"errors"
	"fmt"
	jsonv2 "github.com/go-json-experiment/json"
	"strings"
)

// ErrNoJSON is returned when no valid JSON object is found in the input.
var ErrNoJSON = errors.New("no valid JSON object found in text")

// ErrInputTooLarge is returned when the input exceeds the maximum allowed size.
var ErrInputTooLarge = errors.New("input exceeds maximum allowed size")

const (
	defaultMaxInputBytes = 64 * 1024 // 64KB
)

// ExtractJSONOptions configures JSON extraction behavior.
type ExtractJSONOptions struct {
	MaxInputBytes         int  // Maximum input size in bytes. Default: 64KB.
	DisallowUnknownFields bool // Reject JSON with keys not in the target struct.
}

// ExtractJSON extracts the first valid JSON object from LLM-generated text and
// unmarshals it into dest. It handles common LLM output patterns:
//   - Raw JSON
//   - JSON wrapped in ```json ... ``` code fences
//   - JSON embedded in prose text
//
// It applies size limits and optionally rejects unknown fields, defending
// against prompt injection attacks that attempt to smuggle extra keys.
func ExtractJSON(text string, dest interface{}, opts *ExtractJSONOptions) error {
	if opts == nil {
		opts = &ExtractJSONOptions{}
	}
	maxBytes := opts.MaxInputBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxInputBytes
	}

	if len(text) > maxBytes {
		return fmt.Errorf("%w: %d bytes (max %d)", ErrInputTooLarge, len(text), maxBytes)
	}

	cleaned := extractJSONString(text)
	if cleaned == "" {
		return ErrNoJSON
	}

	var jsonOpts []jsonv2.Options
	if opts.DisallowUnknownFields {
		jsonOpts = append(jsonOpts, jsonv2.RejectUnknownMembers(true))
	}

	if err := jsonv2.Unmarshal([]byte(cleaned), dest, jsonOpts...); err != nil {
		return fmt.Errorf("json decode: %w", err)
	}
	return nil
}

// extractJSONString isolates the JSON object from surrounding text.
// Strategy (in priority order):
//  1. Try the text as-is (after trim)
//  2. Extract from ```json ... ``` code fence
//  3. Find the first { ... } balanced brace pair
func extractJSONString(text string) string {
	text = strings.TrimSpace(text)

	if isJSON(text) {
		return text
	}

	if extracted := extractFromCodeFence(text); extracted != "" && isJSON(extracted) {
		return extracted
	}

	if extracted := extractFirstBraced(text); extracted != "" && isJSON(extracted) {
		return extracted
	}

	return ""
}

func isJSON(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return false
	}
	return (s[0] == '{' && s[len(s)-1] == '}') || (s[0] == '[' && s[len(s)-1] == ']')
}

// extractFromCodeFence extracts content from the first ```json ... ``` fence.
func extractFromCodeFence(text string) string {
	lower := strings.ToLower(text)

	markers := []string{"```json", "```"}
	for _, marker := range markers {
		idx := strings.Index(lower, marker)
		if idx < 0 {
			continue
		}
		start := idx + len(marker)
		rest := text[start:]
		endIdx := strings.Index(rest, "```")
		if endIdx < 0 {
			continue
		}
		return strings.TrimSpace(rest[:endIdx])
	}
	return ""
}

// extractFirstBraced finds the first balanced { ... } in text.
// Handles nested braces and string literals containing braces.
func extractFirstBraced(text string) string {
	start := strings.IndexByte(text, '{')
	if start < 0 {
		return ""
	}

	depth := 0
	inString := false
	escaped := false

	for i := start; i < len(text); i++ {
		ch := text[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && inString {
			escaped = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch ch {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[start : i+1]
			}
		}
	}
	return ""
}

// SanitizeToolArgs validates that tool arguments conform to expected types and
// constraints. It returns the sanitized arguments or an error.
func SanitizeToolArgs(args map[string]interface{}, schema map[string]ArgSpec) (map[string]interface{}, error) {
	sanitized := make(map[string]interface{}, len(schema))

	for name, spec := range schema {
		val, exists := args[name]
		if !exists || val == nil {
			if spec.Required {
				return nil, fmt.Errorf("missing required argument: %s", name)
			}
			if spec.Default != nil {
				sanitized[name] = spec.Default
			}
			continue
		}

		coerced, err := coerceArg(val, spec.Type)
		if err != nil {
			return nil, fmt.Errorf("argument %q: %w", name, err)
		}

		if spec.MaxLength > 0 {
			if s, ok := coerced.(string); ok && len(s) > spec.MaxLength {
				return nil, fmt.Errorf("argument %q exceeds max length %d", name, spec.MaxLength)
			}
		}

		sanitized[name] = coerced
	}

	return sanitized, nil
}

// ArgSpec defines validation constraints for a single tool argument.
type ArgSpec struct {
	Type      ArgType     // Expected type.
	Required  bool        // Whether the argument is required.
	MaxLength int         // Maximum string length (0 = unlimited).
	Default   interface{} // Default value if not provided.
}

// ArgType represents the expected type of a tool argument.
type ArgType int

const (
	ArgString ArgType = iota
	ArgInt
	ArgFloat
	ArgBool
	ArgObject
	ArgArray
)

func coerceArg(val interface{}, expected ArgType) (interface{}, error) {
	switch expected {
	case ArgString:
		switch v := val.(type) {
		case string:
			return v, nil
		case float64:
			return fmt.Sprintf("%g", v), nil
		default:
			return nil, fmt.Errorf("expected string, got %T", val)
		}
	case ArgInt:
		switch v := val.(type) {
		case float64:
			return int(v), nil
		case int:
			return v, nil
		default:
			return nil, fmt.Errorf("expected integer, got %T", val)
		}
	case ArgFloat:
		switch v := val.(type) {
		case float64:
			return v, nil
		case int:
			return float64(v), nil
		default:
			return nil, fmt.Errorf("expected number, got %T", val)
		}
	case ArgBool:
		if b, ok := val.(bool); ok {
			return b, nil
		}
		return nil, fmt.Errorf("expected boolean, got %T", val)
	case ArgObject:
		if m, ok := val.(map[string]interface{}); ok {
			return m, nil
		}
		return nil, fmt.Errorf("expected object, got %T", val)
	case ArgArray:
		if a, ok := val.([]interface{}); ok {
			return a, nil
		}
		return nil, fmt.Errorf("expected array, got %T", val)
	default:
		return val, nil
	}
}
