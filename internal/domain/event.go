package domain

import (
	"encoding/json"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/internal/utils"
)

// EventFormat represents the format of event data
type EventFormat string

// Event formats supported by the system
const (
	JSONFormat    EventFormat = "json"
	ProtobufFormat EventFormat = "protobuf"
	RawFormat     EventFormat = "raw"
)

// EventVersion represents the version of an event schema
type EventVersion string

// Common event versions
const (
	V1 EventVersion = "v1"
	V2 EventVersion = "v2"
)

// Event represents a message passed through the event bus.
type Event struct {
	ID        string                 // Unique identifier for the event
	Topic     string                 // Type or category of the event (e.g., "task.completed", "system.load.high")
	Data      interface{}            // Payload of the event
	Timestamp time.Time              // When the event occurred
	Version   EventVersion           // Schema version of the event
	Format    EventFormat            // Format of the event data (JSON, Protobuf, etc.)
	Attributes map[string]interface{} // Additional metadata for filtering
}

// NewEvent creates a new event with default settings.
func NewEvent(topic string, data interface{}) Event {
	return Event{
		ID:        utils.GenerateEventID(),
		Topic:     topic,
		Data:      data,
		Timestamp: time.Now(),
		Version:   V1,
		Format:    JSONFormat,
		Attributes: make(map[string]interface{}),
	}
}

// NewEventWithAttributes creates an event with custom attributes for filtering.
func NewEventWithAttributes(topic string, data interface{}, attributes map[string]interface{}) Event {
	event := NewEvent(topic, data)
	if attributes != nil {
		event.Attributes = attributes
	}
	return event
}

// WithVersion sets the version of the event.
func (e Event) WithVersion(version EventVersion) Event {
	e.Version = version
	return e
}

// WithFormat sets the format of the event data.
func (e Event) WithFormat(format EventFormat) Event {
	e.Format = format
	return e
}

// ToJSON serializes the event to JSON.
func (e Event) ToJSON() ([]byte, error) {
	return json.Marshal(e)
}

// Match checks if the event matches the given filter attributes.
func (e Event) Match(filter map[string]interface{}) bool {
	if len(filter) == 0 {
		return true
	}
	
	for k, v := range filter {
		if attrValue, exists := e.Attributes[k]; !exists || attrValue != v {
			return false
		}
	}
	return true
}
