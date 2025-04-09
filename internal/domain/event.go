package domain

import "time"

// Event represents a message passed through the event bus.
type Event struct {
	Topic     string      // Type or category of the event (e.g., "task.completed", "system.load.high")
	Data      interface{} // Payload of the event
	Timestamp time.Time   // When the event occurred
}

// NewEvent creates a new event.
func NewEvent(topic string, data interface{}) Event {
	return Event{
		Topic:     topic,
		Data:      data,
		Timestamp: time.Now(),
	}
}
