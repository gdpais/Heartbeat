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

type Credential struct {
	Username string
	Password string
}

type CredentialResolver interface {
	Resolve(context.Context, string) (Credential, error)
}

type EnvCredentialResolver struct{}

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

type Manager struct {
	Resolver     CredentialResolver
	Application  string
	DialTimeout  time.Duration
	QueryTimeout time.Duration
	Encrypt      bool
}

func NewManager(resolver CredentialResolver) Manager {
	return Manager{
		Resolver:     resolver,
		Application:  "HeartbeatDBCollector",
		DialTimeout:  5 * time.Second,
		QueryTimeout: 5 * time.Second,
		Encrypt:      true,
	}
}

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
