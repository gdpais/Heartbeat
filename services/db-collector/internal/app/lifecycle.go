package app

import (
	"context"
	"sync"

	heartbeatconfig "heartbeat/internal/config"
	"heartbeat/services/db-collector/internal/collectors"
)

type pollerEntry struct {
	collector heartbeatconfig.CollectorRuntimeConfig
	cancel    context.CancelFunc
	done      chan struct{}
	status    heartbeatconfig.CollectorStatus
}

type pollerLifecycle struct {
	runner collectors.Runner
	mu     sync.Mutex
	items  map[string]*pollerEntry
	errCh  chan error
}

func newPollerLifecycle(runner collectors.Runner) *pollerLifecycle {
	return &pollerLifecycle{
		runner: runner,
		items:  map[string]*pollerEntry{},
		errCh:  make(chan error, 1),
	}
}

func (l *pollerLifecycle) Start(ctx context.Context, collector heartbeatconfig.CollectorRuntimeConfig) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, exists := l.items[collector.ID]; exists {
		return l.updateLocked(ctx, collector)
	}
	pollerCtx, cancel := context.WithCancel(ctx)
	entry := &pollerEntry{
		collector: collector,
		cancel:    cancel,
		done:      make(chan struct{}),
		status: heartbeatconfig.CollectorStatus{
			ID:      collector.ID,
			Phase:   "running",
			Version: collector.ID,
		},
	}
	l.items[collector.ID] = entry
	go func() {
		defer close(entry.done)
		err := (collectors.Poller{Runner: l.runner, Collector: collector}).Start(pollerCtx)
		if err != nil && pollerCtx.Err() == nil {
			l.mu.Lock()
			entry.status.Phase = "failed"
			entry.status.Error = err.Error()
			l.mu.Unlock()
			select {
			case l.errCh <- err:
			default:
			}
		}
	}()
	return nil
}

func (l *pollerLifecycle) Update(ctx context.Context, collector heartbeatconfig.CollectorRuntimeConfig) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.updateLocked(ctx, collector)
}

func (l *pollerLifecycle) updateLocked(ctx context.Context, collector heartbeatconfig.CollectorRuntimeConfig) error {
	if entry, exists := l.items[collector.ID]; exists {
		entry.status.Phase = "restarting"
		entry.cancel()
		l.mu.Unlock()
		<-entry.done
		l.mu.Lock()
		delete(l.items, collector.ID)
	}
	pollerCtx, cancel := context.WithCancel(ctx)
	entry := &pollerEntry{
		collector: collector,
		cancel:    cancel,
		done:      make(chan struct{}),
		status: heartbeatconfig.CollectorStatus{
			ID:      collector.ID,
			Phase:   "running",
			Version: collector.ID,
		},
	}
	l.items[collector.ID] = entry
	go func() {
		defer close(entry.done)
		err := (collectors.Poller{Runner: l.runner, Collector: collector}).Start(pollerCtx)
		if err != nil && pollerCtx.Err() == nil {
			l.mu.Lock()
			entry.status.Phase = "failed"
			entry.status.Error = err.Error()
			l.mu.Unlock()
			select {
			case l.errCh <- err:
			default:
			}
		}
	}()
	return nil
}

func (l *pollerLifecycle) Drain(_ context.Context, id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if entry, exists := l.items[id]; exists {
		entry.status.Phase = "draining"
	}
	return nil
}

func (l *pollerLifecycle) Stop(_ context.Context, id string) error {
	l.mu.Lock()
	entry, exists := l.items[id]
	if !exists {
		l.mu.Unlock()
		return nil
	}
	entry.status.Phase = "stopping"
	entry.cancel()
	l.mu.Unlock()
	<-entry.done
	l.mu.Lock()
	delete(l.items, id)
	l.mu.Unlock()
	return nil
}

func (l *pollerLifecycle) Status(id string) heartbeatconfig.CollectorStatus {
	l.mu.Lock()
	defer l.mu.Unlock()
	if entry, exists := l.items[id]; exists {
		return entry.status
	}
	return heartbeatconfig.CollectorStatus{ID: id, Phase: "stopped"}
}

func (l *pollerLifecycle) statuses() []heartbeatconfig.CollectorStatus {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]heartbeatconfig.CollectorStatus, 0, len(l.items))
	for _, entry := range l.items {
		out = append(out, entry.status)
	}
	return out
}

func (l *pollerLifecycle) wait(ctx context.Context) error {
	select {
	case err := <-l.errCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *pollerLifecycle) shutdown(ctx context.Context) error {
	l.mu.Lock()
	ids := make([]string, 0, len(l.items))
	for id := range l.items {
		ids = append(ids, id)
	}
	l.mu.Unlock()
	for _, id := range ids {
		if err := l.Stop(ctx, id); err != nil {
			return err
		}
	}
	return nil
}
