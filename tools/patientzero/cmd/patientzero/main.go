// PatientZero is a synthetic OutSystems 11 application for Heartbeat's tests
// and demos (ADR 0010). It serves entry points modelled on OutSystems ones;
// each request runs SQL against the business database and writes the log rows
// the platform would write into the logging database.
//
// Configuration is read from the environment:
//
//	PATIENTZERO_LISTEN                         listen address [:8080]
//	PATIENTZERO_SQL_HOST                       SQL Server host (required)
//	PATIENTZERO_SQL_PORT                       SQL Server port [1433]
//	PATIENTZERO_SQL_CREDENTIAL                 <login>:<password> (required)
//	PATIENTZERO_SQL_TRUST_SERVER_CERTIFICATE   true to trust a self-signed certificate [false]
//	PATIENTZERO_DATABASE                       business database [patientzero]
//	PATIENTZERO_LOG_DATABASE                   logging database [patientzero_log]
//	PATIENTZERO_SCHEMA_DIR                     the oslog fixtures the logging schema is built from [/app/oslog]
//	PATIENTZERO_LOG_READER_ROLE                role granted SELECT on the log views; empty for none [heartbeat_oslog_reader]
//	PATIENTZERO_FRONT_END                      front-end server name in the log rows [the host name]
package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	_ "github.com/microsoft/go-mssqldb"

	"heartbeat/tools/patientzero/internal/app"
	"heartbeat/tools/patientzero/internal/logschema"
	"heartbeat/tools/patientzero/internal/oslog"
	"heartbeat/tools/patientzero/internal/shop"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("patientzero stopped", "err", err)
		os.Exit(1)
	}
}

type config struct {
	listen, host, login, password   string
	port                            int
	trust                           bool
	database, logDatabase           string
	schemaDir, readerRole, frontEnd string
}

func env(name, def string) string {
	if v, ok := os.LookupEnv(name); ok {
		return v
	}
	return def
}

func loadConfig() (config, error) {
	c := config{
		listen:      env("PATIENTZERO_LISTEN", ":8080"),
		host:        os.Getenv("PATIENTZERO_SQL_HOST"),
		database:    env("PATIENTZERO_DATABASE", "patientzero"),
		logDatabase: env("PATIENTZERO_LOG_DATABASE", "patientzero_log"),
		schemaDir:   env("PATIENTZERO_SCHEMA_DIR", "/app/oslog"),
		readerRole:  env("PATIENTZERO_LOG_READER_ROLE", "heartbeat_oslog_reader"),
		frontEnd:    os.Getenv("PATIENTZERO_FRONT_END"),
	}
	if c.host == "" {
		return c, errors.New("PATIENTZERO_SQL_HOST is required")
	}
	login, password, ok := strings.Cut(os.Getenv("PATIENTZERO_SQL_CREDENTIAL"), ":")
	if !ok || login == "" || password == "" {
		return c, errors.New("PATIENTZERO_SQL_CREDENTIAL must be <login>:<password>")
	}
	if strings.EqualFold(login, "sa") {
		return c, errors.New("PATIENTZERO_SQL_CREDENTIAL must name PatientZero's own login, not sa")
	}
	c.login, c.password = login, password
	port, err := strconv.Atoi(env("PATIENTZERO_SQL_PORT", "1433"))
	if err != nil || port < 1 || port > 65535 {
		return c, errors.New("PATIENTZERO_SQL_PORT must be a port number")
	}
	c.port = port
	if c.trust, err = strconv.ParseBool(env("PATIENTZERO_SQL_TRUST_SERVER_CERTIFICATE", "false")); err != nil {
		return c, errors.New("PATIENTZERO_SQL_TRUST_SERVER_CERTIFICATE must be true or false")
	}
	if c.database == c.logDatabase {
		return c, errors.New("PATIENTZERO_DATABASE and PATIENTZERO_LOG_DATABASE must differ")
	}
	if c.frontEnd == "" {
		c.frontEnd, _ = os.Hostname()
	}
	return c, nil
}

func (c config) dsn(database string) string {
	q := url.Values{}
	q.Set("database", database)
	q.Set("encrypt", "true")
	q.Set("TrustServerCertificate", strconv.FormatBool(c.trust))
	q.Set("app name", "PatientZero")
	u := url.URL{
		Scheme:   "sqlserver",
		User:     url.UserPassword(c.login, c.password),
		Host:     net.JoinHostPort(c.host, strconv.Itoa(c.port)),
		RawQuery: q.Encode(),
	}
	return u.String()
}

func run(log *slog.Logger) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	types, err := logschema.Load(cfg.schemaDir)
	if err != nil {
		return err
	}
	business, err := sql.Open("sqlserver", cfg.dsn(cfg.database))
	if err != nil {
		return err
	}
	defer business.Close()
	logging, err := sql.Open("sqlserver", cfg.dsn(cfg.logDatabase))
	if err != nil {
		return err
	}
	defer logging.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The schemas are applied in the background, retried until SQL Server
	// answers; /readyz fails until they are in place.
	var migrated atomic.Bool
	go func() {
		for {
			err := app.Migrate(ctx, business, logging, types, cfg.readerRole)
			if err == nil {
				migrated.Store(true)
				log.Info("schemas ready", "database", cfg.database, "log_database", cfg.logDatabase, "log_types", len(types))
				return
			}
			log.Warn("applying the schemas failed; retrying", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}()

	srv := &app.Server{
		Store:    &shop.SQLStore{DB: business},
		Logs:     &oslog.SQLWriter{DB: logging, Schema: oslog.NewSchema(types)},
		Schema:   oslog.NewSchema(types),
		FrontEnd: cfg.frontEnd,
		Log:      log,
		Ready: func(ctx context.Context) error {
			if !migrated.Load() {
				return errors.New("schemas not applied yet")
			}
			if err := business.PingContext(ctx); err != nil {
				return err
			}
			return logging.PingContext(ctx)
		},
	}
	httpServer := &http.Server{
		Addr:              cfg.listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- httpServer.ListenAndServe() }()
	log.Info("patientzero listening", "addr", cfg.listen, "front_end", cfg.frontEnd)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdown)
}
