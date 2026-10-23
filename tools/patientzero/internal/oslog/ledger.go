package oslog

import (
	"sync"
	"time"
)

// LedgerRetention is how long the Ledger keeps its counts: longer than a
// delivery check may reach back (scenario.MaxDeliveryReach).
const LedgerRetention = 8 * time.Hour

// Ledger counts the log rows PatientZero has committed, per log type and
// Instant, so a scenario can compare what Loki holds with what was written:
// the known answer of its delivery checks. It counts the rows of committed
// transactions only, by the Instant the logging database stores (UTC,
// milliseconds), in one-second buckets that close on the second, as a Loki
// range (from, to] does. Counts older than LedgerRetention are dropped. A
// nil *Ledger counts nothing. It is safe for concurrent use.
type Ledger struct {
	mu      sync.Mutex
	buckets map[string]map[int64]*count
	pruned  time.Time
	// Now is the clock that ages counts out; time.Now when nil.
	Now func() time.Time
}

// count is one second's rows of a log type, and those a commit-delay fault
// held back, by how long.
type count struct {
	rows int64
	held map[time.Duration]int64
}

// Counted is how many rows of one log type were committed in a window, and
// how many of those a commit-delay fault held back longer than asked.
type Counted struct {
	Rows int64 `json:"rows"`
	Held int64 `json:"held"`
}

// bucket is the second whose (second-1, second] holds t.
func bucket(t time.Time) int64 {
	s := t.Unix()
	if t.Nanosecond() > 0 {
		s++
	}
	return s
}

// Add counts rows committed together; heldFor is how long a commit-delay
// fault held them back (0 for none). Rows without a time Instant are not
// counted.
func (l *Ledger) Add(rows []Row, heldFor time.Duration) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.buckets == nil {
		l.buckets = map[string]map[int64]*count{}
	}
	for _, r := range rows {
		at, ok := r.Columns["Instant"].(time.Time)
		if !ok {
			continue
		}
		byType := l.buckets[r.Type]
		if byType == nil {
			byType = map[int64]*count{}
			l.buckets[r.Type] = byType
		}
		b := bucket(Stored(at))
		c := byType[b]
		if c == nil {
			c = &count{}
			byType[b] = c
		}
		c.rows++
		if heldFor > 0 {
			if c.held == nil {
				c.held = map[time.Duration]int64{}
			}
			c.held[heldFor]++
		}
	}
	l.prune()
}

// prune drops the counts past LedgerRetention, once a minute.
func (l *Ledger) prune() {
	now := time.Now()
	if l.Now != nil {
		now = l.Now()
	}
	if now.Sub(l.pruned) < time.Minute {
		return
	}
	l.pruned = now
	oldest := now.Add(-LedgerRetention).Unix()
	for _, byType := range l.buckets {
		for s := range byType {
			if s < oldest {
				delete(byType, s)
			}
		}
	}
}

// Count is what was committed of logType with an Instant in (from, to],
// both cut to the second, and how many of those rows were held back longer
// than heldOver.
func (l *Ledger) Count(logType string, from, to time.Time, heldOver time.Duration) Counted {
	var out Counted
	if l == nil {
		return out
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	lo, hi := from.Unix(), to.Unix()
	for s, c := range l.buckets[logType] {
		if s > lo && s <= hi {
			out.Rows += c.rows
			for d, n := range c.held {
				if d > heldOver {
					out.Held += n
				}
			}
		}
	}
	return out
}

// Stored is t as the logging database stores an Instant: in UTC, cut to the
// millisecond of its datetime2(3) columns. The writer binds times this way,
// so the Ledger and the database agree on which second a row is in.
func Stored(t time.Time) time.Time { return t.UTC().Truncate(time.Millisecond) }
