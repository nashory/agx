package agentstream

import (
	"context"
	"sync"
)

type queuedEvent struct {
	event    Event
	critical bool
}

// EventQueue bounds intermediate stream updates while preserving every
// critical event. Critical bursts may temporarily exceed maxPending rather
// than dropping final answers, errors, or terminal state.
type EventQueue struct {
	ctx    context.Context
	cancel context.CancelFunc
	out    chan Event
	wake   chan struct{}
	done   chan struct{}
	once   sync.Once

	mu         sync.Mutex
	pending    []queuedEvent
	maxPending int
	closed     bool
}

func NewEventQueue(parent context.Context, maxPending int) *EventQueue {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	queue := &EventQueue{
		ctx:        ctx,
		cancel:     cancel,
		out:        make(chan Event),
		wake:       make(chan struct{}, 1),
		done:       make(chan struct{}),
		maxPending: maxPending,
	}
	go queue.run()
	return queue
}

func (q *EventQueue) Events() <-chan Event {
	return q.out
}

func (q *EventQueue) Publish(event Event, critical bool) {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	if q.maxPending > 0 && len(q.pending) >= q.maxPending {
		if !critical {
			q.mu.Unlock()
			return
		}
		drop := -1
		for i, pending := range q.pending {
			if !pending.critical {
				drop = i
				break
			}
		}
		if drop >= 0 {
			copy(q.pending[drop:], q.pending[drop+1:])
			q.pending = q.pending[:len(q.pending)-1]
		}
	}
	q.pending = append(q.pending, queuedEvent{event: event, critical: critical})
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *EventQueue) Close() {
	q.once.Do(func() {
		q.mu.Lock()
		q.closed = true
		q.pending = nil
		q.mu.Unlock()
		q.cancel()
	})
	<-q.done
}

func (q *EventQueue) run() {
	defer close(q.done)
	defer close(q.out)
	defer q.markClosed()
	for {
		event, ok := q.next()
		if ok {
			select {
			case q.out <- event:
			case <-q.ctx.Done():
				return
			}
			continue
		}
		select {
		case <-q.ctx.Done():
			return
		case <-q.wake:
		}
	}
}

func (q *EventQueue) markClosed() {
	q.mu.Lock()
	q.closed = true
	q.pending = nil
	q.mu.Unlock()
}

func (q *EventQueue) next() (Event, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed || len(q.pending) == 0 {
		return Event{}, false
	}
	event := q.pending[0].event
	copy(q.pending, q.pending[1:])
	q.pending = q.pending[:len(q.pending)-1]
	return event, true
}
