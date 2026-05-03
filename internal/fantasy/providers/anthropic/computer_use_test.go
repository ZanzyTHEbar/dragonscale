package anthropic

import (
	"encoding/base64"
	"errors"
	"testing"

	"charm.land/fantasy"
	"charm.land/fantasy/internal/testcmp"
	"github.com/stretchr/testify/require"
)

func TestParseComputerUseInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    ComputerUseInput
		wantErr bool
	}{
		{
			name:  "screenshot",
			input: `{"action":"screenshot"}`,
			want:  ComputerUseInput{Action: ActionScreenshot},
		},
		{
			name:  "left_click with coordinate",
			input: `{"action":"left_click","coordinate":[100,200]}`,
			want: ComputerUseInput{
				Action:     ActionLeftClick,
				Coordinate: [2]int64{100, 200},
			},
		},
		{
			name:  "right_click with coordinate",
			input: `{"action":"right_click","coordinate":[50,75]}`,
			want: ComputerUseInput{
				Action:     ActionRightClick,
				Coordinate: [2]int64{50, 75},
			},
		},
		{
			name:  "double_click with coordinate",
			input: `{"action":"double_click","coordinate":[300,400]}`,
			want: ComputerUseInput{
				Action:     ActionDoubleClick,
				Coordinate: [2]int64{300, 400},
			},
		},
		{
			name:  "middle_click with coordinate",
			input: `{"action":"middle_click","coordinate":[10,20]}`,
			want: ComputerUseInput{
				Action:     ActionMiddleClick,
				Coordinate: [2]int64{10, 20},
			},
		},
		{
			name:  "mouse_move with coordinate",
			input: `{"action":"mouse_move","coordinate":[500,600]}`,
			want: ComputerUseInput{
				Action:     ActionMouseMove,
				Coordinate: [2]int64{500, 600},
			},
		},
		{
			name:  "left_click_drag with start_coordinate and coordinate",
			input: `{"action":"left_click_drag","start_coordinate":[10,20],"coordinate":[300,400]}`,
			want: ComputerUseInput{
				Action:          ActionLeftClickDrag,
				StartCoordinate: [2]int64{10, 20},
				Coordinate:      [2]int64{300, 400},
			},
		},
		{
			name:  "type with text",
			input: `{"action":"type","text":"hello world"}`,
			want: ComputerUseInput{
				Action: ActionType,
				Text:   "hello world",
			},
		},
		{
			name:  "key with text",
			input: `{"action":"key","text":"ctrl+c"}`,
			want: ComputerUseInput{
				Action: ActionKey,
				Text:   "ctrl+c",
			},
		},
		{
			name:  "scroll with coordinate direction and amount",
			input: `{"action":"scroll","coordinate":[960,540],"scroll_direction":"down","scroll_amount":3}`,
			want: ComputerUseInput{
				Action:          ActionScroll,
				Coordinate:      [2]int64{960, 540},
				ScrollDirection: "down",
				ScrollAmount:    3,
			},
		},
		{
			name:    "invalid JSON returns error",
			input:   `{not valid json}`,
			wantErr: true,
		},
		{
			name:  "triple_click with coordinate",
			input: `{"action":"triple_click","coordinate":[120,240]}`,
			want: ComputerUseInput{
				Action:     ActionTripleClick,
				Coordinate: [2]int64{120, 240},
			},
		},
		{
			name:  "left_mouse_down with coordinate",
			input: `{"action":"left_mouse_down","coordinate":[80,90]}`,
			want: ComputerUseInput{
				Action:     ActionLeftMouseDown,
				Coordinate: [2]int64{80, 90},
			},
		},
		{
			name:  "left_mouse_up with coordinate",
			input: `{"action":"left_mouse_up","coordinate":[80,90]}`,
			want: ComputerUseInput{
				Action:     ActionLeftMouseUp,
				Coordinate: [2]int64{80, 90},
			},
		},
		{
			name:  "wait",
			input: `{"action":"wait"}`,
			want:  ComputerUseInput{Action: ActionWait},
		},
		{
			name:  "zoom with region",
			input: `{"action":"zoom","region":[100,200,500,600]}`,
			want: ComputerUseInput{
				Action: ActionZoom,
				Region: [4]int64{100, 200, 500, 600},
			},
		},
		{
			name:  "left_click with modifier key",
			input: `{"action":"left_click","coordinate":[100,200],"text":"shift"}`,
			want: ComputerUseInput{
				Action:     ActionLeftClick,
				Coordinate: [2]int64{100, 200},
				Text:       "shift",
			},
		},
		{
			name:  "unknown action parses without error",
			input: `{"action":"future_action","coordinate":[1,2]}`,
			want: ComputerUseInput{
				Action:     ComputerAction("future_action"),
				Coordinate: [2]int64{1, 2},
			},
		},
		{
			name:  "hold_key with duration",
			input: `{"action":"hold_key","text":"shift","duration":2}`,
			want: ComputerUseInput{
				Action:   ActionHoldKey,
				Text:     "shift",
				Duration: 2,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			input, err := ParseComputerUseInput(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			testcmp.RequireEqual(t, tt.want, input)
		})
	}
}

func TestNewComputerUseScreenshotResult(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		toolCallID    string
		screenshotPNG []byte
		want          fantasy.ToolResultPart
	}{
		{
			name:          "base64 encodes PNG bytes",
			toolCallID:    "call-123",
			screenshotPNG: []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A},
			want: fantasy.ToolResultPart{
				ToolCallID: "call-123",
				Output: fantasy.ToolResultOutputContentMedia{
					MediaType: "image/png",
					Data:      base64.StdEncoding.EncodeToString([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A}),
				},
			},
		},
		{
			name:          "preserves tool call ID",
			toolCallID:    "tc_abc",
			screenshotPNG: []byte{0x01},
			want: fantasy.ToolResultPart{
				ToolCallID: "tc_abc",
				Output: fantasy.ToolResultOutputContentMedia{
					MediaType: "image/png",
					Data:      base64.StdEncoding.EncodeToString([]byte{0x01}),
				},
			},
		},
		{
			name:          "empty screenshot bytes",
			toolCallID:    "call-empty",
			screenshotPNG: []byte{},
			want: fantasy.ToolResultPart{
				ToolCallID: "call-empty",
				Output: fantasy.ToolResultOutputContentMedia{
					MediaType: "image/png",
				},
			},
		},
		{
			name:          "output content type is media",
			toolCallID:    "call-type",
			screenshotPNG: []byte{0xFF},
			want: fantasy.ToolResultPart{
				ToolCallID: "call-type",
				Output: fantasy.ToolResultOutputContentMedia{
					MediaType: "image/png",
					Data:      base64.StdEncoding.EncodeToString([]byte{0xFF}),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := NewComputerUseScreenshotResult(tt.toolCallID, tt.screenshotPNG)
			testcmp.RequireEqual(t, tt.want, result)
			testcmp.RequireEqual(t, fantasy.ToolResultContentTypeMedia, result.Output.GetType())
		})
	}
}

func TestNewComputerUseScreenshotResultWithMediaType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		toolCallID string
		base64Data string
		mediaType  string
		want       fantasy.ToolResultPart
	}{
		{
			name:       "custom media type and base64 data",
			toolCallID: "call-456",
			base64Data: base64.StdEncoding.EncodeToString([]byte("jpeg-data")),
			mediaType:  "image/jpeg",
			want: fantasy.ToolResultPart{
				ToolCallID: "call-456",
				Output: fantasy.ToolResultOutputContentMedia{
					MediaType: "image/jpeg",
					Data:      base64.StdEncoding.EncodeToString([]byte("jpeg-data")),
				},
			},
		},
		{
			name:       "preserves tool call ID",
			toolCallID: "tc_xyz",
			base64Data: "data",
			mediaType:  "image/webp",
			want: fantasy.ToolResultPart{
				ToolCallID: "tc_xyz",
				Output: fantasy.ToolResultOutputContentMedia{
					MediaType: "image/webp",
					Data:      "data",
				},
			},
		},
		{
			name:       "output content type is media",
			toolCallID: "call-type",
			base64Data: "data",
			mediaType:  "image/png",
			want: fantasy.ToolResultPart{
				ToolCallID: "call-type",
				Output: fantasy.ToolResultOutputContentMedia{
					MediaType: "image/png",
					Data:      "data",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := NewComputerUseScreenshotResultWithMediaType(
				tt.toolCallID,
				tt.base64Data,
				tt.mediaType,
			)
			testcmp.RequireEqual(t, tt.want, result)
			testcmp.RequireEqual(t, fantasy.ToolResultContentTypeMedia, result.Output.GetType())
		})
	}
}

func TestNewComputerUseErrorResult(t *testing.T) {
	t.Parallel()

	type errorResultProjection struct {
		ToolCallID string
		OutputType fantasy.ToolResultContentType
		Error      string
	}

	tests := []struct {
		name       string
		toolCallID string
		err        error
		want       errorResultProjection
	}{
		{
			name:       "error message propagates",
			toolCallID: "call-err",
			err:        errors.New("screenshot capture failed"),
			want: errorResultProjection{
				ToolCallID: "call-err",
				OutputType: fantasy.ToolResultContentTypeError,
				Error:      "screenshot capture failed",
			},
		},
		{
			name:       "preserves tool call ID",
			toolCallID: "tc_err",
			err:        errors.New("fail"),
			want: errorResultProjection{
				ToolCallID: "tc_err",
				OutputType: fantasy.ToolResultContentTypeError,
				Error:      "fail",
			},
		},
		{
			name:       "output content type is error",
			toolCallID: "call-type",
			err:        errors.New("oops"),
			want: errorResultProjection{
				ToolCallID: "call-type",
				OutputType: fantasy.ToolResultContentTypeError,
				Error:      "oops",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := NewComputerUseErrorResult(tt.toolCallID, tt.err)
			errOutput, ok := result.Output.(fantasy.ToolResultOutputContentError)
			require.True(t, ok, "output should be ToolResultOutputContentError")

			got := errorResultProjection{
				ToolCallID: result.ToolCallID,
				OutputType: result.Output.GetType(),
				Error:      errOutput.Error.Error(),
			}
			testcmp.RequireEqual(t, tt.want, got)
		})
	}
}

func TestNewComputerUseTextResult(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		toolCallID string
		text       string
		want       fantasy.ToolResultPart
	}{
		{
			name:       "text content is set",
			toolCallID: "call-txt",
			text:       "action completed successfully",
			want: fantasy.ToolResultPart{
				ToolCallID: "call-txt",
				Output: fantasy.ToolResultOutputContentText{
					Text: "action completed successfully",
				},
			},
		},
		{
			name:       "preserves tool call ID",
			toolCallID: "tc_text",
			text:       "hello",
			want: fantasy.ToolResultPart{
				ToolCallID: "tc_text",
				Output: fantasy.ToolResultOutputContentText{
					Text: "hello",
				},
			},
		},
		{
			name:       "empty text",
			toolCallID: "call-empty",
			want: fantasy.ToolResultPart{
				ToolCallID: "call-empty",
				Output:     fantasy.ToolResultOutputContentText{},
			},
		},
		{
			name:       "output content type is text",
			toolCallID: "call-type",
			text:       "test",
			want: fantasy.ToolResultPart{
				ToolCallID: "call-type",
				Output: fantasy.ToolResultOutputContentText{
					Text: "test",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := NewComputerUseTextResult(tt.toolCallID, tt.text)
			testcmp.RequireEqual(t, tt.want, result)
			testcmp.RequireEqual(t, fantasy.ToolResultContentTypeText, result.Output.GetType())
		})
	}
}
