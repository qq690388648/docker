package events

import (
	"sync"
	"time"

	"github.com/docker/docker/pkg/pubsub"
	eventtypes "github.com/docker/engine-api/types/events"
)

const (
	eventsLimit = 64
	bufferSize  = 1024
)

// Events is pubsub channel for events generated by the engine.
type Events struct {
	mu     sync.Mutex
	events []eventtypes.Message
	pub    *pubsub.Publisher
}

// New returns new *Events instance
func New() *Events {
	return &Events{
		events: make([]eventtypes.Message, 0, eventsLimit),
		pub:    pubsub.NewPublisher(100*time.Millisecond, bufferSize),
	}
}

// Subscribe adds new listener to events, returns slice of 64 stored
// last events, a channel in which you can expect new events (in form
// of interface{}, so you need type assertion), and a function to call
// to stop the stream of events.
func (e *Events) Subscribe() ([]eventtypes.Message, chan interface{}, func()) {
	e.mu.Lock()
	current := make([]eventtypes.Message, len(e.events))
	copy(current, e.events)
	l := e.pub.Subscribe()
	e.mu.Unlock()

	cancel := func() {
		e.Evict(l)
	}
	return current, l, cancel
}

// SubscribeTopic adds new listener to events, returns slice of 64 stored
// last events, a channel in which you can expect new events (in form
// of interface{}, so you need type assertion).
func (e *Events) SubscribeTopic(since, sinceNano int64, ef *Filter) ([]eventtypes.Message, chan interface{}) {
	e.mu.Lock()
	defer e.mu.Unlock()

	var buffered []eventtypes.Message
	topic := func(m interface{}) bool {
		return ef.Include(m.(eventtypes.Message))
	}

	if since != -1 {
		for i := len(e.events) - 1; i >= 0; i-- {
			ev := e.events[i]
			if ev.Time < since || ((ev.Time == since) && (ev.TimeNano < sinceNano)) {
				break
			}
			if ef.filter.Len() == 0 || topic(ev) {
				buffered = append([]eventtypes.Message{ev}, buffered...)
			}
		}
	}

	var ch chan interface{}
	if ef.filter.Len() > 0 {
		ch = e.pub.SubscribeTopic(topic)
	} else {
		// Subscribe to all events if there are no filters
		ch = e.pub.Subscribe()
	}

	return buffered, ch
}

// Evict evicts listener from pubsub
func (e *Events) Evict(l chan interface{}) {
	e.pub.Evict(l)
}

// Log broadcasts event to listeners. Each listener has 100 millisecond for
// receiving event or it will be skipped.
func (e *Events) Log(action, eventType string, actor eventtypes.Actor) {
	now := time.Now().UTC()
	jm := eventtypes.Message{
		Action:   action,
		Type:     eventType,
		Actor:    actor,
		Time:     now.Unix(),
		TimeNano: now.UnixNano(),
	}

	// fill deprecated fields for container and images
	switch eventType {
	case eventtypes.ContainerEventType:
		jm.ID = actor.ID
		jm.Status = action
		jm.From = actor.Attributes["image"]
	case eventtypes.ImageEventType:
		jm.ID = actor.ID
		jm.Status = action
	}

	e.mu.Lock()
	if len(e.events) == cap(e.events) {
		// discard oldest event
		copy(e.events, e.events[1:])
		e.events[len(e.events)-1] = jm
	} else {
		e.events = append(e.events, jm)
	}
	e.mu.Unlock()
	e.pub.Publish(jm)
}

// SubscribersCount returns number of event listeners
func (e *Events) SubscribersCount() int {
	return e.pub.Len()
}
