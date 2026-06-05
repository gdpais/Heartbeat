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

type Endpoint struct {
	BaseURL            string            `yaml:"base_url"`
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

type CollectorRuntimeConfig struct {
	ID             string
	Kind           string
	Enabled        bool
	CredentialRef  string
	Environment    string
	TargetNames    []string
	ScrapeInterval time.Duration
	Targets        []TargetRuntimeConfig
	Probes         []ProbeRuntimeConfig
}

type TargetRuntimeConfig struct {
	Name            string
	EnvironmentSlug string
	Engine          string
	Host            string
	Port            int
	DatabaseName    string
	CredentialRef   string
	Probes          []ProbeRuntimeConfig
}

type ProbeRuntimeConfig struct {
	Name          string
	QueryTemplate string
	TimeoutMS     int
}

type RuntimeConfig struct {
	Version      string
	Grafana      Endpoint
	Loki         Endpoint
	Alertmanager Endpoint
	Collectors   []CollectorRuntimeConfig
}

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
