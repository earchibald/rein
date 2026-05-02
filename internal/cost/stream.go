package cost

import "sync"

type Stream struct {
	mu          sync.Mutex
	nextID      int
	subscribers map[int]chan Event
}

func NewStream() *Stream {
	return &Stream{subscribers: map[int]chan Event{}}
}

func (s *Stream) Publish(event Event) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ch := range s.subscribers {
		ch <- event
	}
}

func (s *Stream) Subscribe(buffer int) (events <-chan Event, unsubscribe func()) {
	if buffer < 0 {
		buffer = 0
	}
	ch := make(chan Event, buffer)
	if s == nil {
		close(ch)
		return ch, func() {}
	}
	s.mu.Lock()
	id := s.nextID
	s.nextID++
	s.subscribers[id] = ch
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if current, ok := s.subscribers[id]; ok {
			delete(s.subscribers, id)
			close(current)
		}
	}
}
