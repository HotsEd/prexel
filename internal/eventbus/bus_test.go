package eventbus

import (
	"sync"
	"testing"
	"time"
)

func TestPublishSubscribe_Basic(t *testing.T) {
	b := New(0, 0)
	ch, unsub := b.Subscribe("deploy.123", "")
	defer unsub()

	ev := b.Publish("deploy.123", "started", map[string]string{"app": "x"})
	if ev.ID == "" || ev.Topic != "deploy.123" || ev.Type != "started" {
		t.Errorf("bad event: %+v", ev)
	}

	select {
	case got := <-ch:
		if got.ID != ev.ID {
			t.Errorf("mismatched id: got %q want %q", got.ID, ev.ID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for event")
	}
}

func TestSubscribe_Wildcard(t *testing.T) {
	b := New(0, 0)
	ch, unsub := b.Subscribe("*", "")
	defer unsub()

	b.Publish("deploy.123", "started", nil)
	b.Publish("domain.foo", "verified", nil)

	got := 0
	timeout := time.After(500 * time.Millisecond)
loop:
	for got < 2 {
		select {
		case <-ch:
			got++
		case <-timeout:
			break loop
		}
	}
	if got != 2 {
		t.Errorf("wildcard sub received %d events, want 2", got)
	}
}

func TestSubscribe_PrefixReplay(t *testing.T) {
	// Prefix subscriptions ("deploy.*") match buffered events on replay; live
	// delivery from Publish only fans out to exact-topic subscribers and the
	// wildcard ("*"). Document the replay behaviour here.
	b := New(0, 0)
	b.Publish("deploy.1", "x", nil)
	b.Publish("other.1", "x", nil)
	b.Publish("deploy.2", "x", nil)

	ch, unsub := b.Subscribe("deploy.*", "")
	defer unsub()

	got := 0
	timeout := time.After(300 * time.Millisecond)
loop:
	for got < 2 {
		select {
		case <-ch:
			got++
		case <-timeout:
			break loop
		}
	}
	if got != 2 {
		t.Errorf("deploy.* replay received %d events, want 2", got)
	}
}

func TestReplay_LastEventID(t *testing.T) {
	b := New(0, 0)

	first := b.Publish("topic.x", "a", nil)
	b.Publish("topic.x", "b", nil)
	b.Publish("topic.x", "c", nil)

	// Subscribe with lastEventID = first. We should receive b and c on replay,
	// then nothing further (no new publishes).
	ch, unsub := b.Subscribe("topic.x", first.ID)
	defer unsub()

	got := 0
	timeout := time.After(300 * time.Millisecond)
loop:
	for {
		select {
		case <-ch:
			got++
		case <-timeout:
			break loop
		}
	}
	if got != 2 {
		t.Errorf("replay received %d events, want 2", got)
	}
}

func TestPublish_DropsFullSubscribers(t *testing.T) {
	b := New(0, 0)
	ch, unsub := b.Subscribe("topic.x", "")
	defer unsub()

	// Channel buffer is 64; push more without draining.
	for i := 0; i < 256; i++ {
		b.Publish("topic.x", "x", i)
	}
	// Subscriber should still be alive; full channel just drops.
	select {
	case _, ok := <-ch:
		if !ok {
			t.Error("channel was closed unexpectedly")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("expected at least one buffered event")
	}
}

func TestMultipleSubscribers_AllReceive(t *testing.T) {
	b := New(0, 0)

	var wg sync.WaitGroup
	subs := 5
	wg.Add(subs)
	got := make([]int, subs)
	chs := make([]<-chan Event, subs)
	unsubs := make([]func(), subs)
	for i := 0; i < subs; i++ {
		chs[i], unsubs[i] = b.Subscribe("topic.x", "")
	}
	defer func() {
		for _, u := range unsubs {
			u()
		}
	}()

	for i := 0; i < subs; i++ {
		i := i
		go func() {
			defer wg.Done()
			timeout := time.After(500 * time.Millisecond)
			for got[i] < 3 {
				select {
				case <-chs[i]:
					got[i]++
				case <-timeout:
					return
				}
			}
		}()
	}

	b.Publish("topic.x", "a", nil)
	b.Publish("topic.x", "b", nil)
	b.Publish("topic.x", "c", nil)

	wg.Wait()
	for i, n := range got {
		if n != 3 {
			t.Errorf("subscriber %d got %d events, want 3", i, n)
		}
	}
}

func TestBufferRing_DropsOldest(t *testing.T) {
	b := New(3, 30*time.Second)
	b.Publish("topic.x", "a", nil)
	b.Publish("topic.x", "b", nil)
	b.Publish("topic.x", "c", nil)
	b.Publish("topic.x", "d", nil) // forces eviction

	ch, unsub := b.Subscribe("topic.x", "")
	defer unsub()

	got := 0
	timeout := time.After(300 * time.Millisecond)
loop:
	for {
		select {
		case <-ch:
			got++
		case <-timeout:
			break loop
		}
	}
	if got != 3 {
		t.Errorf("replay returned %d events, want 3 (cap)", got)
	}
}

func TestUnsubscribe(t *testing.T) {
	b := New(0, 0)
	ch, unsub := b.Subscribe("topic.x", "")
	unsub()

	b.Publish("topic.x", "post-unsub", nil)
	select {
	case <-ch:
		// Channel might receive nothing, or might have already-queued; just ensure
		// no panic and no infinite blocking.
	case <-time.After(100 * time.Millisecond):
	}
}

func TestNextID_Monotonic(t *testing.T) {
	b := New(0, 0)
	e1 := b.Publish("t", "x", nil)
	e2 := b.Publish("t", "x", nil)
	if e1.ID >= e2.ID {
		t.Errorf("ids not monotonic: %q vs %q", e1.ID, e2.ID)
	}
}
