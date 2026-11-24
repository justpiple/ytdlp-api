package job

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/justpiple/ytdlp-api/internal/model"
	"github.com/justpiple/ytdlp-api/internal/storage"
)

// ErrJobNotFound is returned when the requested async job ID does not exist.
var ErrJobNotFound = errors.New("job: not found")

type jobRecord struct {
	event       model.ProgressEvent
	subscribers map[chan model.ProgressEvent]struct{}
	isTerminal  bool
	createdAt   time.Time
}

// Manager limits concurrent yt-dlp executions and coordinates async jobs & SSE streams.
type Manager struct {
	sem chan struct{}

	mu   sync.RWMutex
	jobs map[string]*jobRecord
}

// NewManager creates a job Manager with a fixed concurrency limit.
func NewManager(maxConcurrent int) *Manager {
	if maxConcurrent <= 0 {
		maxConcurrent = 4
	}
	return &Manager{
		sem:  make(chan struct{}, maxConcurrent),
		jobs: make(map[string]*jobRecord),
	}
}

// Acquire reserves a concurrency slot or returns ctx.Err() if cancelled while waiting.
func (m *Manager) Acquire(ctx context.Context) error {
	select {
	case m.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Release frees a concurrency slot previously acquired via Acquire.
func (m *Manager) Release() {
	select {
	case <-m.sem:
	default:
	}
}

// ActiveCount returns the number of currently executing download jobs.
func (m *Manager) ActiveCount() int {
	return len(m.sem)
}

// MaxConcurrent returns the configured maximum concurrent download slots.
func (m *Manager) MaxConcurrent() int {
	return cap(m.sem)
}

// Create initializes a new async job in queued state.
func (m *Manager) Create() string {
	id := storage.NewID()
	now := time.Now().UTC()

	m.mu.Lock()
	m.jobs[id] = &jobRecord{
		event: model.ProgressEvent{
			JobID:     id,
			Status:    model.JobStatusQueued.String(),
			Message:   "queued",
			UpdatedAt: now,
		},
		subscribers: make(map[chan model.ProgressEvent]struct{}),
		createdAt:   now,
	}
	m.mu.Unlock()

	return id
}

// Get returns the latest ProgressEvent snapshot for a job.
func (m *Manager) Get(jobID string) (model.ProgressEvent, error) {
	m.mu.RLock()
	rec, ok := m.jobs[jobID]
	if !ok {
		m.mu.RUnlock()
		return model.ProgressEvent{}, ErrJobNotFound
	}
	ev := rec.event
	m.mu.RUnlock()

	return ev, nil
}

// Update publishes a new progress event to all SSE subscribers.
func (m *Manager) Update(jobID string, ev model.ProgressEvent) {
	ev.JobID = jobID
	ev.UpdatedAt = time.Now().UTC()

	terminal := ev.Status == model.JobStatusCompleted.String() ||
		ev.Status == model.JobStatusFailed.String()

	m.mu.Lock()
	rec, ok := m.jobs[jobID]
	if !ok {
		m.mu.Unlock()
		return
	}
	rec.event = ev
	rec.isTerminal = terminal

	for ch := range rec.subscribers {
		if terminal {
			// Ensure the final completed/failed event is never dropped for a slow consumer.
			select {
			case ch <- ev:
			default:
				select {
				case <-ch:
				default:
				}
				ch <- ev
			}
			close(ch)
			delete(rec.subscribers, ch)
			continue
		}

		select {
		case ch <- ev:
		default:
			// Drop intermediate frame if slow consumer buffer is full.
		}
	}
	m.mu.Unlock()
}

// Subscribe returns a channel receiving live job events and an unsubscribe cleanup function.
// Immediately sends the current job state as the initial event.
func (m *Manager) Subscribe(jobID string) (<-chan model.ProgressEvent, func(), error) {
	m.mu.Lock()
	rec, ok := m.jobs[jobID]
	if !ok {
		m.mu.Unlock()
		return nil, nil, ErrJobNotFound
	}

	ch := make(chan model.ProgressEvent, 16)
	current := rec.event
	ch <- current

	if rec.isTerminal {
		close(ch)
		m.mu.Unlock()
		return ch, func() {}, nil
	}

	rec.subscribers[ch] = struct{}{}
	m.mu.Unlock()

	unsubscribe := func() {
		m.mu.Lock()
		if r, exists := m.jobs[jobID]; exists {
			if _, active := r.subscribers[ch]; active {
				delete(r.subscribers, ch)
				close(ch)
			}
		}
		m.mu.Unlock()
	}

	return ch, unsubscribe, nil
}

// StartCleanupWorker periodically prunes completed/failed job metadata older than ttl.
func (m *Manager) StartCleanupWorker(ctx context.Context, interval, ttl time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.pruneExpired(ttl)
		}
	}
}

func (m *Manager) pruneExpired(ttl time.Duration) {
	cutoff := time.Now().UTC().Add(-ttl)

	m.mu.Lock()
	for id, rec := range m.jobs {
		if rec.isTerminal && rec.event.UpdatedAt.Before(cutoff) {
			delete(m.jobs, id)
		}
	}
	m.mu.Unlock()
}
