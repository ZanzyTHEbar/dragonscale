package channels

import (
	"context"
	"testing"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/bus"
)

func TestBaseChannelIsAllowed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		allowList []string
		senderID  string
		want      bool
	}{
		{
			name:      "empty allowlist allows all",
			allowList: nil,
			senderID:  "anyone",
			want:      true,
		},
		{
			name:      "compound sender matches numeric allowlist",
			allowList: []string{"123456"},
			senderID:  "123456|alice",
			want:      true,
		},
		{
			name:      "compound sender matches username allowlist",
			allowList: []string{"@alice"},
			senderID:  "123456|alice",
			want:      true,
		},
		{
			name:      "numeric sender matches legacy compound allowlist",
			allowList: []string{"123456|alice"},
			senderID:  "123456",
			want:      true,
		},
		{
			name:      "non matching sender is denied",
			allowList: []string{"123456"},
			senderID:  "654321|bob",
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := NewBaseChannel("test", nil, nil, tt.allowList)
			if got := ch.IsAllowed(tt.senderID); got != tt.want {
				t.Fatalf("IsAllowed(%q) = %v, want %v", tt.senderID, got, tt.want)
			}
		})
	}
}

func TestBaseChannelHandleMessage_PreservesCompoundTelegramIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		allowList []string
		senderID  string
		wantSend  bool
	}{
		{
			name:      "numeric allowlist survives publish recheck",
			allowList: []string{"123456"},
			senderID:  "123456|alice",
			wantSend:  true,
		},
		{
			name:      "username allowlist survives publish recheck",
			allowList: []string{"@alice"},
			senderID:  "123456|alice",
			wantSend:  true,
		},
		{
			name:      "legacy compound allowlist survives publish recheck",
			allowList: []string{"123456|alice"},
			senderID:  "123456|alice",
			wantSend:  true,
		},
		{
			name:      "non matching sender stays blocked",
			allowList: []string{"@alice"},
			senderID:  "654321|bob",
			wantSend:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msgBus := bus.NewMessageBus()
			ch := NewBaseChannel("telegram", nil, msgBus, tt.allowList)
			ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()

			ch.HandleMessage(tt.senderID, "777", "hello", nil, map[string]string{"username": "alice"})

			msg, ok := msgBus.ConsumeInbound(ctx)
			if tt.wantSend {
				if !ok {
					t.Fatal("expected published inbound message")
				}
				if msg.SenderID != tt.senderID {
					t.Fatalf("expected sender %q, got %q", tt.senderID, msg.SenderID)
				}
				if msg.SessionKey != "telegram:777" {
					t.Fatalf("expected telegram session key, got %q", msg.SessionKey)
				}
				return
			}
			if ok {
				t.Fatalf("expected message to remain blocked, got %#v", msg)
			}
		})
	}
}
