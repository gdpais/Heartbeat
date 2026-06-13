// Package config loads, validates, versions, and redacts Heartbeat integration
// runtime configuration.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Endpoint holds the connection details for an external service integration.
type Endpoint struct {
	BaseURL            string            `yaml:"base_url"`
	Endpoint           string            `yaml:"endpoint"`
	DashboardTemplates map[string]string `yaml:"dashboard_templates"`
	DeepLinkTemplates  map[string]string `yaml:"deep_link_templates"`
}

// NotificationChannel declares outbound delivery targets without embedding
// secret values in the runtime config.
type NotificationChannel struct {
	ID            string            `yaml:"id"`
	ChannelType   string            `yaml:"channel_type"`
	TargetRef     string            `yaml:"target_ref"`
	CredentialRef string            `yaml:"credential_ref"`
	Config        map[string]string `yaml:"config"`
}

type document struct {
	Grafana              Endpoint               `yaml:"grafana"`
	Loki                 Endpoint               `yaml:"loki"`
	Alertmanager         Endpoint               `yaml:"alertmanager"`
	OpenTelemetry        Endpoint               `yaml:"opentelemetry"`
	NotificationChannels []NotificationChannel  `yaml:"notification_channels"`
	Collectors           []collectorDocument    `yaml:"collectors"`
	CredentialRefs       map[string]string      `yaml:"credential_refs"`
	Delivery             map[string]interface{} `yaml:"delivery"`
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

// CollectorRuntimeConfig is the normalised, ready-to-use representation of a
// single collector declaration from the integrations file.
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

// TargetRuntimeConfig holds the connection parameters and probe assignments
// for a single database target.
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

// ProbeRuntimeConfig describes a single probe to execute against a target.
type ProbeRuntimeConfig struct {
	Name          string
	QueryTemplate string
	TimeoutMS     int
}

// RuntimeConfig is the fully parsed and validated integrations file.
type RuntimeConfig struct {
	Version              string
	Grafana              Endpoint
	Loki                 Endpoint
	Alertmanager         Endpoint
	OpenTelemetry        Endpoint
	NotificationChannels []NotificationChannel
	Collectors           []CollectorRuntimeConfig
	CredentialRefs       map[string]string
}

// LoadRuntimeConfig reads, validates, normalises, and versions a candidate
// integrations YAML file. Invalid candidates return an error and no partial
// RuntimeConfig.
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
		Version:              hash(content),
		Grafana:              normalizeEndpoint(doc.Grafana),
		Loki:                 normalizeEndpoint(doc.Loki),
		Alertmanager:         normalizeEndpoint(doc.Alertmanager),
		OpenTelemetry:        normalizeEndpoint(doc.OpenTelemetry),
		NotificationChannels: append([]NotificationChannel(nil), doc.NotificationChannels...),
		CredentialRefs:       copyStringMap(doc.CredentialRefs),
	}
	for _, collector := range doc.Collectors {
		runtimeCfg, err := normalizeCollector(collector)
		if err != nil {
			return RuntimeConfig{}, err
		}
		cfg.Collectors = append(cfg.Collectors, runtimeCfg)
	}
	if err := validate(cfg); err != nil {
		return RuntimeConfig{}, err
	}
	return cfg, nil
}

// EnabledCollectors returns a deterministically sorted slice of enabled
// collectors matching kind.
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

// Redacted returns a copy suitable for diagnostics. Secret references remain
// visible, but their concrete values are masked.
func (c RuntimeConfig) Redacted() RuntimeConfig {
	out := c
	out.Collectors = append([]CollectorRuntimeConfig(nil), c.Collectors...)
	out.NotificationChannels = append([]NotificationChannel(nil), c.NotificationChannels...)
	out.CredentialRefs = map[string]string{}
	for key := range c.CredentialRefs {
		out.CredentialRefs[key] = "<redacted>"
	}
	for i := range out.NotificationChannels {
		if out.NotificationChannels[i].CredentialRef != "" {
			out.NotificationChannels[i].CredentialRef = redactRef(out.NotificationChannels[i].CredentialRef)
		}
	}
	for i := range out.Collectors {
		out.Collectors[i].CredentialRef = redactRef(out.Collectors[i].CredentialRef)
		for j := range out.Collectors[i].Targets {
			out.Collectors[i].Targets[j].CredentialRef = redactRef(out.Collectors[i].Targets[j].CredentialRef)
		}
	}
	return out
}

func normalizeEndpoint(endpoint Endpoint) Endpoint {
	if endpoint.DashboardTemplates == nil {
		endpoint.DashboardTemplates = map[string]string{}
	}
	if endpoint.DeepLinkTemplates == nil {
		endpoint.DeepLinkTemplates = map[string]string{}
	}
	return endpoint
}

func normalizeCollector(doc collectorDocument) (CollectorRuntimeConfig, error) {
	cfg := CollectorRuntimeConfig{
		ID:             strings.TrimSpace(doc.ID),
		Kind:           strings.TrimSpace(doc.Kind),
		Enabled:        doc.Enabled,
		CredentialRef:  strings.TrimSpace(doc.CredentialRef),
		ScrapeInterval: 30 * time.Second,
		Environment:    strings.TrimSpace(doc.Config.Environment),
		TargetNames:    append([]string(nil), doc.Config.TargetNames...),
		Probes:         normalizeProbes(doc.Config.Probes),
	}
	if doc.Config.ScrapeInterval != "" {
		interval, err := time.ParseDuration(doc.Config.ScrapeInterval)
		if err != nil {
			return CollectorRuntimeConfig{}, fmt.Errorf("invalid scrape_interval for collector %s: %w", doc.ID, err)
		}
		cfg.ScrapeInterval = interval
	}
	for _, target := range doc.Config.Targets {
		runtimeTarget := TargetRuntimeConfig{
			Name:            strings.TrimSpace(target.Name),
			EnvironmentSlug: strings.TrimSpace(target.Environment),
			Engine:          cfg.Kind,
			Host:            strings.TrimSpace(target.Host),
			Port:            target.Port,
			DatabaseName:    strings.TrimSpace(target.DatabaseName),
			CredentialRef:   strings.TrimSpace(target.CredentialRef),
			Probes:          normalizeProbes(target.Probes),
		}
		if runtimeTarget.EnvironmentSlug == "" {
			runtimeTarget.EnvironmentSlug = cfg.Environment
		}
		if runtimeTarget.CredentialRef == "" {
			runtimeTarget.CredentialRef = cfg.CredentialRef
		}
		if len(runtimeTarget.Probes) == 0 {
			runtimeTarget.Probes = append(runtimeTarget.Probes, cfg.Probes...)
		}
		cfg.Targets = append(cfg.Targets, runtimeTarget)
	}
	return cfg, nil
}

func normalizeProbes(docs []probeDocument) []ProbeRuntimeConfig {
	probes := make([]ProbeRuntimeConfig, 0, len(docs))
	for _, doc := range docs {
		if strings.TrimSpace(doc.Name) == "" {
			continue
		}
		probes = append(probes, ProbeRuntimeConfig{
			Name:          strings.TrimSpace(doc.Name),
			QueryTemplate: doc.QueryTemplate,
			TimeoutMS:     doc.TimeoutMS,
		})
	}
	return probes
}

func validate(cfg RuntimeConfig) error {
	if err := validateURL("grafana.base_url", cfg.Grafana.BaseURL, true); err != nil {
		return err
	}
	if err := validateURL("loki.base_url", cfg.Loki.BaseURL, true); err != nil {
		return err
	}
	if err := validateURL("alertmanager.base_url", cfg.Alertmanager.BaseURL, true); err != nil {
		return err
	}
	if err := validateURL("opentelemetry.endpoint", cfg.OpenTelemetry.Endpoint, false); err != nil {
		return err
	}
	ids := map[string]struct{}{}
	for _, collector := range cfg.Collectors {
		if collector.ID == "" {
			return fmt.Errorf("collector id is required")
		}
		if _, exists := ids[collector.ID]; exists {
			return fmt.Errorf("collector id %q must be unique", collector.ID)
		}
		ids[collector.ID] = struct{}{}
		if collector.Kind == "" {
			return fmt.Errorf("collector kind is required for %s", collector.ID)
		}
		if collector.ScrapeInterval <= 0 {
			return fmt.Errorf("collector %s must have positive scrape interval", collector.ID)
		}
		if collector.CredentialRef != "" && !validSecretRef(collector.CredentialRef) {
			return fmt.Errorf("collector %s credential_ref must be a secret reference", collector.ID)
		}
		if err := validateTargets(collector); err != nil {
			return err
		}
	}
	notificationIDs := map[string]struct{}{}
	for _, channel := range cfg.NotificationChannels {
		if channel.ID == "" {
			return fmt.Errorf("notification channel id is required")
		}
		if _, exists := notificationIDs[channel.ID]; exists {
			return fmt.Errorf("notification channel id %q must be unique", channel.ID)
		}
		notificationIDs[channel.ID] = struct{}{}
		if channel.ChannelType == "" || channel.TargetRef == "" {
			return fmt.Errorf("notification channel %s requires channel_type and target_ref", channel.ID)
		}
		if channel.CredentialRef != "" && !validSecretRef(channel.CredentialRef) {
			return fmt.Errorf("notification channel %s credential_ref must be a secret reference", channel.ID)
		}
	}
	return nil
}

func validateTargets(collector CollectorRuntimeConfig) error {
	targets := map[string]struct{}{}
	for _, target := range collector.Targets {
		if target.Name == "" {
			return fmt.Errorf("collector %s target name is required", collector.ID)
		}
		if _, exists := targets[target.Name]; exists {
			return fmt.Errorf("collector %s target %q must be unique", collector.ID, target.Name)
		}
		targets[target.Name] = struct{}{}
		if target.Host == "" {
			return fmt.Errorf("collector %s target %s host is required", collector.ID, target.Name)
		}
		if target.Port < 1 || target.Port > 65535 {
			return fmt.Errorf("collector %s target %s port must be between 1 and 65535", collector.ID, target.Name)
		}
		if target.CredentialRef != "" && !validSecretRef(target.CredentialRef) {
			return fmt.Errorf("collector %s target %s credential_ref must be a secret reference", collector.ID, target.Name)
		}
		for _, probe := range target.Probes {
			if probe.TimeoutMS < 0 {
				return fmt.Errorf("collector %s target %s probe %s timeout_ms cannot be negative", collector.ID, target.Name, probe.Name)
			}
		}
	}
	for _, selected := range collector.TargetNames {
		if _, exists := targets[selected]; !exists && len(targets) > 0 {
			return fmt.Errorf("collector %s target_names references unknown target %q", collector.ID, selected)
		}
	}
	return nil
}

func validateURL(name, raw string, required bool) error {
	if raw == "" {
		if required {
			return fmt.Errorf("%s is required", name)
		}
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("%s must be an absolute URL", name)
	}
	return nil
}

func validSecretRef(ref string) bool {
	return strings.HasPrefix(ref, "kv/") || strings.HasPrefix(ref, "secret/") || strings.HasPrefix(ref, "env/")
}

func redactRef(ref string) string {
	if ref == "" {
		return ""
	}
	return ref + ":<redacted>"
}

func copyStringMap(in map[string]string) map[string]string {
	if in == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func hash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
