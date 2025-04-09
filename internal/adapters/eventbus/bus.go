package eventbus

import (
	"fmt" // Added for error formatting
	"log"
	"sync"

	"github.com/ZanzyTHEbar/dragonscale/internal/domain"
)

// Subscriber is a channel that receives events for a specific topic.
// Use a buffered channel to avoid blocking the publisher.
type Subscriber chan domain.Event

// EventBus defines the interface for publishing and subscribing to events.
type EventBus interface {
	Publish(event domain.Event)
	Subscribe(topic string, bufferSize int) (Subscriber, error)
	Unsubscribe(topic string, sub Subscriber) error
	Stop()
}

// SimpleEventBus is a basic in-memory event bus implementation using channels.
type SimpleEventBus struct {
	subscribers map[string]map[Subscriber]bool // Map topic to a Set of subscriber channels for faster unsubscribe
	mu          sync.RWMutex                   // Protects subscribers map
	stopChan    chan struct{}                  // To signal shutdown
	isStopped   bool                           // Atomic bool could also be used
	// wg          sync.WaitGroup               // To wait for publisher goroutine (if any)
}

// NewSimpleEventBus creates a new SimpleEventBus.
func NewSimpleEventBus() *SimpleEventBus {
	bus := &SimpleEventBus{
		subscribers: make(map[string]map[Subscriber]bool),
		stopChan:    make(chan struct{}),
		isStopped:   false,
	}
	return bus
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

	subsMap, found := b.subscribers[event.Topic]
	if !found || len(subsMap) == 0 {
		b.mu.RUnlock()
		log.Printf("No subscribers for event topic '%s'", event.Topic)
		return
	}

	// Create a list of subscribers to publish to outside the lock
	subsList := make([]Subscriber, 0, len(subsMap))
	for sub := range subsMap {
		subsList = append(subsList, sub)
	}
	b.mu.RUnlock() // Release lock before sending

	log.Printf("Publishing event to topic '%s' to %d subscribers", event.Topic, len(subsList))
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
			// Consider adding metrics or specific handling for dropped events
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

	b.subscribers[topic][sub] = true // Add subscriber to the set

	// log.Printf("New subscriber added for topic '%s'. Total subscribers for topic: %d", topic, len(b.subscribers[topic]))
	return sub, nil
}

// Unsubscribe removes a subscriber channel from a topic.
func (b *SimpleEventBus) Unsubscribe(topic string, sub Subscriber) error {
	b.mu.Lock() // Use Lock for modifying the map
	defer b.mu.Unlock()

	// No operation if stopped, but allow unsubscribe to clean up
	// if b.isStopped { return fmt.Errorf("eventbus is stopped") }

	if subsMap, found := b.subscribers[topic]; found {
		if _, subExists := subsMap[sub]; subExists {
			delete(subsMap, sub) // Remove subscriber from set
			// If the set for the topic is now empty, remove the topic itself
			if len(subsMap) == 0 {
				delete(b.subscribers, topic)
				// log.Printf("Last subscriber removed for topic '%s'. Topic deleted.", topic)
			} else {
				// log.Printf("Subscriber removed from topic '%s'. Remaining subscribers: %d", topic, len(subsMap))
			}
			// It's the subscriber's responsibility to close their channel.
			return nil
		} else {
			// log.Printf("Attempted to unsubscribe non-existent subscriber from topic '%s'", topic)
			return fmt.Errorf("subscriber not found for topic %s", topic)
		}
	} else {
		// log.Printf("Attempted to unsubscribe from non-existent topic '%s'", topic)
		return fmt.Errorf("topic %s not found", topic)
	}
}

// Stop signals the event bus to stop publishing and cleans up resources.
func (b *SimpleEventBus) Stop() {
	b.mu.Lock()
	if b.isStopped {
		b.mu.Unlock()
		return
	}
	close(b.stopChan) // Signal stop
	b.isStopped = true
	// Clear subscribers map to prevent further publishes and help GC
	b.subscribers = make(map[string]map[Subscriber]bool)
	b.mu.Unlock()

	// Wait for any background goroutines (e.g., publisher) if implemented
	// b.wg.Wait()
	log.Println("SimpleEventBus stopped.")
}

/*
// Example Usage (Move to main.go or tests)
func ExampleUsage() {
	bus := NewSimpleEventBus()

	subCompleted, _ := bus.Subscribe("task.completed", 10)
	subFailed, _ := bus.Subscribe("task.failed", 10)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for event := range subCompleted {
			log.Printf("Handler 1 Received Completed: %+v
", event)
            // Simulate work
            time.Sleep(50 * time.Millisecond)
		}
		log.Println("Handler 1 (Completed) exiting.")
	}()
	go func() {
		defer wg.Done()
		for event := range subFailed {
			log.Printf("Handler 2 Received Failed: %+v
", event)
		}
		log.Println("Handler 2 (Failed) exiting.")
	}()

	bus.Publish(domain.NewEvent("task.completed", map[string]string{"taskId": "123", "result": "ok"}))
	bus.Publish(domain.NewEvent("task.failed", map[string]string{"taskId": "456", "error": "timeout"}))
	bus.Publish(domain.NewEvent("task.completed", map[string]string{"taskId": "789", "result": "done"}))

	time.Sleep(100 * time.Millisecond) // Allow time for processing

	bus.Unsubscribe("task.completed", subCompleted)
	// Owner closes the channel
	close(subCompleted)

	bus.Publish(domain.NewEvent("task.completed", map[string]string{"taskId": "abc", "result": "final"})) // Not received by handler 1

	time.Sleep(100 * time.Millisecond)
	bus.Stop() // Stop the bus

    // Close remaining subscriber channel after stopping the bus
    // This ensures the range loop in the handler exits.
	bus.Unsubscribe("task.failed", subFailed) // Optional: Unsubscribe before close
    close(subFailed)

    wg.Wait() // Wait for handlers to finish processing and exit
	log.Println("Example usage finished.")
}
*/
