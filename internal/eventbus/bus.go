// Package eventbus is an in-memory pub/sub primitive used to fan domain events
// (deploy started/log/success/failed, app status changes, domain DNS verification,
// SSL changes …) out to SSE subscribers.
//
// Design:
//
//   - Topics are strings. Subscribers register on either a concrete topic
//     ("deploy.<id>.log") or the catch-all "*".
//   - Every Publish call assigns a monotonically increasing event id so SSE
//     clients can reconnect with Last-Event-ID and resume from where they left off.
//   - A small in-memory ring buffer retains the last N events / last W seconds
//     for replay. Older events are dropped silently.
//   - Publish is non-blocking. If a subscriber's channel is full, the event
//     is dropped for that subscriber.
//
// The Bus is safe for concurrent use.
package eventbus

import (
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Event is a single published message.
type Event struct {
	ID        string    `json:"id"`
	Topic     string    `json:"topic"`
	Type      string    `json:"type"`
	Payload   any       `json:"payload,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// Bus is the in-memory pub/sub primitive. The zero value is NOT usable — use New.
type Bus struct {
	mu      sync.RWMutex
	subs    map[string]map[*subscription]struct{} // topic -> set of subscriptions
	wildcard map[*subscription]struct{}

	// ring buffer for replay
	bufMu  sync.Mutex
	buf    []Event
	bufCap int

	// retention window — events older than this are evicted on every publish
	retention time.Duration

	seq atomic.Int64
}

type subscription struct {
	ch    chan Event
	topic string
}

// New constructs a Bus with a buffer holding up to bufferCap events for at most
// retention duration. Pass zero for either to use defaults (1000 / 30s).
func New(bufferCap int, retention time.Duration) *Bus {
	if bufferCap <= 0 {
		bufferCap = 1000
	}
	if retention <= 0 {
		retention = 30 * time.Second
	}
	return &Bus{
		subs:      make(map[string]map[*subscription]struct{}),
		wildcard:  make(map[*subscription]struct{}),
		buf:       make([]Event, 0, bufferCap),
		bufCap:    bufferCap,
		retention: retention,
	}
}

// nextID returns a monotonically increasing event id string. The format is
// "<unix-millis>-<seq>" so clients reconnecting with Last-Event-ID can compare
// lexicographically.
func (b *Bus) nextID() string {
	n := b.seq.Add(1)
	return strconv.FormatInt(time.Now().UnixMilli(), 10) + "-" + strconv.FormatInt(n, 10)
}

// Publish emits an event on the given topic. The Type field is set from
// eventType — useful when one topic carries multiple event "kinds" (e.g.
// "deploy.<id>" with types "started"/"log"/"success"). The call is
// non-blocking: full subscriber channels lose the event.
func (b *Bus) Publish(topic, eventType string, payload any) Event {
	ev := Event{
		ID:        b.nextID(),
		Topic:     topic,
		Type:      eventType,
		Payload:   payload,
		Timestamp: time.Now(),
	}
	b.bufferAppend(ev)

	b.mu.RLock()
	subs := b.subs[topic]
	// Copy the sub references so we can release the lock during channel send.
	targets := make([]*subscription, 0, len(subs)+len(b.wildcard))
	for s := range subs {
		targets = append(targets, s)
	}
	for s := range b.wildcard {
		targets = append(targets, s)
	}
	b.mu.RUnlock()

	for _, s := range targets {
		select {
		case s.ch <- ev:
		default:
			// drop
		}
	}
	return ev
}

// Subscribe returns a channel of events for the given topic and an
// unsubscribe function. Pass "*" to receive every event.
//
// If lastEventID is non-empty, events held in the replay buffer that strictly
// follow it (and match the topic filter) are pushed onto the returned channel
// before any new events.
func (b *Bus) Subscribe(topic string, lastEventID string) (<-chan Event, func()) {
	if topic == "" {
		topic = "*"
	}
	sub := &subscription{
		ch:    make(chan Event, 64),
		topic: topic,
	}

	// Pre-load replay events BEFORE wiring up the live subscription so the
	// ordering is preserved (replay events strictly precede live events).
	replay := b.replay(topic, lastEventID)

	b.mu.Lock()
	if topic == "*" {
		b.wildcard[sub] = struct{}{}
	} else {
		set, ok := b.subs[topic]
		if !ok {
			set = make(map[*subscription]struct{})
			b.subs[topic] = set
		}
		set[sub] = struct{}{}
	}
	b.mu.Unlock()

	// Push replay (non-blocking; subscriber should drain quickly).
	go func() {
		for _, ev := range replay {
			select {
			case sub.ch <- ev:
			default:
			}
		}
	}()

	unsub := func() {
		b.mu.Lock()
		if topic == "*" {
			delete(b.wildcard, sub)
		} else if set, ok := b.subs[topic]; ok {
			delete(set, sub)
			if len(set) == 0 {
				delete(b.subs, topic)
			}
		}
		b.mu.Unlock()
		// Drain any pending events to unblock potential senders, then close.
		// We don't close here to avoid races with concurrent Publish; instead
		// readers stop receiving once we drop them from subs.
	}
	return sub.ch, unsub
}

// bufferAppend records ev in the replay buffer and evicts expired or excess entries.
func (b *Bus) bufferAppend(ev Event) {
	b.bufMu.Lock()
	defer b.bufMu.Unlock()
	cutoff := time.Now().Add(-b.retention)
	// drop expired entries from the head
	drop := 0
	for ; drop < len(b.buf); drop++ {
		if b.buf[drop].Timestamp.After(cutoff) {
			break
		}
	}
	if drop > 0 {
		b.buf = b.buf[drop:]
	}
	b.buf = append(b.buf, ev)
	if len(b.buf) > b.bufCap {
		b.buf = b.buf[len(b.buf)-b.bufCap:]
	}
}

// replay returns buffered events matching topic that occurred after lastEventID.
// An empty topic or "*" matches everything; a topic ending in ".*" matches by
// prefix; otherwise exact match.
func (b *Bus) replay(topic, lastEventID string) []Event {
	b.bufMu.Lock()
	defer b.bufMu.Unlock()
	out := make([]Event, 0, len(b.buf))
	for _, ev := range b.buf {
		if lastEventID != "" && ev.ID <= lastEventID {
			continue
		}
		if !topicMatches(topic, ev.Topic) {
			continue
		}
		out = append(out, ev)
	}
	return out
}

// topicMatches reports whether ev belongs to the filter. Supports:
//
//	"*"             — match all
//	"deploy.*"      — match by prefix "deploy."
//	"deploy.<id>.*" — match by prefix "deploy.<id>."
//	exact match     — full equality
func topicMatches(filter, topic string) bool {
	if filter == "" || filter == "*" {
		return true
	}
	if strings.HasSuffix(filter, ".*") {
		prefix := strings.TrimSuffix(filter, "*")
		return strings.HasPrefix(topic, prefix)
	}
	return filter == topic
}
