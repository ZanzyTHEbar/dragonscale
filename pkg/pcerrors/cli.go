package pcerrors

import (
	"fmt"
	"io"
	"os"
)

// CLIHandler is the PicoClaw error lifecycle boundary for the CLI.
//
// It is intentionally small: render a user-facing message and return an exit code.
// TODO: More advanced behaviors (structured logging, debug traces, redaction) can be layered on later.
type CLIHandler struct {
	Writer io.Writer
}

func DefaultCLIHandler() CLIHandler {
	return CLIHandler{Writer: os.Stderr}
}

func (h CLIHandler) Handle(err error) int {
	if err == nil {
		return 0
	}
	_, _ = fmt.Fprintln(h.Writer, UserMessage(err))
	return ExitCode(err)
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
