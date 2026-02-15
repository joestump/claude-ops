package hub

import (
	"fmt"
	"sync"
	"testing"
)

func TestPublishAndSubscribe(t *testing.T) {
	h := New()
	ch, unsub := h.Subscribe(1)
	defer unsub()

	h.Publish(1, "hello")
	h.Publish(1, "world")

	got := <-ch
	if got != "hello" {
		t.Fatalf("expected hello, got %q", got)
	}
	got = <-ch
	if got != "world" {
		t.Fatalf("expected world, got %q", got)
	}
}

func TestCatchupOnSubscribe(t *testing.T) {
	h := New()

	h.Publish(1, "line1")
	h.Publish(1, "line2")
	h.Publish(1, "line3")

	ch, unsub := h.Subscribe(1)
	defer unsub()

	for _, want := range []string{"line1", "line2", "line3"} {
		got := <-ch
		if got != want {
			t.Fatalf("expected %q, got %q", want, got)
		}
	}
}

func TestCloseSession(t *testing.T) {
	h := New()
	ch, _ := h.Subscribe(1)

	h.Publish(1, "before")
	h.Close(1)

	// Drain buffered line, then channel should be closed.
	<-ch
	_, ok := <-ch
	if ok {
		t.Fatal("expected channel to be closed after session Close")
	}
}

func TestSubscribeAfterClose(t *testing.T) {
	h := New()

	h.Publish(1, "a")
	h.Publish(1, "b")
	h.Close(1)

	ch, _ := h.Subscribe(1)
	var lines []string
	for line := range ch {
		lines = append(lines, line)
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 catchup lines, got %d", len(lines))
	}
}

func TestIsActive(t *testing.T) {
	h := New()

	if h.IsActive(1) {
		t.Fatal("expected inactive for unknown session")
	}

	h.Publish(1, "x")
	if !h.IsActive(1) {
		t.Fatal("expected active after publish")
	}

	h.Close(1)
	if h.IsActive(1) {
		t.Fatal("expected inactive after close")
	}
}

func TestPublishAfterCloseIsNoop(t *testing.T) {
	h := New()
	h.Publish(1, "before")
	h.Close(1)
	h.Publish(1, "after") // should not panic or grow buffer

	h.mu.Lock()
	s := h.sessions[1]
	if len(s.buf) != 1 {
		t.Fatalf("expected 1 buffered line, got %d", len(s.buf))
	}
	h.mu.Unlock()
}

func TestBufferEviction(t *testing.T) {
	h := New()
	for i := 0; i < defaultBufferCap+100; i++ {
		h.Publish(1, "line")
	}

	h.mu.Lock()
	s := h.sessions[1]
	if len(s.buf) != defaultBufferCap {
		t.Fatalf("expected buffer capped at %d, got %d", defaultBufferCap, len(s.buf))
	}
	h.mu.Unlock()
}

func TestBufferEvictionOrdering(t *testing.T) {
	h := New()
	// Write more than buffer capacity to force wrapping.
	total := defaultBufferCap + 50
	for i := 0; i < total; i++ {
		h.Publish(1, fmt.Sprintf("line-%d", i))
	}

	// Subscribe should get the last defaultBufferCap lines in order.
	ch, unsub := h.Subscribe(1)
	defer unsub()

	h.Close(1) // close so we can range over ch

	var got []string
	for line := range ch {
		got = append(got, line)
	}

	if len(got) != defaultBufferCap {
		t.Fatalf("expected %d lines, got %d", defaultBufferCap, len(got))
	}

	// First line should be the oldest surviving: line-50.
	want := fmt.Sprintf("line-%d", total-defaultBufferCap)
	if got[0] != want {
		t.Fatalf("expected first line %q, got %q", want, got[0])
	}

	// Last line should be the most recent.
	want = fmt.Sprintf("line-%d", total-1)
	if got[len(got)-1] != want {
		t.Fatalf("expected last line %q, got %q", want, got[len(got)-1])
	}
}

func TestMultipleSubscribers(t *testing.T) {
	h := New()
	ch1, unsub1 := h.Subscribe(1)
	ch2, unsub2 := h.Subscribe(1)
	defer unsub1()
	defer unsub2()

	h.Publish(1, "msg")

	got1 := <-ch1
	got2 := <-ch2
	if got1 != "msg" || got2 != "msg" {
		t.Fatalf("expected both subscribers to get msg, got %q and %q", got1, got2)
	}
}

func TestConcurrentPublish(t *testing.T) {
	h := New()
	ch, unsub := h.Subscribe(1)
	defer unsub()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.Publish(1, "concurrent")
		}()
	}
	wg.Wait()

	// Drain all messages.
	count := 0
	for count < 100 {
		<-ch
		count++
	}
}

func TestUnsubscribe(t *testing.T) {
	h := New()
	ch, unsub := h.Subscribe(1)
	unsub()

	h.Publish(1, "after-unsub")

	// Channel should not receive anything after unsubscribe.
	select {
	case <-ch:
		t.Fatal("expected no message after unsubscribe")
	default:
	}
}

func TestRemove(t *testing.T) {
	h := New()
	ch, _ := h.Subscribe(1)
	h.Publish(1, "data")

	h.Remove(1)

	// Channel should be closed.
	_, ok := <-ch
	// Drain the buffered "data" first.
	if ok {
		_, ok = <-ch
	}
	if ok {
		t.Fatal("expected channel to be closed after Remove")
	}

	// Session should be gone.
	if h.IsActive(1) {
		t.Fatal("expected session removed")
	}

	// Re-publishing should create a fresh session.
	h.Publish(1, "fresh")
	if !h.IsActive(1) {
		t.Fatal("expected new session to be active")
	}
}

func TestRemoveNonexistent(t *testing.T) {
	h := New()
	h.Remove(999) // should not panic
}

func TestMultipleSessions(t *testing.T) {
	h := New()

	ch1, unsub1 := h.Subscribe(1)
	ch2, unsub2 := h.Subscribe(2)
	defer unsub1()
	defer unsub2()

	h.Publish(1, "session-1")
	h.Publish(2, "session-2")

	if got := <-ch1; got != "session-1" {
		t.Fatalf("session 1: expected session-1, got %q", got)
	}
	if got := <-ch2; got != "session-2" {
		t.Fatalf("session 2: expected session-2, got %q", got)
	}

	// Closing one session shouldn't affect the other.
	h.Close(1)
	h.Publish(2, "still-alive")
	if got := <-ch2; got != "still-alive" {
		t.Fatalf("session 2: expected still-alive, got %q", got)
	}
}
