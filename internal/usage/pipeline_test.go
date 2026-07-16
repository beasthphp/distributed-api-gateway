package usage

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type fakeStore struct {
	mu              sync.Mutex
	attempts        int
	failAttempts    int
	persisted       []Event
	deadLettered    []Event
	deadLetterError error
	block           <-chan struct{}
	started         chan struct{}
	startOnce       sync.Once
}

func (s *fakeStore) PersistUsage(ctx context.Context, events []Event) error {
	s.mu.Lock()
	s.attempts++
	attempt := s.attempts
	s.mu.Unlock()
	s.startOnce.Do(func() {
		if s.started != nil {
			close(s.started)
		}
	})
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if attempt <= s.failAttempts {
		return errors.New("temporary database error")
	}
	s.mu.Lock()
	s.persisted = append(s.persisted, events...)
	s.mu.Unlock()
	return nil
}

func (s *fakeStore) PersistUsageDeadLetters(_ context.Context, events []Event, _ string, _ int) error {
	if s.deadLetterError != nil {
		return s.deadLetterError
	}
	s.mu.Lock()
	s.deadLettered = append(s.deadLettered, events...)
	s.mu.Unlock()
	return nil
}

type fakeObserver struct {
	mu                sync.Mutex
	depth             int
	enqueued          int
	dropped           int
	retries           int
	batches           int
	persisted         int
	deadLettered      int
	deadLetterFailure int
}

func (o *fakeObserver) SetUsageQueueDepth(value int) {
	o.mu.Lock()
	o.depth = value
	o.mu.Unlock()
}
func (o *fakeObserver) RecordUsageEnqueued() {
	o.mu.Lock()
	o.enqueued++
	o.mu.Unlock()
}
func (o *fakeObserver) RecordUsageDropped(value int) {
	o.mu.Lock()
	o.dropped += value
	o.mu.Unlock()
}
func (o *fakeObserver) RecordUsageRetry() {
	o.mu.Lock()
	o.retries++
	o.mu.Unlock()
}
func (o *fakeObserver) RecordUsageBatch(value int) {
	o.mu.Lock()
	o.batches++
	o.persisted += value
	o.mu.Unlock()
}
func (o *fakeObserver) RecordUsageDeadLettered(value int) {
	o.mu.Lock()
	o.deadLettered += value
	o.mu.Unlock()
}
func (o *fakeObserver) RecordUsageDeadLetterFailure(value int) {
	o.mu.Lock()
	o.deadLetterFailure += value
	o.mu.Unlock()
}

func TestPipelineRetriesAndDrainsOnClose(t *testing.T) {
	store := &fakeStore{failAttempts: 2}
	observer := &fakeObserver{}
	pipeline := newTestPipeline(t, store, observer, Config{
		QueueCapacity: 4, BatchSize: 2, FlushInterval: time.Hour,
		MaxAttempts: 3, RetryBaseDelay: time.Millisecond,
	})
	if !pipeline.Enqueue(testEvent()) || !pipeline.Enqueue(testEvent()) {
		t.Fatal("Enqueue() rejected an event")
	}
	closePipeline(t, pipeline)

	store.mu.Lock()
	attempts, persisted := store.attempts, len(store.persisted)
	store.mu.Unlock()
	if attempts != 3 || persisted != 2 {
		t.Fatalf("attempts/persisted = %d/%d, want 3/2", attempts, persisted)
	}
	if observer.retries != 2 || observer.batches != 1 || observer.persisted != 2 {
		t.Fatalf("observer retries/batches/events = %d/%d/%d, want 2/1/2", observer.retries, observer.batches, observer.persisted)
	}
}

func TestPipelineDeadLettersAfterRetryExhaustion(t *testing.T) {
	store := &fakeStore{failAttempts: 10}
	observer := &fakeObserver{}
	pipeline := newTestPipeline(t, store, observer, Config{
		QueueCapacity: 2, BatchSize: 2, FlushInterval: time.Hour,
		MaxAttempts: 2, RetryBaseDelay: time.Millisecond,
	})
	pipeline.Enqueue(testEvent())
	pipeline.Enqueue(testEvent())
	closePipeline(t, pipeline)

	store.mu.Lock()
	deadLetters := len(store.deadLettered)
	store.mu.Unlock()
	if deadLetters != 2 || observer.deadLettered != 2 || observer.retries != 1 {
		t.Fatalf("dead letters/retries = %d/%d/%d, want 2/2/1", deadLetters, observer.deadLettered, observer.retries)
	}
}

func TestPipelineCloseFlushesPartialBatch(t *testing.T) {
	store := &fakeStore{}
	pipeline := newTestPipeline(t, store, &fakeObserver{}, Config{
		QueueCapacity: 4, BatchSize: 4, FlushInterval: time.Hour,
		MaxAttempts: 1, RetryBaseDelay: time.Millisecond,
	})
	if !pipeline.Enqueue(testEvent()) {
		t.Fatal("event was rejected")
	}
	closePipeline(t, pipeline)

	store.mu.Lock()
	persisted := len(store.persisted)
	store.mu.Unlock()
	if persisted != 1 {
		t.Fatalf("persisted = %d, want final partial batch of 1", persisted)
	}
}

func TestPipelineBackpressureIsNonBlockingAndMeasured(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	store := &fakeStore{block: release, started: started}
	observer := &fakeObserver{}
	pipeline := newTestPipeline(t, store, observer, Config{
		QueueCapacity: 1, BatchSize: 1, FlushInterval: time.Hour,
		MaxAttempts: 1, RetryBaseDelay: time.Millisecond,
	})
	if !pipeline.Enqueue(testEvent()) {
		t.Fatal("first event was rejected")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not begin persistence")
	}
	if !pipeline.Enqueue(testEvent()) {
		t.Fatal("second event did not fill the queue")
	}
	startedAt := time.Now()
	if pipeline.Enqueue(testEvent()) {
		t.Fatal("third event was accepted despite a full queue")
	}
	if elapsed := time.Since(startedAt); elapsed > 50*time.Millisecond {
		t.Fatalf("full-queue Enqueue() blocked for %v", elapsed)
	}
	if observer.dropped != 1 {
		t.Fatalf("dropped = %d, want 1", observer.dropped)
	}
	close(release)
	closePipeline(t, pipeline)
}

func TestPipelineShutdownDeadlineCancelsStorage(t *testing.T) {
	store := &fakeStore{block: make(chan struct{}), started: make(chan struct{}), deadLetterError: context.Canceled}
	observer := &fakeObserver{}
	pipeline := newTestPipeline(t, store, observer, Config{
		QueueCapacity: 1, BatchSize: 1, FlushInterval: time.Hour,
		MaxAttempts: 1, RetryBaseDelay: time.Millisecond,
	})
	pipeline.Enqueue(testEvent())
	<-store.started
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := pipeline.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close() error = %v, want deadline exceeded", err)
	}
	if observer.dropped != 1 || observer.deadLetterFailure != 1 {
		t.Fatalf("dropped/dead-letter failures = %d/%d, want 1/1", observer.dropped, observer.deadLetterFailure)
	}
}

func newTestPipeline(t *testing.T, store Store, observer Observer, cfg Config) *Pipeline {
	t.Helper()
	pipeline, err := NewPipeline(store, cfg, observer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewPipeline() error = %v", err)
	}
	return pipeline
}

func closePipeline(t *testing.T, pipeline *Pipeline) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := pipeline.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func testEvent() Event {
	id, _ := NewEventID()
	return Event{
		ID: id, RequestID: "request-1",
		APIKeyID: "11111111-1111-4111-8111-111111111111",
		ClientID: "22222222-2222-4222-8222-222222222222",
		Route:    "/api/users", Method: "GET", StatusCode: 200,
		DurationMicros: 1500, ResponseBytes: 128, OccurredAt: time.Now().UTC(),
	}
}
