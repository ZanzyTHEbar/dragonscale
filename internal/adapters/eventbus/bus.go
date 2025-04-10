package eventbus

import (
	"fmt" // Added for error formatting
	"log"
	"sync"

	"github.com/ZanzyTHEbar/dragonscale/internal/domain"
)

// Subscriber is a channel that receives events for a specific topic.
// Use a buffered channel to avoid blocking the publisher.
type Subscriber = chan domain.Event

// EventFilter defines a function type for filtering events.
type EventFilter = func(domain.Event) bool

// EventBus defines the interface for publishing and subscribing to events.
type EventBus interface {
	Publish(event domain.Event)
	Subscribe(topic string, bufferSize int) (Subscriber, error)
	SubscribeWithFilter(topic string, filter EventFilter, bufferSize int) (Subscriber, error)
	Unsubscribe(topic string, sub Subscriber) error
	Stop()
	GetTransport() string
	SwitchTransport(transportType string) error
}

// SimpleEventBus is a basic in-memory event bus implementation using channels.
type SimpleEventBus struct {
	subscribers     map[string]map[Subscriber]bool          // Map topic to a Set of subscriber channels
	filteredSubs    map[string]map[*filteredSubscriber]bool // Map topic to filtered subscribers
	mu              sync.RWMutex                            // Protects subscribers map
	stopChan        chan struct{}                           // To signal shutdown
	isStopped       bool                                    // Atomic bool could also be used
	transportType   string                                  // Current transport mechanism (memory, http, grpc, kafka, etc.)
	eventSerializer EventSerializer                         // Serializer for converting events to different formats
}

// filteredSubscriber combines a subscriber channel with its filter
type filteredSubscriber struct {
	channel Subscriber
	filter  EventFilter
}

// EventSerializer defines the interface for event serialization.
type EventSerializer interface {
	Serialize(event domain.Event) ([]byte, error)
	Deserialize(data []byte, format domain.EventFormat) (domain.Event, error)
}

// JSONSerializer is a basic JSON serializer for events.
type JSONSerializer struct{}

// Serialize converts an event to JSON bytes.
func (s *JSONSerializer) Serialize(event domain.Event) ([]byte, error) {
	return event.ToJSON()
}

// Deserialize converts JSON bytes back to an event.
func (s *JSONSerializer) Deserialize(data []byte, format domain.EventFormat) (domain.Event, error) {
	// Implementation would parse JSON back to event
	// This is a simplification - actual implementation would use json.Unmarshal
	return domain.Event{}, fmt.Errorf("deserialization not implemented for format: %s", format)
}

// NewSimpleEventBus creates a new SimpleEventBus.
func NewSimpleEventBus() *SimpleEventBus {
	return &SimpleEventBus{
		subscribers:     make(map[string]map[Subscriber]bool),
		filteredSubs:    make(map[string]map[*filteredSubscriber]bool),
		stopChan:        make(chan struct{}),
		isStopped:       false,
		transportType:   "memory",
		eventSerializer: &JSONSerializer{},
	}
}

// Publish sends an event to all subscribers of the event's topic.
// Uses non-blocking sends to prevent slow subscribers from blocking the bus.
func (b *SimpleEventBus) Publish(event domain.Event) {
	b.mu.RLock()
	if b.isStopped {
		b.mu.RUnlock()
		log.Printf("EventBus stopped, ignoring publish for topic: %s", event.Topic)
		return
	}

	// Get regular subscribers
	subsMap, found := b.subscribers[event.Topic]

	// Get filtered subscribers
	filteredSubsMap, filteredFound := b.filteredSubs[event.Topic]

	if (!found || len(subsMap) == 0) && (!filteredFound || len(filteredSubsMap) == 0) {
		b.mu.RUnlock()
		log.Printf("No subscribers for event topic '%s'", event.Topic)
		return
	}

	// Create a list of regular subscribers to publish to outside the lock
	subsList := make([]Subscriber, 0, len(subsMap))
	for sub := range subsMap {
		subsList = append(subsList, sub)
	}

	// Create a list of filtered subscribers that match the event
	filteredSubsList := make([]*filteredSubscriber, 0)
	if filteredFound {
		for fsub := range filteredSubsMap {
			if fsub.filter(event) {
				filteredSubsList = append(filteredSubsList, fsub)
			}
		}
	}

	b.mu.RUnlock() // Release lock before sending

	totalSubs := len(subsList) + len(filteredSubsList)
	log.Printf("Publishing event to topic '%s' to %d subscribers (%d regular, %d filtered)",
		event.Topic, totalSubs, len(subsList), len(filteredSubsList))

	// Publish to regular subscribers
	for _, sub := range subsList {
		select {
		case sub <- event:
			// Event sent successfully
		case <-b.stopChan:
			// If bus stopped during publish, abort sending to remaining subs
			log.Printf("EventBus stopping during publish to topic '%s'", event.Topic)
			return
		default:
			// Subscriber channel buffer is full. Drop the event for this subscriber.
			log.Printf("Warning: EventBus subscriber buffer full for topic '%s'. Event dropped.", event.Topic)
		}
	}

	// Publish to filtered subscribers
	for _, fsub := range filteredSubsList {
		select {
		case fsub.channel <- event:
			// Event sent successfully
		case <-b.stopChan:
			// If bus stopped during publish, abort sending to remaining subs
			log.Printf("EventBus stopping during publish to topic '%s'", event.Topic)
			return
		default:
			// Subscriber channel buffer is full. Drop the event for this subscriber.
			log.Printf("Warning: EventBus filtered subscriber buffer full for topic '%s'. Event dropped.", event.Topic)
		}
	}
}

// Subscribe creates a new subscriber channel for a given topic.
// bufferSize determines the capacity of the subscriber channel.
func (b *SimpleEventBus) Subscribe(topic string, bufferSize int) (Subscriber, error) {
	b.mu.Lock() // Use Lock for modifying the map
	defer b.mu.Unlock()

	if b.isStopped {
		return nil, fmt.Errorf("eventbus is stopped")
	}

	if bufferSize <= 0 {
		bufferSize = 10 // Default buffer size
	}

	sub := make(Subscriber, bufferSize)

	// Initialize the map for the topic if it doesn't exist
	if _, found := b.subscribers[topic]; !found {
		b.subscribers[topic] = make(map[Subscriber]bool)
	}

	// Add the subscriber to the map
	b.subscribers[topic][sub] = true
	log.Printf("New subscriber added for topic '%s'. Total subscribers: %d", topic, len(b.subscribers[topic]))

	return sub, nil
}

// SubscribeWithFilter creates a new subscriber channel for a given topic with a filter function.
func (b *SimpleEventBus) SubscribeWithFilter(topic string, filter EventFilter, bufferSize int) (Subscriber, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.isStopped {
		return nil, fmt.Errorf("eventbus is stopped")
	}

	if bufferSize <= 0 {
		bufferSize = 10 // Default buffer size
	}

	if filter == nil {
		// If no filter provided, use regular subscription
		return b.Subscribe(topic, bufferSize)
	}

	sub := make(Subscriber, bufferSize)
	fsub := &filteredSubscriber{
		channel: sub,
		filter:  filter,
	}

	// Initialize the map for the topic if it doesn't exist
	if _, found := b.filteredSubs[topic]; !found {
		b.filteredSubs[topic] = make(map[*filteredSubscriber]bool)
	}

	// Add the filtered subscriber to the map
	b.filteredSubs[topic][fsub] = true
	log.Printf("New filtered subscriber added for topic '%s'. Total filtered subscribers: %d",
		topic, len(b.filteredSubs[topic]))

	return sub, nil
}

// Unsubscribe removes a subscriber from a topic.
func (b *SimpleEventBus) Unsubscribe(topic string, sub Subscriber) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.isStopped {
		return fmt.Errorf("eventbus is stopped")
	}

	// Try to unsubscribe from regular subscribers
	if topicSubs, found := b.subscribers[topic]; found {
		if _, ok := topicSubs[sub]; ok {
			delete(topicSubs, sub)
			log.Printf("Subscriber removed from topic '%s'. Remaining subscribers: %d", topic, len(topicSubs))

			// Clean up empty topic maps
			if len(topicSubs) == 0 {
				delete(b.subscribers, topic)
				log.Printf("No more subscribers for topic '%s', removed topic", topic)
			}

			// Close the subscriber channel
			close(sub)
			return nil
		}
	}

	// Try to unsubscribe from filtered subscribers
	if filteredSubs, found := b.filteredSubs[topic]; found {
		for fsub := range filteredSubs {
			if fsub.channel == sub {
				delete(filteredSubs, fsub)
				log.Printf("Filtered subscriber removed from topic '%s'. Remaining filtered subscribers: %d",
					topic, len(filteredSubs))

				// Clean up empty topic maps
				if len(filteredSubs) == 0 {
					delete(b.filteredSubs, topic)
					log.Printf("No more filtered subscribers for topic '%s', removed topic", topic)
				}

				// Close the subscriber channel
				close(sub)
				return nil
			}
		}
	}

	return fmt.Errorf("subscriber not found for topic '%s'", topic)
}

// Stop signals all goroutines to stop and closes all subscriber channels.
func (b *SimpleEventBus) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.isStopped {
		return // Already stopped
	}

	// Signal stop
	close(b.stopChan)
	b.isStopped = true

	// Close all regular subscriber channels
	for topic, subs := range b.subscribers {
		for sub := range subs {
			close(sub)
		}
		log.Printf("Closed %d subscriber channels for topic '%s'", len(subs), topic)
	}

	// Close all filtered subscriber channels
	for topic, fsubs := range b.filteredSubs {
		for fsub := range fsubs {
			close(fsub.channel)
		}
		log.Printf("Closed %d filtered subscriber channels for topic '%s'", len(fsubs), topic)
	}

	// Clear the subscribers maps
	b.subscribers = make(map[string]map[Subscriber]bool)
	b.filteredSubs = make(map[string]map[*filteredSubscriber]bool)
	log.Println("EventBus stopped and all subscribers closed")
}

// GetTransport returns the current transport mechanism.
func (b *SimpleEventBus) GetTransport() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.transportType
}

// SwitchTransport changes the transport mechanism.
// Note: In a real implementation, this would involve creating new connections,
// migrating subscribers, etc.
func (b *SimpleEventBus) SwitchTransport(transportType string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.isStopped {
		return fmt.Errorf("cannot switch transport: eventbus is stopped")
	}

	supportedTransports := map[string]bool{
		"memory": true,
		"http":   true,
		"grpc":   true,
		"kafka":  true,
	}

	if !supportedTransports[transportType] {
		return fmt.Errorf("unsupported transport type: %s", transportType)
	}

	if b.transportType == transportType {
		return nil // Already using this transport
	}

	log.Printf("Switching eventbus transport from %s to %s", b.transportType, transportType)
	b.transportType = transportType

	// TODO: In a real implementation, this would involve:
	// 1. Creating a new transport connection
	// 2. Migrating subscribers
	// 3. Closing the old transport connection

	return nil
}

// GetSubscriberCount returns the number of subscribers for a given topic.
func (b *SimpleEventBus) GetSubscriberCount(topic string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()

	count := 0
	if topicSubs, found := b.subscribers[topic]; found {
		count += len(topicSubs)
	}
	if filteredSubs, found := b.filteredSubs[topic]; found {
		count += len(filteredSubs)
	}
	return count
}

// HasTopic checks if a topic has any subscribers.
func (b *SimpleEventBus) HasTopic(topic string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()

	_, regularFound := b.subscribers[topic]
	_, filteredFound := b.filteredSubs[topic]
	return regularFound || filteredFound
}

// ListTopics returns a list of all active topics.
func (b *SimpleEventBus) ListTopics() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	topicSet := make(map[string]bool)

	// Add regular subscriber topics
	for topic := range b.subscribers {
		topicSet[topic] = true
	}

	// Add filtered subscriber topics
	for topic := range b.filteredSubs {
		topicSet[topic] = true
	}

	// Convert to slice
	topics := make([]string, 0, len(topicSet))
	for topic := range topicSet {
		topics = append(topics, topic)
	}

	return topics
}
