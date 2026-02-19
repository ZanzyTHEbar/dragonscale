package pcerrors

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// CLIHandler is the PicoClaw error lifecycle boundary for the CLI.
//
// It renders a user-facing message to Writer and returns an appropriate exit
// code. Optional Verbose and Redact flags layer on debug traces and sensitive-
// value scrubbing without requiring callers to change their error types.
type CLIHandler struct {
	// Writer is where the error message is written. Defaults to os.Stderr.
	Writer io.Writer
	// Verbose enables structured debug output: full error chain and error code.
	Verbose bool
	// Redact scrubs known patterns (API keys, tokens, passwords) before printing.
	Redact bool
	// JSON emits machine-readable JSON instead of plain text.
	JSON bool
}

// DefaultCLIHandler returns a CLIHandler with sane defaults for interactive use.
func DefaultCLIHandler() CLIHandler {
	return CLIHandler{Writer: os.Stderr}
}

func (h CLIHandler) Handle(err error) int {
	if err == nil {
		return 0
	}

	w := h.Writer
	if w == nil {
		w = os.Stderr
	}

	msg := h.renderMessage(err)

	if h.JSON {
		record := map[string]any{
			"error": msg,
			"code":  int(CodeOf(err)),
		}
		if h.Verbose {
			record["detail"] = err.Error()
		}
		data, _ := json.Marshal(record)
		_, _ = fmt.Fprintln(w, string(data))
	} else {
		_, _ = fmt.Fprintln(w, msg)
		if h.Verbose {
			if detail := err.Error(); detail != msg {
				_, _ = fmt.Fprintf(w, "  detail: %s\n", detail)
			}
			_, _ = fmt.Fprintf(w, "  code: %d\n", int(CodeOf(err)))
		}
	}

	return ExitCode(err)
}

// renderMessage produces the user-facing string, optionally redacted.
func (h CLIHandler) renderMessage(err error) string {
	msg := UserMessage(err)
	if h.Redact {
		msg = redactSensitive(msg)
	}
	return msg
}

// sensitivePatterns matches common secret-looking values in error messages.
var sensitivePatterns = []*regexp.Regexp{
	// API keys / bearer tokens embedded in URLs or strings (long alphanumeric strings)
	regexp.MustCompile(`(?i)(api[-_]?key|token|bearer|password|secret|auth)([=:\s]+)[A-Za-z0-9\-_\.]{12,}`),
	// sk-... OpenAI-style keys
	regexp.MustCompile(`\bsk-[A-Za-z0-9\-_]{20,}`),
	// Generic "key=VALUE" patterns (at least 12 chars after the separator)
	regexp.MustCompile(`\b([A-Za-z0-9_\-]{3,20})(=)[A-Za-z0-9\-_\.]{12,}`),
}

// redactSensitive replaces secret-looking substrings with a placeholder.
func redactSensitive(s string) string {
	for _, re := range sensitivePatterns {
		s = re.ReplaceAllStringFunc(s, func(match string) string {
			// Keep the key name / prefix; replace the value with [REDACTED].
			idx := strings.IndexAny(match, "=: ")
			if idx < 0 {
				return "[REDACTED]"
			}
			return match[:idx+1] + "[REDACTED]"
		})
	}
	return s
}

// UserMessage returns a friendly, stable message for humans.
//
// For structured errors, we prefer the top-level message (not the fully formatted builder.Error()).
func UserMessage(err error) string {
	if eb, ok := AsErrBuilder(err); ok && eb != nil {
		if eb.Msg != "" {
			return eb.Msg
		}
	}
	return err.Error()
}

// ExitCode maps error codes to process exit codes.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	switch CodeOf(err) {
	case CodeInvalidArgument, CodeFailedPrecondition, CodeOutOfRange:
		return 2
	case CodeUnauthenticated, CodePermissionDenied:
		return 3
	default:
		return 1
	}
}
