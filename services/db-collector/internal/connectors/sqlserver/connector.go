// Package sqlserver provides a connection manager for Microsoft SQL Server
// targets used by the db-collector service.
//
// Manager.Open resolves credentials via a [CredentialResolver], builds a DSN,
// and returns a pooled [database/sql.DB] for the target.  A Manager created by
// [NewManager] keeps one bounded connection pool per distinct target, login
// and connection setting, so repeated probes against the same target reuse
// authenticated connections instead of performing a TCP, TLS and SQL login
// handshake on every call.  A pool is verified with a ping only when it is
// first created; idle pools are evicted lazily and [Manager.Close] releases
// all of them.  The caller must invoke the cleanup function returned by Open
// when it has finished with the handle.
//
// Every batch the collector sends must start with [SessionSettings] (see
// [WithSessionSettings]), so a collector query never waits long on a lock and
// is always the preferred deadlock victim.
//
// The default credential resolver, [EnvCredentialResolver], reads credentials
// from environment variables of the form:
//
//	HEARTBEAT_CREDENTIAL_<REF>
//
// where <REF> is the credential reference string with all non-alphanumeric
// characters replaced by underscores and converted to upper-case.  The value
// must be a colon-separated "username:password" pair.
package sqlserver

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/microsoft/go-mssqldb"

	collectormetadata "heartbeat/services/db-collector/internal/metadata"
)

// Credential holds the username and password for a SQL Server connection.
type Credential struct {
	Username string
	Password string
}

// CredentialResolver resolves a named credential reference into a
// [Credential].  Implementations may read from environment variables, secret
// managers, or any other secure store.
type CredentialResolver interface {
	Resolve(context.Context, string) (Credential, error)
}

// EnvCredentialResolver resolves credentials from environment variables.
type EnvCredentialResolver struct{}

// Resolve implements [CredentialResolver].  It normalises ref to upper-snake
// case and looks up the environment variable HEARTBEAT_CREDENTIAL_<REF>.
// The variable must contain a "username:password" pair; an error is returned
// when the variable is absent or malformed.
func (EnvCredentialResolver) Resolve(_ context.Context, ref string) (Credential, error) {
	key := strings.NewReplacer("/", "_", "-", "_", ".", "_").Replace(strings.ToUpper(ref))
	value := os.Getenv("HEARTBEAT_CREDENTIAL_" + key)
	if value == "" {
		return Credential{}, fmt.Errorf("missing credential for ref %s", ref)
	}
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 {
		return Credential{}, fmt.Errorf("credential %s must be username:password", ref)
	}
	return Credential{Username: parts[0], Password: parts[1]}, nil
}

// Connection pool limits applied to every pooled [database/sql.DB].  Probes
// within a target run serially, so two connections per pool leave headroom for
// the occasional overlap without multiplying server-side sessions.
const (
	// defaultMaxOpenConns bounds the open connections per pool.
	defaultMaxOpenConns = 2
	// defaultMaxIdleConns bounds the idle connections kept per pool.
	defaultMaxIdleConns = 2
	// defaultConnMaxLifetime recycles connections periodically so that server
	// fail-overs, DNS changes and certificate rotations are eventually picked
	// up.
	defaultConnMaxLifetime = 10 * time.Minute
	// defaultConnMaxIdleTime closes connections that have not been used
	// recently.
	defaultConnMaxIdleTime = 5 * time.Minute
	// defaultPoolIdleTTL is how long a pool may go unused before Open evicts
	// and closes it.  This reclaims pools for targets removed by config
	// reloads.
	defaultPoolIdleTTL = 15 * time.Minute
	// defaultDriverName is the database/sql driver registered by go-mssqldb.
	defaultDriverName = "sqlserver"
)

// LockTimeout is how long a collector statement waits for a lock before SQL
// Server cancels it with error 1222.  It is far below the default probe
// timeout (half the scrape interval, at most 10s), so a lock wait fails fast
// with a specific error instead of keeping the probe queued behind, and in
// front of, application lock requests until the probe deadline.
const LockTimeout = time.Second

// SessionSettings is the T-SQL prefix of every collector batch.  It caps lock
// waits at [LockTimeout] and makes the collector's session the preferred
// deadlock victim, so a probe never wins a deadlock against the application.
//
// The settings are sent in the same batch as the query instead of through a
// session initialisation statement, which go-mssqldb would run as an extra
// round trip on every pooled connection reuse.  Pooled connections are reset
// before the batch runs, so the settings hold for exactly that batch.  The
// LOCK_TIMEOUT value is [LockTimeout] in milliseconds; a test keeps them in
// sync.
const SessionSettings = "SET LOCK_TIMEOUT 1000; SET DEADLOCK_PRIORITY LOW;\n"

// WithSessionSettings returns query prefixed with [SessionSettings].  Every
// statement the collector sends to a target, including operator
// query_template overrides, must go through it.
func WithSessionSettings(query string) string {
	return SessionSettings + query
}

// ErrManagerClosed is returned by [Manager.Open] after [Manager.Close] has
// been called on a pooled Manager (or any copy of it).
var ErrManagerClosed = errors.New("sqlserver connection manager is closed")

// Manager opens authenticated, TLS-encrypted connections to SQL Server targets.
//
// A Manager returned by [NewManager] owns shared connection-pool state behind
// a pointer, so copies of it share the same pools and closing any copy closes
// them all.  A Manager constructed as a struct literal has no pool and opens a
// fresh connection on every call to [Manager.Open].
type Manager struct {
	// Resolver provides credentials for a given credential reference string.
	Resolver CredentialResolver
	// Application is the client application name sent to the server for
	// observability purposes.
	Application string
	// DialTimeout limits how long the TCP connection and initial handshake may
	// take.  It also bounds the ping performed when a pool is first created.
	DialTimeout time.Duration
	// QueryTimeout is reserved for future use; individual query deadlines are
	// currently managed at the probe level.
	QueryTimeout time.Duration
	// Encrypt controls whether the connection uses TLS encryption.
	Encrypt bool
	// TrustServerCertificate skips hostname and chain verification while keeping
	// TLS transport enabled. This is intended for dev-only self-signed targets.
	TrustServerCertificate bool

	// pool holds the shared per-target connection pools; nil disables pooling.
	pool *pool
	// driverName overrides the database/sql driver; empty means "sqlserver".
	// It exists so tests can substitute a fake driver.
	driverName string
}

// NewManager returns a Manager configured with sensible production defaults:
// 5-second dial/query timeouts, TLS encryption enabled, the application name
// set to "HeartbeatDBCollector", and connection pooling enabled.  Callers
// should invoke [Manager.Close] on shutdown to release pooled connections.
func NewManager(resolver CredentialResolver) Manager {
	return Manager{
		Resolver:     resolver,
		Application:  "HeartbeatDBCollector",
		DialTimeout:  5 * time.Second,
		QueryTimeout: 5 * time.Second,
		Encrypt:      true,
		pool:         newPool(),
	}
}

// Open resolves credentials for target and returns a [database/sql.DB] handle
// for it together with a cleanup function.  On success the caller must always
// invoke cleanup exactly once when it has finished using the handle, and must
// not call Close on the handle itself.  On failure cleanup is nil.  Open is
// safe for concurrent use.
//
// For a Manager created by [NewManager] the handle is a shared connection pool
// keyed by the target address, database, credential reference, a keyed hash
// of the resolved username and password (so a rotated credential gets a fresh
// pool) and every other connection setting.  The pool is created and verified
// with a ping bounded by DialTimeout on first use only; a failed first ping is
// not cached, so the next Open retries.  cleanup releases the caller's
// reference; it does not close the pool.  Pools unused for 15 minutes are
// closed lazily by subsequent Opens.  After [Manager.Close], Open returns
// [ErrManagerClosed].
//
// For a Manager without a pool (a struct literal), Open opens and pings a new
// handle on every call and cleanup closes it.
func (m Manager) Open(ctx context.Context, target collectormetadata.DatabaseTarget) (*sql.DB, func(), error) {
	if m.Resolver == nil {
		return nil, nil, errors.New("sqlserver manager has no credential resolver")
	}
	creds, err := m.Resolver.Resolve(ctx, target.CredentialRef)
	if err != nil {
		return nil, nil, err
	}
	dsn := m.dsn(target, creds)
	if m.pool == nil {
		db, err := m.connect(ctx, target, dsn, false)
		if err != nil {
			return nil, nil, err
		}
		return db, func() { _ = db.Close() }, nil
	}
	key := m.pool.key(m, target, creds)
	return m.pool.acquire(ctx, key, func(ctx context.Context) (*sql.DB, error) {
		return m.connect(ctx, target, dsn, true)
	})
}

// Close closes every pooled connection handle and makes subsequent calls to
// [Manager.Open] on this Manager and all of its copies return
// [ErrManagerClosed].  Handles still held by callers stop accepting new
// queries.  Close returns the joined close errors, is safe to call more than
// once (later calls return nil), and is a no-op for a Manager without a pool.
func (m Manager) Close() error {
	if m.pool == nil {
		return nil
	}
	return m.pool.close()
}

func (m Manager) driver() string {
	if m.driverName != "" {
		return m.driverName
	}
	return defaultDriverName
}

// dsn builds the go-mssqldb connection URL.  The result contains the password
// and must never be logged or included in errors.
func (m Manager) dsn(target collectormetadata.DatabaseTarget, creds Credential) string {
	query := url.Values{}
	query.Set("database", target.DatabaseName)
	query.Set("app name", m.Application)
	query.Set("encrypt", strconv.FormatBool(m.Encrypt))
	query.Set("TrustServerCertificate", strconv.FormatBool(m.TrustServerCertificate))
	query.Set("dial timeout", strconv.Itoa(int(m.DialTimeout.Seconds())))
	return (&url.URL{
		Scheme:   "sqlserver",
		User:     url.UserPassword(creds.Username, creds.Password),
		Host:     net.JoinHostPort(target.Host, strconv.Itoa(target.Port)),
		RawQuery: query.Encode(),
	}).String()
}

// connect opens a handle for dsn and verifies it with a ping bounded by
// DialTimeout.  pooled applies the pool limits.  On failure the handle is
// closed.
func (m Manager) connect(ctx context.Context, target collectormetadata.DatabaseTarget, dsn string, pooled bool) (*sql.DB, error) {
	db, err := sql.Open(m.driver(), dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlserver connection for target %s: %w", target.Name, err)
	}
	if pooled {
		db.SetMaxOpenConns(defaultMaxOpenConns)
		db.SetMaxIdleConns(defaultMaxIdleConns)
		db.SetConnMaxLifetime(defaultConnMaxLifetime)
		db.SetConnMaxIdleTime(defaultConnMaxIdleTime)
	}
	pingCtx := ctx
	if m.DialTimeout > 0 {
		var cancel context.CancelFunc
		pingCtx, cancel = context.WithTimeout(ctx, m.DialTimeout)
		defer cancel()
	}
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlserver target %s: %w", target.Name, err)
	}
	return db, nil
}

// poolKey identifies one pooled handle.  It holds every setting that affects
// the DSN; the credential is represented only by a keyed hash.
type poolKey struct {
	driver                 string
	host                   string
	port                   int
	database               string
	credentialRef          string
	credentialHash         string
	application            string
	encrypt                bool
	trustServerCertificate bool
	dialTimeout            time.Duration
}

// pool is the shared, mutex-guarded set of per-key handles behind a Manager.
type pool struct {
	// secret keys the credential hash so pool keys cannot be used to guess
	// passwords offline.
	secret  [32]byte
	idleTTL time.Duration
	now     func() time.Time

	mu      sync.Mutex
	closed  bool
	entries map[poolKey]*poolEntry
}

// poolEntry is one handle, possibly still being initialised.  ready is closed
// once initialisation finishes; db or err is set before that and never
// changes afterwards.  Failed entries are removed from the pool before ready
// is closed, so an entry found in the pool with ready closed always has db.
type poolEntry struct {
	ready chan struct{}
	db    *sql.DB
	err   error

	// refs and lastUsed are guarded by pool.mu.
	refs     int
	lastUsed time.Time
}

func newPool() *pool {
	p := &pool{
		idleTTL: defaultPoolIdleTTL,
		now:     time.Now,
		entries: make(map[poolKey]*poolEntry),
	}
	if _, err := rand.Read(p.secret[:]); err != nil {
		// crypto/rand.Read does not fail on supported platforms.
		panic(fmt.Sprintf("sqlserver: generate pool key secret: %v", err))
	}
	return p
}

func (p *pool) key(m Manager, target collectormetadata.DatabaseTarget, creds Credential) poolKey {
	mac := hmac.New(sha256.New, p.secret[:])
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(creds.Username)))
	mac.Write(n[:])
	mac.Write([]byte(creds.Username))
	mac.Write([]byte(creds.Password))
	return poolKey{
		driver:                 m.driver(),
		host:                   target.Host,
		port:                   target.Port,
		database:               target.DatabaseName,
		credentialRef:          target.CredentialRef,
		credentialHash:         hex.EncodeToString(mac.Sum(nil)),
		application:            m.Application,
		encrypt:                m.Encrypt,
		trustServerCertificate: m.TrustServerCertificate,
		dialTimeout:            m.DialTimeout,
	}
}

// acquire returns the handle for key, creating it with create if needed.
// create runs without holding p.mu so a slow target does not block Opens for
// other keys; concurrent callers for the same key wait for the single
// in-flight creation instead of starting their own.
func (p *pool) acquire(ctx context.Context, key poolKey, create func(context.Context) (*sql.DB, error)) (*sql.DB, func(), error) {
	for {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return nil, nil, ErrManagerClosed
		}
		stale := p.sweepLocked()
		entry, ok := p.entries[key]
		if !ok {
			entry = &poolEntry{ready: make(chan struct{})}
			p.entries[key] = entry
			p.mu.Unlock()
			closeHandles(stale)
			return p.initialize(ctx, key, entry, create)
		}
		select {
		case <-entry.ready:
			entry.refs++
			entry.lastUsed = p.now()
			p.mu.Unlock()
			closeHandles(stale)
			return entry.db, p.releaser(entry), nil
		default:
		}
		p.mu.Unlock()
		closeHandles(stale)
		select {
		case <-entry.ready:
			if entry.err != nil {
				return nil, nil, entry.err
			}
			// Loop to take a reference under the lock; the entry may have
			// been removed by Close in the meantime.
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
}

func (p *pool) initialize(ctx context.Context, key poolKey, entry *poolEntry, create func(context.Context) (*sql.DB, error)) (*sql.DB, func(), error) {
	db, err := create(ctx)
	p.mu.Lock()
	if err == nil && p.closed {
		_ = db.Close()
		db, err = nil, ErrManagerClosed
	}
	if err != nil {
		if p.entries[key] == entry {
			delete(p.entries, key)
		}
		entry.err = err
		close(entry.ready)
		p.mu.Unlock()
		return nil, nil, err
	}
	entry.db = db
	entry.refs = 1
	entry.lastUsed = p.now()
	close(entry.ready)
	p.mu.Unlock()
	return db, p.releaser(entry), nil
}

// releaser returns an idempotent cleanup that drops one reference to entry.
func (p *pool) releaser(entry *poolEntry) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			entry.refs--
			entry.lastUsed = p.now()
			p.mu.Unlock()
		})
	}
}

// sweepLocked removes initialised entries that have no outstanding references
// and have been idle for at least idleTTL, returning their handles for the
// caller to close after releasing p.mu.
func (p *pool) sweepLocked() []*sql.DB {
	if p.idleTTL <= 0 {
		return nil
	}
	now := p.now()
	var stale []*sql.DB
	for key, entry := range p.entries {
		if entry.db != nil && entry.refs == 0 && now.Sub(entry.lastUsed) >= p.idleTTL {
			delete(p.entries, key)
			stale = append(stale, entry.db)
		}
	}
	return stale
}

func (p *pool) close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	var handles []*sql.DB
	for key, entry := range p.entries {
		// Entries still initialising are closed by their initialiser once it
		// observes p.closed.
		if entry.db != nil {
			handles = append(handles, entry.db)
		}
		delete(p.entries, key)
	}
	p.mu.Unlock()
	var errs []error
	for _, db := range handles {
		if err := db.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func closeHandles(handles []*sql.DB) {
	for _, db := range handles {
		_ = db.Close()
	}
}
