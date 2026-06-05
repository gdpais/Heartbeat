// Package config loads and normalises the integrations YAML file into typed
// runtime configuration structs consumed by the collector subsystem.
//
// The integrations file declares one or more collectors, each with a set of
// database targets and probes to execute.  LoadRuntimeConfig is the single
// public entry-point; it reads the file, validates required fields, fills in
// defaults, and returns a [RuntimeConfig].
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

// Endpoint holds the connection details for an external service integration
// (Grafana, Loki, or Alertmanager).
type Endpoint struct {
	// BaseURL is the root HTTP URL of the service.
	BaseURL string `yaml:"base_url"`
	// DashboardTemplates maps logical template names to their dashboard URLs or
	// identifiers within the service.
	DashboardTemplates map[string]string `yaml:"dashboard_templates"`
}

type collectorDocument struct {
	ID            string                  `yaml:"id"`
	Kind          string                  `yaml:"kind"`
	Enabled       bool                    `yaml:"enabled"`
	CredentialRef string                  `yaml:"credential_ref"`
	Config        collectorConfigDocument `yaml:"config"`
}

type collectorConfigDocument struct {
	Environment    string           `yaml:"environment"`
	ScrapeInterval string           `yaml:"scrape_interval"`
	TargetNames    []string         `yaml:"target_names"`
	Targets        []targetDocument `yaml:"targets"`
	Probes         []probeDocument  `yaml:"probes"`
}

type targetDocument struct {
	Name          string          `yaml:"name"`
	Environment   string          `yaml:"environment"`
	Host          string          `yaml:"host"`
	Port          int             `yaml:"port"`
	DatabaseName  string          `yaml:"database_name"`
	CredentialRef string          `yaml:"credential_ref"`
	Probes        []probeDocument `yaml:"probes"`
}

type probeDocument struct {
	Name          string `yaml:"name"`
	QueryTemplate string `yaml:"query_template"`
	TimeoutMS     int    `yaml:"timeout_ms"`
}

type document struct {
	Grafana      Endpoint            `yaml:"grafana"`
	Loki         Endpoint            `yaml:"loki"`
	Alertmanager Endpoint            `yaml:"alertmanager"`
	Collectors   []collectorDocument `yaml:"collectors"`
}

// CollectorRuntimeConfig is the normalised, ready-to-use representation of a
// single collector declaration from the integrations file.
type CollectorRuntimeConfig struct {
	// ID is the unique identifier for this collector (required).
	ID string
	// Kind specifies the database engine, e.g. "sqlserver".
	Kind string
	// Enabled indicates whether this collector should be started.
	Enabled bool
	// CredentialRef is the default credential reference applied to targets that
	// do not specify their own.
	CredentialRef string
	// Environment is the default environment slug for targets that do not
	// specify their own.
	Environment string
	// TargetNames optionally filters the active targets to a named subset; an
	// empty slice means all targets are active.
	TargetNames []string
	// ScrapeInterval is how often the collector polls its targets.
	ScrapeInterval time.Duration
	// Targets is the list of database targets this collector will probe.
	Targets []TargetRuntimeConfig
	// Probes is the default probe set inherited by targets that define no
	// probes of their own.
	Probes []ProbeRuntimeConfig
}

// TargetRuntimeConfig holds the connection parameters and probe assignments
// for a single database target.
type TargetRuntimeConfig struct {
	// Name is the human-readable identifier for this target.
	Name string
	// EnvironmentSlug categorises the target (e.g. "production", "staging").
	EnvironmentSlug string
	// Engine is the database engine type inherited from the parent collector.
	Engine string
	// Host is the network hostname or IP address of the database server.
	Host string
	// Port is the TCP port of the database server.
	Port int
	// DatabaseName is the logical database to connect to.
	DatabaseName string
	// CredentialRef references the credential used to authenticate; falls back
	// to the collector-level credential when empty.
	CredentialRef string
	// Probes is the set of probes to execute against this target.
	Probes []ProbeRuntimeConfig
}

// ProbeRuntimeConfig describes a single probe to execute against a target.
type ProbeRuntimeConfig struct {
	// Name identifies the probe and must match a key in the probe catalog.
	Name string
	// QueryTemplate overrides the catalog's default SQL query when non-empty.
	QueryTemplate string
	// TimeoutMS is the per-execution deadline in milliseconds; 0 means use the
	// catalog or interval default.
	TimeoutMS int
}

// RuntimeConfig is the fully parsed and validated representation of the
// integrations file.  It is the root value returned by [LoadRuntimeConfig].
type RuntimeConfig struct {
	// Version is the SHA-256 hex digest of the raw file content, used to
	// detect configuration changes at runtime.
	Version      string
	Grafana      Endpoint
	Loki         Endpoint
	Alertmanager Endpoint
	// Collectors lists all collector declarations, both enabled and disabled.
	Collectors []CollectorRuntimeConfig
}

// LoadRuntimeConfig reads the YAML integrations file at path, validates its
// contents, and returns the normalised [RuntimeConfig].
//
// The returned Version field is the SHA-256 digest of the raw file bytes so
// callers can detect configuration changes without re-parsing the file.
// An error is returned if the file cannot be read, cannot be parsed, or
// contains invalid collector declarations.
func LoadRuntimeConfig(path string) (RuntimeConfig, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return RuntimeConfig{}, fmt.Errorf("read runtime config: %w", err)
	}
	var doc document
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return RuntimeConfig{}, fmt.Errorf("parse runtime config: %w", err)
	}
	cfg := RuntimeConfig{
		Version:      hash(content),
		Grafana:      doc.Grafana,
		Loki:         doc.Loki,
		Alertmanager: doc.Alertmanager,
	}
	for _, collector := range doc.Collectors {
		runtimeCfg, err := normalizeCollector(collector)
		if err != nil {
			return RuntimeConfig{}, err
		}
		cfg.Collectors = append(cfg.Collectors, runtimeCfg)
	}
	return cfg, nil
}

// EnabledCollectors returns a deterministically sorted slice of collectors
// that are both enabled and match the given engine kind (e.g. "sqlserver").
func (c RuntimeConfig) EnabledCollectors(kind string) []CollectorRuntimeConfig {
	var out []CollectorRuntimeConfig
	for _, collector := range c.Collectors {
		if collector.Enabled && collector.Kind == kind {
			out = append(out, collector)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func normalizeCollector(doc collectorDocument) (CollectorRuntimeConfig, error) {
	cfg := CollectorRuntimeConfig{
		ID:             doc.ID,
		Kind:           doc.Kind,
		Enabled:        doc.Enabled,
		CredentialRef:  doc.CredentialRef,
		ScrapeInterval: 30 * time.Second,
	}
	cfg.Environment = doc.Config.Environment
	cfg.TargetNames = append(cfg.TargetNames, doc.Config.TargetNames...)
	cfg.Probes = normalizeProbes(doc.Config.Probes)
	if doc.Config.ScrapeInterval != "" {
		interval, err := time.ParseDuration(doc.Config.ScrapeInterval)
		if err != nil {
			return CollectorRuntimeConfig{}, fmt.Errorf("invalid scrape_interval for collector %s: %w", doc.ID, err)
		}
		cfg.ScrapeInterval = interval
	}
	for _, target := range doc.Config.Targets {
		runtimeTarget := TargetRuntimeConfig{
			Name:            target.Name,
			EnvironmentSlug: target.Environment,
			Engine:          doc.Kind,
			Host:            target.Host,
			Port:            target.Port,
			DatabaseName:    target.DatabaseName,
			CredentialRef:   target.CredentialRef,
		}
		if runtimeTarget.EnvironmentSlug == "" {
			runtimeTarget.EnvironmentSlug = cfg.Environment
		}
		if runtimeTarget.CredentialRef == "" {
			runtimeTarget.CredentialRef = cfg.CredentialRef
		}
		runtimeTarget.Probes = normalizeProbes(target.Probes)
		if len(runtimeTarget.Probes) == 0 {
			runtimeTarget.Probes = append(runtimeTarget.Probes, cfg.Probes...)
		}
		cfg.Targets = append(cfg.Targets, runtimeTarget)
	}
	if cfg.ID == "" {
		return CollectorRuntimeConfig{}, fmt.Errorf("collector id is required")
	}
	if cfg.Kind == "" {
		return CollectorRuntimeConfig{}, fmt.Errorf("collector kind is required for %s", cfg.ID)
	}
	if cfg.ScrapeInterval <= 0 {
		return CollectorRuntimeConfig{}, fmt.Errorf("collector %s must have positive scrape interval", cfg.ID)
	}
	return cfg, nil
}

func normalizeProbes(docs []probeDocument) []ProbeRuntimeConfig {
	probes := make([]ProbeRuntimeConfig, 0, len(docs))
	for _, doc := range docs {
		if doc.Name == "" {
			continue
		}
		probes = append(probes, ProbeRuntimeConfig{
			Name:          doc.Name,
			QueryTemplate: doc.QueryTemplate,
			TimeoutMS:     doc.TimeoutMS,
		})
	}
	return probes
}

func hash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
