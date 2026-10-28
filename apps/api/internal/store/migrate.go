package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/stdlib"

	"heartbeat/db/migrations"
	"heartbeat/internal/credentials"
)

// migrateLockTimeout bounds the wait for another replica's migrations:
// golang-migrate holds a PostgreSQL advisory lock while it migrates, so
// replicas starting together apply each migration once, and the others wait
// here. Longer than any migration should take, so a slow one does not fail
// the replicas waiting for it.
const migrateLockTimeout = 5 * time.Minute

// LatestVersion returns the schema version of the newest migration this
// build embeds.
func LatestVersion() (int64, error) {
	latest, err := migrations.Latest(migrations.FS)
	if err != nil {
		return 0, err
	}
	return int64(latest), nil
}

// MigrationResult is what [MigrateUp] did.
type MigrationResult struct {
	// From is the schema version before, 0 for a database never migrated.
	From uint
	// To is the schema version after.
	To uint
	// Newer is set when the database was already past the newest migration
	// this build embeds, as after rolling the API back: nothing was
	// applied.
	Newer bool
}

// MigrateUp applies, in order, every migration in db/migrations newer than
// the database's schema version, and records each in schema_migrations.
// Replicas may run it together; each migration is applied once.
//
// A database already past the newest migration this build embeds is left
// alone and is not an error, so rolling the API back to an older release
// still starts it (migrations are expand then contract). A dirty version,
// left by a migration that failed partway, is an error until repaired.
//
// When ctx is done, MigrateUp stops after the migration in progress.
func MigrateUp(ctx context.Context, cfg Config, resolver credentials.Resolver, logger *slog.Logger) (MigrationResult, error) {
	m, err := newMigrate(cfg, resolver, logger)
	if err != nil {
		return MigrationResult{}, err
	}
	defer closeMigrate(m, logger)

	from, dirty, err := version(m)
	if err != nil {
		return MigrationResult{}, err
	}
	latest, err := migrations.Latest(migrations.FS)
	if err != nil {
		return MigrationResult{}, err
	}
	if !dirty && uint64(from) > latest {
		return MigrationResult{From: from, To: from, Newer: true}, nil
	}

	done := make(chan error, 1)
	go func() { done <- m.Up() }()
	select {
	case err = <-done:
	case <-ctx.Done():
		m.GracefulStop <- true
		err = <-done
		if err == nil || errors.Is(err, migrate.ErrNoChange) {
			err = ctx.Err()
		}
	}
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return MigrationResult{From: from}, fmt.Errorf("migrate up from version %d: %w", from, err)
	}
	to, _, err := version(m)
	if err != nil {
		return MigrationResult{From: from}, err
	}
	return MigrationResult{From: from, To: to}, nil
}

// MigrationVersion returns the database's schema version, 0 when it was
// never migrated, and whether the migration to it failed partway.
func MigrationVersion(cfg Config, resolver credentials.Resolver, logger *slog.Logger) (uint, bool, error) {
	m, err := newMigrate(cfg, resolver, logger)
	if err != nil {
		return 0, false, err
	}
	defer closeMigrate(m, logger)
	return version(m)
}

// version is m.Version, with 0 for a database never migrated.
func version(m *migrate.Migrate) (uint, bool, error) {
	v, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read schema version: %w", err)
	}
	return v, dirty, nil
}

// newMigrate connects to the database and returns golang-migrate set up
// with the embedded migrations. It creates schema_migrations when missing.
func newMigrate(cfg Config, resolver credentials.Resolver, logger *slog.Logger) (*migrate.Migrate, error) {
	connConfig, err := cfg.connConfig()
	if err != nil {
		return nil, err
	}
	db := stdlib.OpenDB(*connConfig, stdlib.OptionBeforeConnect(beforeConnect(resolver, cfg.CredentialRef)))
	driver, err := migratepgx.WithInstance(db, &migratepgx.Config{})
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect to postgresql: %w", err)
	}
	source, err := iofs.New(migrations.FS, ".")
	if err != nil {
		_ = driver.Close()
		return nil, err
	}
	m, err := migrate.NewWithInstance("iofs", source, "pgx5", driver)
	if err != nil {
		_ = source.Close()
		_ = driver.Close()
		return nil, err
	}
	m.LockTimeout = migrateLockTimeout
	m.Log = migrateLogger{logger: logger}
	return m, nil
}

func closeMigrate(m *migrate.Migrate, logger *slog.Logger) {
	sourceErr, dbErr := m.Close()
	if err := errors.Join(sourceErr, dbErr); err != nil {
		logger.Warn("migrate: close", "error", err)
	}
}

// migrateLogger sends golang-migrate's progress lines to slog.
type migrateLogger struct {
	logger *slog.Logger
}

func (l migrateLogger) Printf(format string, v ...any) {
	l.logger.Info("migrate: " + strings.TrimSpace(fmt.Sprintf(format, v...)))
}

func (migrateLogger) Verbose() bool { return false }
