package matehost

import (
	"net"
	"sync"
	"time"
)

// lineStream writes lines to one connection from its own goroutine, so
// whoever puts a line never waits on the reader at the other end. Its queue
// is bounded: a reader that falls behind by more than the queue holds is
// cut off (its connection closed), never waited for. It knows nothing of
// what the lines say, so another socket that pushes JSON lines (mate core,
// CON-212) can use it as it is.
type lineStream struct {
	conn    net.Conn
	timeout time.Duration
	quit    chan struct{}
	stop    sync.Once

	mu sync.Mutex
	// lines is closed by finish; ended is set once nothing more may be put.
	lines   chan []byte
	ended   bool
	drainBy time.Time
}

// newLineStream is a stream to conn holding at most limit lines (at least
// 2) and giving each write timeout. Nothing is written until run.
func newLineStream(conn net.Conn, limit int, timeout time.Duration) *lineStream {
	return &lineStream{conn: conn, timeout: timeout, quit: make(chan struct{}), lines: make(chan []byte, max(limit, 2))}
}

// Put queues line and returns at once. It reports false when the stream
// has ended, or when the queue is full: then the stream ends there and its
// connection is closed.
func (s *lineStream) Put(line []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return false
	}
	select {
	case s.lines <- line:
		return true
	default:
		s.ended = true
		s.abort()
		return false
	}
}

// finish ends the stream after what is queued, which has until grace from
// now to be written; then the connection is closed.
func (s *lineStream) finish(grace time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	s.ended = true
	s.drainBy = time.Now().Add(grace)
	_ = s.conn.SetWriteDeadline(s.drainBy)
	close(s.lines)
}

// abort closes the connection at once, dropping what is queued; a write in
// progress fails.
func (s *lineStream) abort() {
	s.stop.Do(func() {
		close(s.quit)
		_ = s.conn.Close()
	})
}

// run writes the queued lines until the stream ends, then closes the
// connection. It returns once the connection is closed.
func (s *lineStream) run() {
	defer s.abort()
	for {
		select {
		case <-s.quit:
			return
		case line, ok := <-s.lines:
			if !ok {
				return
			}
			s.mu.Lock()
			deadline := time.Now().Add(s.timeout)
			if !s.drainBy.IsZero() && s.drainBy.Before(deadline) {
				deadline = s.drainBy
			}
			_ = s.conn.SetWriteDeadline(deadline)
			s.mu.Unlock()
			if _, err := s.conn.Write(line); err != nil {
				return
			}
		}
	}
}
