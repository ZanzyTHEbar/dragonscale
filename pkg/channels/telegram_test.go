package channels

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/bus"
	"github.com/mymmrac/telego"
	"github.com/mymmrac/telego/telegoapi"
)

type fakeTelegramCaller struct {
	mu      sync.Mutex
	methods []string
}

func (c *fakeTelegramCaller) Call(_ context.Context, url string, _ *telegoapi.RequestData) (*telegoapi.Response, error) {
	method := url[strings.LastIndex(url, "/")+1:]

	c.mu.Lock()
	c.methods = append(c.methods, method)
	c.mu.Unlock()

	switch method {
	case "sendChatAction":
		return &telegoapi.Response{Ok: true, Result: []byte(`true`)}, nil
	case "sendMessage":
		return &telegoapi.Response{Ok: true, Result: []byte(`{"message_id":1,"date":0,"chat":{"id":777,"type":"private"}}`)}, nil
	default:
		return &telegoapi.Response{Ok: true, Result: []byte(`true`)}, nil
	}
}

func (c *fakeTelegramCaller) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.methods)
}

func (c *fakeTelegramCaller) methodCalls() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.methods...)
}

func newTestTelegramChannel(t *testing.T, allowList []string) (*TelegramChannel, *bus.MessageBus, *fakeTelegramCaller) {
	t.Helper()

	msgBus := bus.NewMessageBus()
	caller := &fakeTelegramCaller{}
	bot, err := telego.NewBot("123456:abcdefghijklmnopqrstuvwxyzabcdefghi", telego.WithAPICaller(caller))
	if err != nil {
		t.Fatalf("new telegram bot: %v", err)
	}

	return &TelegramChannel{
		BaseChannel:  NewBaseChannel("telegram", nil, msgBus, allowList),
		bot:          bot,
		placeholders: sync.Map{},
		stopThinking: sync.Map{},
	}, msgBus, caller
}

func TestTelegramChannelHandleMessage_PreservesCompoundIdentity(t *testing.T) {
	tests := []struct {
		name      string
		allowList []string
	}{
		{
			name:      "numeric allowlist",
			allowList: []string{"123456"},
		},
		{
			name:      "username allowlist",
			allowList: []string{"@alice"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch, msgBus, caller := newTestTelegramChannel(t, tt.allowList)
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()

			err := ch.handleMessage(ctx, &telego.Message{
				MessageID: 42,
				From: &telego.User{
					ID:        123456,
					Username:  "alice",
					FirstName: "Alice",
				},
				Chat: telego.Chat{ID: 777, Type: "private"},
				Text: "hello",
			})
			if err != nil {
				t.Fatalf("handleMessage returned error: %v", err)
			}

			msg, ok := msgBus.ConsumeInbound(ctx)
			if !ok {
				t.Fatal("expected published inbound message")
			}
			if msg.SenderID != "123456|alice" {
				t.Fatalf("SenderID = %q, want %q", msg.SenderID, "123456|alice")
			}
			if msg.ChatID != "777" {
				t.Fatalf("ChatID = %q, want %q", msg.ChatID, "777")
			}
			if msg.SessionKey != "telegram:777" {
				t.Fatalf("SessionKey = %q, want %q", msg.SessionKey, "telegram:777")
			}
			if msg.Metadata["username"] != "alice" {
				t.Fatalf("metadata username = %q, want %q", msg.Metadata["username"], "alice")
			}
			if msg.Metadata["user_id"] != "123456" {
				t.Fatalf("metadata user_id = %q, want %q", msg.Metadata["user_id"], "123456")
			}
			if got := caller.methodCalls(); len(got) != 2 || got[0] != "sendChatAction" || got[1] != "sendMessage" {
				t.Fatalf("Telegram API calls = %#v, want %#v", got, []string{"sendChatAction", "sendMessage"})
			}
		})
	}
}

func TestTelegramChannelHandleMessage_RejectsDisallowedCompoundSender(t *testing.T) {
	ch, msgBus, caller := newTestTelegramChannel(t, []string{"@bob"})
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	err := ch.handleMessage(ctx, &telego.Message{
		MessageID: 42,
		From: &telego.User{
			ID:       123456,
			Username: "alice",
		},
		Chat: telego.Chat{ID: 777, Type: "private"},
		Text: "hello",
	})
	if err != nil {
		t.Fatalf("handleMessage returned error: %v", err)
	}

	if msg, ok := msgBus.ConsumeInbound(ctx); ok {
		t.Fatalf("expected no inbound message, got %#v", msg)
	}
	if caller.callCount() != 0 {
		t.Fatalf("Telegram API call count = %d, want 0", caller.callCount())
	}
}

func TestTelegramChannelHandleMessage_TracksChatIDsConcurrently(t *testing.T) {
	ch, _, _ := newTestTelegramChannel(t, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	const messages = 50
	errCh := make(chan error, messages)
	var wg sync.WaitGroup
	for i := 0; i < messages; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			userID := int64(1000 + i)
			chatID := int64(7000 + i)
			if err := ch.handleMessage(ctx, &telego.Message{
				MessageID: i + 1,
				From: &telego.User{
					ID:       userID,
					Username: fmt.Sprintf("user%d", i),
				},
				Chat: telego.Chat{ID: chatID, Type: "private"},
				Text: fmt.Sprintf("hello %d", i),
			}); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("handleMessage returned error: %v", err)
		}
	}

	seen := 0
	ch.chatIDs.Range(func(key, value interface{}) bool {
		seen++
		senderID, ok := key.(string)
		if !ok || senderID == "" {
			t.Errorf("stored sender key = %#v, want non-empty string", key)
		}
		if _, ok := value.(int64); !ok {
			t.Errorf("stored chat ID for %q = %#v, want int64", senderID, value)
		}
		return true
	})
	if seen != messages {
		t.Fatalf("tracked chat IDs = %d, want %d", seen, messages)
	}
}
