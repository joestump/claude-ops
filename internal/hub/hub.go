package hub

import "sync"

const defaultBufferCap = 1000

// session holds the state for a single streaming session.
type session struct {
	buf     []string // circular buffer
	pos     int      // next write position
	count   int      // total lines written (may exceed cap)
	clients map[chan string]struct{}
	done    bool
}

// lines returns the buffered lines in order from oldest to newest.
func (s *session) lines() []string {
	n := len(s.buf)
	if n == 0 || s.pos == 0 {
		// Buffer is empty, partially filled, or pos just wrapped to 0 —
		// in all cases buf[:n] is already in order.
		return s.buf
	}
	// Buffer has wrapped: pos points to the oldest entry.
	out := make([]string, n)
	copy(out, s.buf[s.pos:])
	copy(out[n-s.pos:], s.buf[:s.pos])
	return out
}

// append adds a line to the circular buffer. O(1) regardless of size.
func (s *session) append(line string) {
	if len(s.buf) < cap(s.buf) {
		s.buf = append(s.buf, line)
	} else {
		s.buf[s.pos] = line
	}
	s.pos = (s.pos + 1) % cap(s.buf)
	s.count++
}

// Hub fans out session output lines to multiple SSE subscribers.
// It buffers the last defaultBufferCap lines per session so late-joining
// clients receive catchup output before live streaming.
// Governing: SPEC-0008 REQ-6 — real-time session output streaming via SSE fan-out.
type Hub struct {
	mu       sync.Mutex
	sessions map[int]*session
}

// New creates a Hub ready for use.
func New() *Hub {
	return &Hub{
		sessions: make(map[int]*session),
	}
}

// getOrCreate returns the session for id, creating it if needed.
// Caller must hold h.mu.
func (h *Hub) getOrCreate(id int) *session {
	s, ok := h.sessions[id]
	if !ok {
		s = &session{
			buf:     make([]string, 0, defaultBufferCap),
			clients: make(map[chan string]struct{}),
		}
		h.sessions[id] = s
	}
	return s
}

// Publish sends a line to all current subscribers of the session and
// appends it to the session buffer (up to defaultBufferCap lines).
func (h *Hub) Publish(sessionID int, line string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	s := h.getOrCreate(sessionID)
	if s.done {
		return
	}

	s.append(line)

	// Fan out to all connected clients. Non-blocking send so a slow
	// consumer cannot stall publishing.
	for ch := range s.clients {
		select {
		case ch <- line:
		default:
		}
	}
}

// Subscribe returns a channel that receives future lines for the session
// and an unsubscribe function. If the session has buffered lines, they
// are sent immediately on the returned channel. If the session is already
// done, the buffered lines are sent and the channel is closed.
func (h *Hub) Subscribe(sessionID int) (<-chan string, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()

	s := h.getOrCreate(sessionID)

	// Buffer enough for catchup + some live headroom.
	ch := make(chan string, defaultBufferCap+64)

	// Replay buffered history.
	for _, line := range s.lines() {
		ch <- line
	}

	if s.done {
		close(ch)
		return ch, func() {}
	}

	s.clients[ch] = struct{}{}

	unsubscribe := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(s.clients, ch)
	}

	return ch, unsubscribe
}

// Close marks the session as done and closes all subscriber channels.
// Subsequent Publish calls for this session are no-ops. New subscribers
// will receive the full buffer and a closed channel.
func (h *Hub) Close(sessionID int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	s, ok := h.sessions[sessionID]
	if !ok {
		return
	}

	s.done = true
	for ch := range s.clients {
		close(ch)
	}
	s.clients = nil
}

// Remove deletes a session entirely, freeing its buffer memory.
// Any remaining subscribers are closed first.
func (h *Hub) Remove(sessionID int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	s, ok := h.sessions[sessionID]
	if !ok {
		return
	}

	// Close any remaining subscribers.
	for ch := range s.clients {
		close(ch)
	}
	delete(h.sessions, sessionID)
}

// IsActive returns true if the session exists and has not been closed.
func (h *Hub) IsActive(sessionID int) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	s, ok := h.sessions[sessionID]
	if !ok {
		return false
	}
	return !s.done
}
