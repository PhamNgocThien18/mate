package matehost

import "sync"

// forwarder hands things on to send in the order they were put, without
// whoever puts them ever waiting on send. The REPL's messages reach its
// program through one (RunREPL): tea.Program.Send blocks until the event
// loop takes the message, and the event loop is the goroutine that runs
// Update, so anything Update waits on that sends would wait on itself for
// ever (CON-121). The REPL's calls to its host run in order through
// another (opQueue). The queue is unbounded: a few messages come per turn,
// and dropping or reordering them would leave the REPL showing stale state.
type forwarder[T any] struct {
	mu    sync.Mutex
	cond  *sync.Cond
	queue []T
	send  func(T)
	done  chan struct{}
}

// newForwarder starts the goroutine that drains the queue into send; it
// ends when Close is called.
func newForwarder[T any](send func(T)) *forwarder[T] {
	f := &forwarder[T]{send: send, done: make(chan struct{})}
	f.cond = sync.NewCond(&f.mu)
	go f.run()
	return f
}

// Put queues v and returns at once.
func (f *forwarder[T]) Put(v T) {
	f.mu.Lock()
	f.queue = append(f.queue, v)
	f.cond.Signal()
	f.mu.Unlock()
}

// Close stops the goroutine; what is still queued is dropped. A send in
// progress is not waited for: Done says when it has returned.
func (f *forwarder[T]) Close() {
	f.mu.Lock()
	f.send = nil
	f.queue = nil
	f.cond.Signal()
	f.mu.Unlock()
}

// Done is closed once the goroutine has ended.
func (f *forwarder[T]) Done() <-chan struct{} { return f.done }

func (f *forwarder[T]) run() {
	defer close(f.done)
	for {
		f.mu.Lock()
		for len(f.queue) == 0 && f.send != nil {
			f.cond.Wait()
		}
		send := f.send
		if send == nil {
			f.mu.Unlock()
			return
		}
		v := f.queue[0]
		var zero T
		f.queue[0] = zero
		f.queue = f.queue[1:]
		f.mu.Unlock()
		send(v)
	}
}
