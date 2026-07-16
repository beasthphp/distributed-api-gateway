package usage

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

type Store interface {
	PersistUsage(context.Context, []Event) error
	PersistUsageDeadLetters(context.Context, []Event, string, int) error
}

type Recorder interface {
	Enqueue(Event) bool
}

type Observer interface {
	SetUsageQueueDepth(int)
	RecordUsageEnqueued()
	RecordUsageDropped(int)
	RecordUsageRetry()
	RecordUsageBatch(int)
	RecordUsageDeadLettered(int)
	RecordUsageDeadLetterFailure(int)
}

type Config struct {
	QueueCapacity  int
	BatchSize      int
	FlushInterval  time.Duration
	MaxAttempts    int
	RetryBaseDelay time.Duration
}

type Pipeline struct {
	store     Store
	cfg       Config
	observer  Observer
	logger    *slog.Logger
	queue     chan Event
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	closeOnce sync.Once
	queueMu   sync.RWMutex
	closed    bool
}

func NewPipeline(store Store, cfg Config, observer Observer, logger *slog.Logger) (*Pipeline, error) {
	if store == nil {
		return nil, fmt.Errorf("usage store is nil")
	}
	if cfg.QueueCapacity <= 0 {
		return nil, fmt.Errorf("usage queue capacity must be positive")
	}
	if cfg.BatchSize <= 0 || cfg.BatchSize > cfg.QueueCapacity {
		return nil, fmt.Errorf("usage batch size must be positive and no larger than queue capacity")
	}
	if cfg.FlushInterval <= 0 || cfg.MaxAttempts <= 0 || cfg.RetryBaseDelay <= 0 {
		return nil, fmt.Errorf("usage flush interval, attempts, and retry delay must be positive")
	}
	if observer == nil {
		observer = noopObserver{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	pipeline := &Pipeline{
		store:    store,
		cfg:      cfg,
		observer: observer,
		logger:   logger,
		queue:    make(chan Event, cfg.QueueCapacity),
		ctx:      ctx,
		cancel:   cancel,
		done:     make(chan struct{}),
	}
	go pipeline.run()
	return pipeline, nil
}

// Enqueue is deliberately non-blocking. When the bounded queue is full, the
// event is dropped and made visible through metrics instead of extending
// request latency or allowing unbounded memory growth.
func (p *Pipeline) Enqueue(event Event) bool {
	if err := event.Validate(); err != nil {
		p.observer.RecordUsageDropped(1)
		p.logger.Warn("usage event rejected", "error", err)
		return false
	}

	p.queueMu.RLock()
	defer p.queueMu.RUnlock()
	if p.closed {
		p.observer.RecordUsageDropped(1)
		return false
	}
	select {
	case p.queue <- event:
		p.observer.RecordUsageEnqueued()
		p.observer.SetUsageQueueDepth(len(p.queue))
		return true
	default:
		p.observer.RecordUsageDropped(1)
		return false
	}
}

// Close stops admission, drains the queue, and flushes the final partial
// batch. If the caller's deadline expires, in-flight storage is cancelled and
// the unpersisted event count is reported as dropped.
func (p *Pipeline) Close(ctx context.Context) error {
	p.closeOnce.Do(func() {
		p.queueMu.Lock()
		p.closed = true
		close(p.queue)
		p.queueMu.Unlock()
	})

	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		p.cancel()
		<-p.done
		return ctx.Err()
	}
}

func (p *Pipeline) run() {
	defer close(p.done)
	defer p.cancel()

	batch := make([]Event, 0, p.cfg.BatchSize)
	timer := time.NewTimer(p.cfg.FlushInterval)
	defer timer.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		p.persist(batch)
		batch = batch[:0]
	}

	for {
		select {
		case <-p.ctx.Done():
			dropped := len(batch) + len(p.queue)
			if dropped > 0 {
				p.observer.RecordUsageDropped(dropped)
			}
			p.observer.SetUsageQueueDepth(0)
			return
		case event, open := <-p.queue:
			if !open {
				flush()
				p.observer.SetUsageQueueDepth(0)
				return
			}
			p.observer.SetUsageQueueDepth(len(p.queue))
			batch = append(batch, event)
			if len(batch) >= p.cfg.BatchSize {
				flush()
				resetTimer(timer, p.cfg.FlushInterval)
			}
		case <-timer.C:
			flush()
			timer.Reset(p.cfg.FlushInterval)
		}
	}
}

func (p *Pipeline) persist(batch []Event) {
	var lastErr error
	for attempt := 1; attempt <= p.cfg.MaxAttempts; attempt++ {
		if err := p.store.PersistUsage(p.ctx, batch); err == nil {
			p.observer.RecordUsageBatch(len(batch))
			return
		} else {
			lastErr = err
		}
		if attempt == p.cfg.MaxAttempts {
			break
		}
		p.observer.RecordUsageRetry()
		if !wait(p.ctx, retryDelay(p.cfg.RetryBaseDelay, attempt)) {
			p.observer.RecordUsageDropped(len(batch))
			return
		}
	}

	p.logger.Error("usage batch exhausted retries", "events", len(batch), "attempts", p.cfg.MaxAttempts, "error", lastErr)
	if err := p.store.PersistUsageDeadLetters(p.ctx, batch, lastErr.Error(), p.cfg.MaxAttempts); err != nil {
		p.observer.RecordUsageDeadLetterFailure(len(batch))
		p.observer.RecordUsageDropped(len(batch))
		p.logger.Error("persist usage dead letters", "events", len(batch), "error", err)
		return
	}
	p.observer.RecordUsageDeadLettered(len(batch))
}

func retryDelay(base time.Duration, failedAttempt int) time.Duration {
	const maximum = 30 * time.Second
	delay := base
	for i := 1; i < failedAttempt && delay < maximum; i++ {
		if delay > maximum/2 {
			return maximum
		}
		delay *= 2
	}
	if delay > maximum {
		return maximum
	}
	return delay
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func resetTimer(timer *time.Timer, duration time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(duration)
}

type noopObserver struct{}

func (noopObserver) SetUsageQueueDepth(int)           {}
func (noopObserver) RecordUsageEnqueued()             {}
func (noopObserver) RecordUsageDropped(int)           {}
func (noopObserver) RecordUsageRetry()                {}
func (noopObserver) RecordUsageBatch(int)             {}
func (noopObserver) RecordUsageDeadLettered(int)      {}
func (noopObserver) RecordUsageDeadLetterFailure(int) {}
