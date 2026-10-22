package outsystems

import (
	"context"
	"fmt"
	"sync"
	"time"

	logspb "go.opentelemetry.io/proto/otlp/logs/v1"

	"heartbeat/services/db-collector/internal/collectors"
	collectormetadata "heartbeat/services/db-collector/internal/metadata"
)

// Waiting for the OTel Collector.
//
// The chart's Collector keeps the order of the batches across a restart only
// when its logs export queue holds a single request (values.yaml,
// otlp_http/logs): a larger queue sends the batches it was sending when it
// stopped after the rest, and Loki rejects their rows when it already has
// rows of the same stream more than an hour newer.  With a single request,
// the Collector accepts the next batch only once Loki has taken the previous
// one, so an export can take as long as Loki takes, or as long as Loki is
// down.  That is backpressure, not a failure of the logging database, so the
// export does not run under the probe deadline and does not fail the target:
//
//   - A batch is exported in the background, under the executor's own
//     context rather than the probe's, and the OTLP client retries it with a
//     bounded backoff until the Collector accepts it (otlp.Client).  The
//     watermark moves when it is accepted, as before.
//   - While a log type's export is pending, its extraction stops: no new
//     batch is read for it.  The rows were read, the result set closed and
//     the connection returned to the pool before the export started, so no
//     query, transaction or lock is held on SQL Server while it waits.
//   - A call waits for the batch it read until the probe deadline, so a
//     Collector that keeps up is never seen as blocked.  An export that is
//     still pending at the end of the call blocks the log type: the call
//     returns [collectors.ErrBlocked], the target stays up, and
//     heartbeat_outsystems_export_blocked is 1 until the export ends.  Once
//     an export to a Collector is blocked, calls stop waiting for theirs to
//     the same Collector, so the target's other log types still run within
//     the cycle.
//   - An export that ends with an error (a request the Collector rejected
//     for good) fails the next call of its log type, which reads the batch
//     again, as a failed export always has.
//
// The batches held at once, read, being exported or pending, are bounded by
// [Executor.MaxHeldBytes] of row data, so a backlog in every log type while
// the Collector is blocked cannot exhaust the collector's memory.

// DefaultMaxHeldBytes bounds the row data of every batch the executor holds
// at once: eight batches of [DefaultMaxBatchBytes], as many as eight targets
// extracting at once.
const DefaultMaxHeldBytes = 8 * DefaultMaxBatchBytes

// minBatchBytes is the least row data a batch is allowed when the held
// budget is spent, so every log type keeps moving, in smaller batches.
const minBatchBytes = 1 << 20

// orphanGrace is how long the exports of a stopped collector keep trying
// before they are cancelled.  A reload stops a changed collector and starts
// it again at once; its exports carry on.  A removed collector's exports are
// cancelled after the grace, and its rows are read again if it comes back.
const orphanGrace = time.Minute

// export is one batch being delivered in the background.
type export struct {
	logType  string
	endpoint string
	rows     int
	started  time.Time
	// done is closed when the export ends; err is set before.
	done chan struct{}
	err  error
	// blocked reports that the export outlived the call that started it.
	// Guarded by Executor.exportMu.
	blocked bool
}

// exportState is the executor's bookkeeping of background exports.
type exportState struct {
	once   sync.Once
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu sync.Mutex
	// pending holds each log type's export while it runs, and after it
	// failed, until the next call of the log type reports the failure.
	pending map[WatermarkKey]*export
	// held is the row data of the batches held, in [rowBytes].
	held int64
	// collectors holds a context per collector for its exports.
	collectors map[string]*collectorExports
}

// collectorExports scopes the exports of one collector.
type collectorExports struct {
	ctx    context.Context
	cancel context.CancelFunc
	// orphaned cancels the exports orphanGrace after the collector stopped,
	// unless it starts again first.
	orphaned *time.Timer
}

func (s *exportState) init() {
	s.once.Do(func() {
		s.ctx, s.cancel = context.WithCancel(context.Background())
		s.pending = map[WatermarkKey]*export{}
		s.collectors = map[string]*collectorExports{}
	})
}

// collectorContext returns the context of collectorID's exports.  s.mu must
// be held.
func (s *exportState) collectorContext(collectorID string) context.Context {
	c := s.collectors[collectorID]
	if c == nil {
		ctx, cancel := context.WithCancel(s.ctx)
		c = &collectorExports{ctx: ctx, cancel: cancel}
		s.collectors[collectorID] = c
	}
	return c.ctx
}

// adopt keeps collectorID's exports going: the collector started (again).
func (s *exportState) adopt(collectorID string) {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.collectors[collectorID]; c != nil && c.orphaned != nil {
		c.orphaned.Stop()
		c.orphaned = nil
	}
}

// orphan cancels collectorID's exports after orphanGrace, and forgets its
// failed ones, unless the collector starts again first.
func (s *exportState) orphan(collectorID string, grace time.Duration) {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.collectors[collectorID]
	if c == nil {
		return
	}
	if c.orphaned != nil {
		c.orphaned.Stop()
	}
	c.orphaned = time.AfterFunc(grace, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.collectors[collectorID] != c || c.orphaned == nil {
			return
		}
		c.cancel()
		delete(s.collectors, collectorID)
		for key, p := range s.pending {
			if key.Collector == collectorID && isDone(p) {
				delete(s.pending, key)
			}
		}
	})
}

// reserve takes up to want bytes of the held budget max and returns how
// much a batch may read: want when the budget allows, else what is left but
// at least minBatchBytes.
func (s *exportState) reserve(want, max int64) int64 {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	grant := want
	if left := max - s.held; left < grant {
		grant = left
	}
	grant = min(want, maxInt64(grant, minBatchBytes))
	s.held += grant
	return grant
}

// release returns n bytes to the held budget.
func (s *exportState) release(n int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.held -= n
}

// live reports whether collectorID is running: started and not stopped
// since.  The exports of a stopped collector update no metrics, whose series
// were deleted when it stopped.
func (s *exportState) live(collectorID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.collectors[collectorID]
	return c != nil && c.orphaned == nil
}

// heldBytes returns the row data held now.
func (s *exportState) heldBytes() int64 {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.held
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func isDone(e *export) bool {
	select {
	case <-e.done:
		return true
	default:
		return false
	}
}

// startExport sends logs in the background and, once the Collector accepts
// them, counts the rows and saves next as key's watermark.  The batch's held
// bytes are released when the export ends.
func (e *Executor) startExport(item collectormetadata.ScheduledProbe, key WatermarkKey, logs []*logspb.ResourceLogs, batch Batch, rows [][]any, instants []time.Time, next Watermark, location *time.Location) *export {
	s := &e.exports
	s.init()
	endpoint := item.OutSystemsLogs.LogsEndpoint
	pending := &export{logType: key.LogType, endpoint: endpoint, rows: len(rows), started: time.Now(), done: make(chan struct{})}
	s.mu.Lock()
	ctx := s.collectorContext(item.CollectorID)
	s.pending[key] = pending
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		err := e.Exporter.Export(ctx, endpoint, logs)
		metrics := e.Metrics
		if !s.live(item.CollectorID) {
			metrics = nil
		}
		if err != nil {
			metrics.exportFailed(item)
			err = fmt.Errorf("log type %s: %w", key.LogType, err)
		} else {
			now := e.clock()
			metrics.addRows(item, len(rows))
			metrics.observeRows(item, batch, rows, instants)
			metrics.setLogDelay(item, now.Sub(Real(instants[len(instants)-1], location)))
			metrics.setLag(item, now.Sub(Real(next.Instant, location)))
			if saveErr := e.Store.Save(key, next, now); saveErr != nil {
				err = fmt.Errorf("log type %s: rows delivered, but the watermark is only kept in memory: %w", key.LogType, saveErr)
			}
		}
		s.mu.Lock()
		s.held -= batch.Bytes
		blocked := pending.blocked
		pending.err = err
		close(pending.done)
		// A failure stays for the log type's next call to report, unless
		// the collector is gone: it reads the batch again if it returns.
		if c := s.collectors[item.CollectorID]; (err == nil || c == nil || c.orphaned != nil) && s.pending[key] == pending {
			delete(s.pending, key)
		}
		s.mu.Unlock()
		if blocked {
			metrics.setExportBlocked(item, false)
			attrs := []any{"collector", key.Collector, "target", key.Target, "log_type", key.LogType, "rows", len(rows), "waited", time.Since(pending.started).Round(time.Millisecond).String()}
			if err != nil {
				e.logger().Warn("outsystems_logs export stopped waiting for the OTel Collector", append(attrs, "error", err)...)
			} else {
				e.logger().Info("outsystems_logs export accepted by the OTel Collector; extraction resumes", attrs...)
			}
		}
	}()
	return pending
}

// awaitExport waits for key's pending export, if any.  With wait false, or
// while another export to the same endpoint is blocked, it only checks.  It returns nil when no
// export is pending or the pending one was accepted, the export's error when
// it failed, and an [collectors.ErrBlocked] error when it is still running,
// which blocks the log type.
func (e *Executor) awaitExport(ctx context.Context, item collectormetadata.ScheduledProbe, key WatermarkKey, wait bool) error {
	s := &e.exports
	s.init()
	s.mu.Lock()
	pending := s.pending[key]
	if pending == nil {
		s.mu.Unlock()
		return nil
	}
	if wait {
		for other, p := range s.pending {
			if other != key && p.blocked && p.endpoint == pending.endpoint {
				wait = false
				break
			}
		}
	}
	s.mu.Unlock()
	if wait {
		select {
		case <-pending.done:
		case <-ctx.Done():
		}
	}
	s.mu.Lock()
	if isDone(pending) {
		if s.pending[key] == pending {
			delete(s.pending, key)
		}
		s.mu.Unlock()
		return pending.err
	}
	first := !pending.blocked
	pending.blocked = true
	s.mu.Unlock()
	waited := time.Since(pending.started).Round(time.Second)
	if first {
		e.Metrics.setExportBlocked(item, true)
		// Info, not a warning: a busy Collector is normal during a backlog,
		// and the lag alert covers a long wait.
		e.logger().Info("outsystems_logs export is waiting for the OTel Collector; this log type is paused until it is accepted",
			"collector", key.Collector, "target", key.Target, "log_type", key.LogType, "rows", pending.rows, "endpoint", pending.endpoint)
	}
	return fmt.Errorf("log type %s: %d rows waiting for the OTel Collector at %s to accept them, for %s: %w",
		key.LogType, pending.rows, pending.endpoint, waited, collectors.ErrBlocked)
}

// Close stops the background exports: it waits for them until ctx ends, then
// cancels the rest and waits for them to return.  A cancelled export leaves
// its watermark, so its rows are read again after a restart.  Call it after
// every poller has stopped.
func (e *Executor) Close(ctx context.Context) {
	s := &e.exports
	s.init()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		s.cancel()
		<-done
	}
	s.cancel()
}
