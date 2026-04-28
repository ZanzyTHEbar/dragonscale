package fantasy

import "testing"

func TestResponseContentText_ConcatenatesMultipleTextParts(t *testing.T) {
	t.Parallel()

	content := ResponseContent{
		TextContent{Text: "first "},
		ReasoningContent{Text: "internal only"},
		TextContent{Text: "second"},
	}

	if got := content.Text(); got != "first second" {
		t.Fatalf("expected concatenated text parts, got %q", got)
	}
}
