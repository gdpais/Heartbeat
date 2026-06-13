package config

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Snapshot is an immutable active configuration view plus reload diagnostics.
type Snapshot struct {
	Config        RuntimeConfig
	LoadedAt      time.Time
	LastReloadAt  time.Time
	LastReloadErr string
}

// Manager owns the active runtime config. Reloads are fail-closed: an invalid
// candidate never replaces the current snapshot.
type Manager struct {
	path   string
	active atomic.Value
	mu     sync.Mutex
}

// NewManager loads the initial config and stores it as the active snapshot.
func NewManager(path string) (*Manager, error) {
	cfg, err := LoadRuntimeConfig(path)
	if err != nil {
		return nil, err
	}
	manager := &Manager{path: path}
	now := time.Now().UTC()
	manager.active.Store(Snapshot{Config: cfg, LoadedAt: now, LastReloadAt: now})
	return manager, nil
}

// Snapshot returns the current immutable active snapshot.
func (m *Manager) Snapshot() Snapshot {
	return m.active.Load().(Snapshot)
}

// Reload validates and activates the candidate config from disk. If validation
// fails, the old active snapshot is preserved and returned with LastReloadErr.
func (m *Manager) Reload() (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.Snapshot()
	now := time.Now().UTC()
	next, err := LoadRuntimeConfig(m.path)
	if err != nil {
		current.LastReloadAt = now
		current.LastReloadErr = err.Error()
		m.active.Store(current)
		return current, err
	}
	snapshot := Snapshot{Config: next, LoadedAt: now, LastReloadAt: now}
	m.active.Store(snapshot)
	return snapshot, nil
}

// CollectorDiff describes desired collector changes by stable collector ID.
type CollectorDiff struct {
	Added     []CollectorRuntimeConfig
	Updated   []CollectorRuntimeConfig
	Removed   []string
	Unchanged []string
}

// DiffCollectors compares two configs for one collector kind.
func DiffCollectors(previous, next RuntimeConfig, kind string) CollectorDiff {
	oldByID := collectorsByID(previous.EnabledCollectors(kind))
	newByID := collectorsByID(next.EnabledCollectors(kind))
	diff := CollectorDiff{}
	for id, nextCollector := range newByID {
		oldCollector, exists := oldByID[id]
		switch {
		case !exists:
			diff.Added = append(diff.Added, nextCollector)
		case collectorsEqual(oldCollector, nextCollector):
			diff.Unchanged = append(diff.Unchanged, id)
		default:
			diff.Updated = append(diff.Updated, nextCollector)
		}
	}
	for id := range oldByID {
		if _, exists := newByID[id]; !exists {
			diff.Removed = append(diff.Removed, id)
		}
	}
	sortDiff(&diff)
	return diff
}

// CollectorLifecycle is implemented by runtime collector supervisors that can
// apply desired config transitions.
type CollectorLifecycle interface {
	Start(context.Context, CollectorRuntimeConfig) error
	Update(context.Context, CollectorRuntimeConfig) error
	Drain(context.Context, string) error
	Stop(context.Context, string) error
	Status(string) CollectorStatus
}

// CollectorStatus is a small status value surfaced by lifecycle implementations.
type CollectorStatus struct {
	ID      string
	Phase   string
	Version string
	Error   string
}

// ReconcileCollectors applies add/update/remove actions to a lifecycle.
func ReconcileCollectors(ctx context.Context, lifecycle CollectorLifecycle, diff CollectorDiff) error {
	for _, collector := range diff.Added {
		if err := lifecycle.Start(ctx, collector); err != nil {
			return fmt.Errorf("start collector %s: %w", collector.ID, err)
		}
	}
	for _, collector := range diff.Updated {
		if err := lifecycle.Update(ctx, collector); err != nil {
			return fmt.Errorf("update collector %s: %w", collector.ID, err)
		}
	}
	for _, id := range diff.Removed {
		if err := lifecycle.Drain(ctx, id); err != nil {
			return fmt.Errorf("drain collector %s: %w", id, err)
		}
		if err := lifecycle.Stop(ctx, id); err != nil {
			return fmt.Errorf("stop collector %s: %w", id, err)
		}
	}
	return nil
}

func collectorsByID(collectors []CollectorRuntimeConfig) map[string]CollectorRuntimeConfig {
	out := make(map[string]CollectorRuntimeConfig, len(collectors))
	for _, collector := range collectors {
		out[collector.ID] = collector
	}
	return out
}

func collectorsEqual(a, b CollectorRuntimeConfig) bool {
	return fmt.Sprintf("%#v", a) == fmt.Sprintf("%#v", b)
}

func sortDiff(diff *CollectorDiff) {
	sortCollectors(diff.Added)
	sortCollectors(diff.Updated)
	sortStrings(diff.Removed)
	sortStrings(diff.Unchanged)
}

func sortCollectors(values []CollectorRuntimeConfig) {
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
}

func sortStrings(values []string) {
	sort.Strings(values)
}
