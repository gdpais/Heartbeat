// Package sqlserver provides a connection manager for Microsoft SQL Server
// targets used by the db-collector service.
//
// Manager.Open resolves credentials via a [CredentialResolver], builds a DSN,
// opens a [database/sql.DB], and verifies connectivity with a ping before
// returning the handle to the caller.  The caller must invoke the returned
// cleanup function to close the connection when done.
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
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
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
// See the package documentation for the expected variable naming convention.
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

// Manager opens authenticated, TLS-encrypted connections to SQL Server targets.
type Manager struct {
	// Resolver provides credentials for a given credential reference string.
	Resolver CredentialResolver
	// Application is the client application name sent to the server for
	// observability purposes.
	Application string
	// DialTimeout limits how long the TCP connection and initial handshake may
	// take.
	DialTimeout time.Duration
	// QueryTimeout is reserved for future use; individual query deadlines are
	// currently managed at the probe level.
	QueryTimeout time.Duration
	// Encrypt controls whether the connection uses TLS encryption.
	Encrypt bool
}

// NewManager returns a Manager configured with sensible production defaults:
// 5-second dial/query timeouts, TLS encryption enabled, and the application
// name set to "HeartbeatDBCollector".
func NewManager(resolver CredentialResolver) Manager {
	return Manager{
		Resolver:     resolver,
		Application:  "HeartbeatDBCollector",
		DialTimeout:  5 * time.Second,
		QueryTimeout: 5 * time.Second,
		Encrypt:      true,
	}
}

// Open resolves credentials for target, opens a SQL Server connection, and
// verifies it with a ping bounded by DialTimeout.
//
// On success it returns the live [database/sql.DB] handle and a cleanup
// function that closes it; the caller must always invoke the cleanup function
// even when the returned error is nil.
// On failure the connection is closed internally and cleanup is nil.
func (m Manager) Open(ctx context.Context, target collectormetadata.DatabaseTarget) (*sql.DB, func(), error) {
	creds, err := m.Resolver.Resolve(ctx, target.CredentialRef)
	if err != nil {
		return nil, nil, err
	}
	query := url.Values{}
	query.Set("database", target.DatabaseName)
	query.Set("app name", m.Application)
	query.Set("encrypt", fmt.Sprintf("%t", m.Encrypt))
	query.Set("dial timeout", fmt.Sprintf("%d", int(m.DialTimeout.Seconds())))
	dsn := (&url.URL{
		Scheme:   "sqlserver",
		User:     url.UserPassword(creds.Username, creds.Password),
		Host:     fmt.Sprintf("%s:%d", target.Host, target.Port),
		RawQuery: query.Encode(),
	}).String()
	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("open sqlserver connection: %w", err)
	}
	cleanup := func() { _ = db.Close() }
	pingCtx, cancel := context.WithTimeout(ctx, m.DialTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("ping sqlserver target %s: %w", target.Name, err)
	}
	return db, cleanup, nil
}
