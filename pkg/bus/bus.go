package bus

import (
	"context"
	"sync"

	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
)

type MessageBus struct {
	inbound  chan InboundMessage
	outbound chan OutboundMessage
	handlers map[string]MessageHandler
	closed   bool
	mu       sync.RWMutex
}

func NewMessageBus() *MessageBus {
	return &MessageBus{
		inbound:  make(chan InboundMessage, 100),
		outbound: make(chan OutboundMessage, 100),
		handlers: make(map[string]MessageHandler),
	}
}

func (mb *MessageBus) PublishInbound(msg InboundMessage) {
	mb.mu.RLock()
	defer mb.mu.RUnlock()
	if mb.closed {
		return
	}
	mb.inbound <- msg
}

// TryPublishInbound attempts a non-blocking send. Returns false if the buffer
// is full (message dropped). Callers should log and/or notify the user.
func (mb *MessageBus) TryPublishInbound(msg InboundMessage) bool {
	select {
	case mb.inbound <- msg:
		return true
	default:
		logger.InfoCF("bus", "Inbound buffer full, message dropped",
			map[string]interface{}{
				"channel":   msg.Channel,
				"sender_id": msg.SenderID,
			})
		return false
	}
}

func (mb *MessageBus) ConsumeInbound(ctx context.Context) (InboundMessage, bool) {
	select {
	case msg := <-mb.inbound:
		return msg, true
	case <-ctx.Done():
		return InboundMessage{}, false
	}
}

func (mb *MessageBus) PublishOutbound(msg OutboundMessage) {
	mb.mu.RLock()
	defer mb.mu.RUnlock()
	if mb.closed {
		return
	}
	mb.outbound <- msg
}

func (mb *MessageBus) SubscribeOutbound(ctx context.Context) (OutboundMessage, bool) {
	select {
	case msg := <-mb.outbound:
		return msg, true
	case <-ctx.Done():
		return OutboundMessage{}, false
	}
}

func (mb *MessageBus) RegisterHandler(channel string, handler MessageHandler) {
	mb.mu.Lock()
	defer mb.mu.Unlock()
	mb.handlers[channel] = handler
}

func (mb *MessageBus) GetHandler(channel string) (MessageHandler, bool) {
	mb.mu.RLock()
	defer mb.mu.RUnlock()
	handler, ok := mb.handlers[channel]
	return handler, ok
}

func (mb *MessageBus) Close() {
	mb.mu.Lock()
	defer mb.mu.Unlock()
	if mb.closed {
		return
	}
	mb.closed = true
	close(mb.inbound)
	close(mb.outbound)
}
