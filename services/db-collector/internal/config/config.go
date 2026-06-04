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
	ID            string         `yaml:"id"`
	Kind          string         `yaml:"kind"`
	Enabled       bool           `yaml:"enabled"`
	CredentialRef string         `yaml:"credential_ref"`
	Config        map[string]any `yaml:"config"`
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
	if env, ok := doc.Config["environment"].(string); ok {
		cfg.Environment = env
	}
	if rawInterval, ok := doc.Config["scrape_interval"].(string); ok && rawInterval != "" {
		interval, err := time.ParseDuration(rawInterval)
		if err != nil {
			return CollectorRuntimeConfig{}, fmt.Errorf("invalid scrape_interval for collector %s: %w", doc.ID, err)
		}
		cfg.ScrapeInterval = interval
	}
	if rawTargets, ok := doc.Config["target_names"].([]any); ok {
		for _, raw := range rawTargets {
			if value, ok := raw.(string); ok {
				cfg.TargetNames = append(cfg.TargetNames, value)
			}
		}
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

func hash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
