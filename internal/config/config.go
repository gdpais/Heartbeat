// Package config loads, validates, versions, and redacts Heartbeat integration
// runtime configuration.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
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

// redactedValue replaces secret values in diagnostics.
const redactedValue = "<redacted>"

// Redacted returns a copy suitable for diagnostics. Secret references remain
// visible, but their concrete values are masked. Notification channel config
// values are masked too, since channel settings such as SMTP or webhook
// options can carry secrets, and userinfo (user:password@) is stripped from
// every URL-bearing field.
func (c RuntimeConfig) Redacted() RuntimeConfig {
	out := c
	out.Grafana = redactEndpoint(c.Grafana)
	out.Loki = redactEndpoint(c.Loki)
	out.Alertmanager = redactEndpoint(c.Alertmanager)
	out.OpenTelemetry = redactEndpoint(c.OpenTelemetry)
	out.Collectors = cloneCollectors(c.Collectors)
	out.NotificationChannels = cloneNotificationChannels(c.NotificationChannels)
	out.CredentialRefs = map[string]string{}
	for key := range c.CredentialRefs {
		out.CredentialRefs[key] = redactedValue
	}
	for i := range out.NotificationChannels {
		channel := &out.NotificationChannels[i]
		if channel.CredentialRef != "" {
			channel.CredentialRef = redactRef(channel.CredentialRef)
		}
		channel.TargetRef = redactTargetRef(channel.TargetRef)
		for key := range channel.Config {
			channel.Config[key] = redactedValue
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

// redactEndpoint returns a copy of in with userinfo stripped from its URLs
// and URL templates.
func redactEndpoint(in Endpoint) Endpoint {
	out := cloneEndpoint(in)
	out.BaseURL = stripUserinfo(out.BaseURL)
	out.Endpoint = stripUserinfo(out.Endpoint)
	for key, value := range out.DashboardTemplates {
		out.DashboardTemplates[key] = stripUserinfo(value)
	}
	for key, value := range out.DeepLinkTemplates {
		out.DeepLinkTemplates[key] = stripUserinfo(value)
	}
	return out
}

func cloneEndpoint(in Endpoint) Endpoint {
	in.DashboardTemplates = copyStringMap(in.DashboardTemplates)
	in.DeepLinkTemplates = copyStringMap(in.DeepLinkTemplates)
	return in
}

func cloneCollectors(in []CollectorRuntimeConfig) []CollectorRuntimeConfig {
	out := make([]CollectorRuntimeConfig, len(in))
	for i, collector := range in {
		out[i] = collector
		out[i].TargetNames = append([]string(nil), collector.TargetNames...)
		out[i].Probes = append([]ProbeRuntimeConfig(nil), collector.Probes...)
		out[i].Targets = make([]TargetRuntimeConfig, len(collector.Targets))
		for j, target := range collector.Targets {
			out[i].Targets[j] = target
			out[i].Targets[j].Probes = append([]ProbeRuntimeConfig(nil), target.Probes...)
		}
	}
	return out
}

func cloneNotificationChannels(in []NotificationChannel) []NotificationChannel {
	out := make([]NotificationChannel, len(in))
	for i, channel := range in {
		out[i] = channel
		if channel.Config != nil {
			out[i].Config = make(map[string]string, len(channel.Config))
			for key, value := range channel.Config {
				out[i].Config[key] = value
			}
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
	if err := validateEndpointCredentials(cfg); err != nil {
		return err
	}
	if err := validateURL("grafana.base_url", cfg.Grafana.BaseURL, true); err != nil {
		return err
	}
	if err := validateURL("loki.base_url", cfg.Loki.BaseURL, true); err != nil {
		return err
	}
	if err := validateURL("loki.endpoint", cfg.Loki.Endpoint, false); err != nil {
		return err
	}
	if err := validateURL("alertmanager.base_url", cfg.Alertmanager.BaseURL, true); err != nil {
		return err
	}
	if err := validateURL("alertmanager.endpoint", cfg.Alertmanager.Endpoint, false); err != nil {
		return err
	}
	if err := validateURL("opentelemetry.endpoint", cfg.OpenTelemetry.Endpoint, false); err != nil {
		return err
	}
	for key, ref := range cfg.CredentialRefs {
		if ref != "" && !validSecretRef(ref) {
			return fmt.Errorf("credential_refs.%s must be a secret reference", key)
		}
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
		if targetRefCredentials(channel.TargetRef) {
			return fmt.Errorf("notification channel %s target_ref %s", channel.ID, embeddedCredentialsHint)
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
		if !validHost(target.Host) {
			return fmt.Errorf("collector %s target %s host must be a host name, an IPv4 address or a bare IPv6 address, with no port, brackets, credentials or path; set the port in port", collector.ID, target.Name)
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

// embeddedCredentialsHint ends validation errors for URLs with userinfo. The
// URL itself is never echoed, since it contains the credential.
const embeddedCredentialsHint = "must not embed credentials (user:password@); use a credential reference"

// validateEndpointCredentials rejects userinfo in every URL-bearing endpoint
// field: base_url, endpoint and the dashboard and deep-link templates of
// grafana, loki, alertmanager and opentelemetry. Credentials belong in secret
// references; a URL is copied into logs, diagnostics, browser links and
// error messages.
func validateEndpointCredentials(cfg RuntimeConfig) error {
	for _, section := range []struct {
		name     string
		endpoint Endpoint
	}{
		{"grafana", cfg.Grafana},
		{"loki", cfg.Loki},
		{"alertmanager", cfg.Alertmanager},
		{"opentelemetry", cfg.OpenTelemetry},
	} {
		if hasUserinfo(section.endpoint.BaseURL) {
			return fmt.Errorf("%s.base_url %s", section.name, embeddedCredentialsHint)
		}
		if hasUserinfo(section.endpoint.Endpoint) {
			return fmt.Errorf("%s.endpoint %s", section.name, embeddedCredentialsHint)
		}
		for _, templates := range []struct {
			field  string
			values map[string]string
		}{
			{"dashboard_templates", section.endpoint.DashboardTemplates},
			{"deep_link_templates", section.endpoint.DeepLinkTemplates},
		} {
			for key, value := range templates.values {
				if hasUserinfo(value) {
					return fmt.Errorf("%s.%s.%s %s", section.name, templates.field, key, embeddedCredentialsHint)
				}
			}
		}
	}
	return nil
}

// specialSchemes are the WHATWG "special" schemes. Browsers parse an
// authority after them even with backslashes, one slash or none
// ("http:\\user:pass@host", "http:user:pass@host").
var specialSchemes = map[string]bool{"http": true, "https": true, "ws": true, "wss": true, "ftp": true, "file": true}

// userinfoSpan returns the byte range [start, end) of the userinfo in raw,
// including its trailing '@', and whether raw has userinfo at all.
//
// It works on the text rather than on url.Parse, so it also covers values
// url.Parse rejects (templates with ${placeholders} in the host) and the
// lenient forms browsers accept for special schemes. As in net/url, the
// authority ends at the first '/', '?' or '#' (and, for special schemes as in
// browsers, '\'), and userinfo ends at the last '@' in it.
func userinfoSpan(raw string) (start, end int, ok bool) {
	isSlash := func(c byte) bool { return c == '/' || c == '\\' }
	// Browsers strip leading C0 controls and spaces before parsing.
	i := len(raw) - len(strings.TrimLeftFunc(raw, func(r rune) bool { return r <= ' ' }))
	delimiters := "/?#"
	if colon := schemeEnd(raw[i:]); colon > 0 {
		scheme := strings.ToLower(raw[i : i+colon])
		i += colon + 1
		if specialSchemes[scheme] {
			delimiters = "/\\?#"
		} else if !strings.HasPrefix(raw[i:], "//") {
			return 0, 0, false // opaque, such as mailto:ops@example.com
		}
	} else if len(raw)-i < 2 || !isSlash(raw[i]) || !isSlash(raw[i+1]) {
		return 0, 0, false // a path, query or fragment: no authority
	}
	for i < len(raw) && isSlash(raw[i]) {
		i++
	}
	authority := raw[i:]
	if stop := strings.IndexAny(authority, delimiters); stop >= 0 {
		authority = authority[:stop]
	}
	at := strings.LastIndexByte(authority, '@')
	if at < 0 {
		return 0, 0, false
	}
	return i, i + at + 1, true
}

// schemeEnd returns the index of the ':' that ends raw's URL scheme, or -1
// when raw does not start with a scheme.
func schemeEnd(raw string) int {
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z':
		case '0' <= c && c <= '9', c == '+', c == '-', c == '.':
			if i == 0 {
				return -1
			}
		case c == ':':
			if i == 0 {
				return -1
			}
			return i
		default:
			return -1
		}
	}
	return -1
}

// hasUserinfo reports whether raw is a URL or URL template with userinfo.
func hasUserinfo(raw string) bool {
	_, _, ok := userinfoSpan(removeTabsAndNewlines(raw))
	return ok
}

// stripUserinfo removes userinfo from raw and leaves everything else,
// including template placeholders and encoding, unchanged apart from the
// tabs and newlines browsers ignore.
func stripUserinfo(raw string) string {
	raw = removeTabsAndNewlines(raw)
	start, end, ok := userinfoSpan(raw)
	if !ok {
		return raw
	}
	return raw[:start] + raw[end:]
}

// removeTabsAndNewlines drops ASCII tab, CR and LF anywhere in raw, as the
// WHATWG URL parser does before parsing, so "ht\ttp://u:p@h" is scanned as
// the browser sees it.
func removeTabsAndNewlines(raw string) string {
	if !strings.ContainsAny(raw, "\t\r\n") {
		return raw
	}
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, raw)
}

// bareUserinfoEnd returns the index just past the '@' of a scheme-less
// "user:password@host" value, or -1. Such a value is not a URL with an
// authority, but tools like curl read it as credentials for http://host.
// "mailto:" is the one opaque scheme a target_ref legitimately uses with an
// '@', so it is exempt; a bare address such as ops@example.com has no ':' and
// never matches.
func bareUserinfoEnd(raw string) int {
	raw = removeTabsAndNewlines(raw)
	head := strings.TrimLeftFunc(raw, func(r rune) bool { return r <= ' ' })
	offset := len(raw) - len(head)
	if stop := strings.IndexAny(head, "/\\?#"); stop >= 0 {
		head = head[:stop]
	}
	at := strings.LastIndexByte(head, '@')
	if at < 0 {
		return -1
	}
	user, _, hasColon := strings.Cut(head[:at], ":")
	if !hasColon || strings.EqualFold(user, "mailto") {
		return -1
	}
	return offset + at + 1
}

// targetRefCredentials reports whether a notification channel target_ref
// carries credentials, as URL userinfo or as a bare user:password@host.
func targetRefCredentials(ref string) bool {
	return hasUserinfo(ref) || bareUserinfoEnd(ref) >= 0
}

// redactTargetRef strips credentials from a notification channel target_ref.
func redactTargetRef(ref string) string {
	ref = stripUserinfo(ref) // also drops tabs and newlines
	if end := bareUserinfoEnd(ref); end >= 0 {
		return ref[end:]
	}
	return ref
}

// validHost reports whether host is safe to join with a port into the
// connection URL: a DNS name or IPv4 address made of letters, digits, '.',
// '-' and '_', or a bare IPv6 literal (net.JoinHostPort adds the brackets, so
// a bracketed value would be double-bracketed). Anything else, such as
// "db:1433", "user@db", "db/instance", "db\instance", whitespace or control
// characters, makes the driver fail to parse the connection URL, and its
// parse error would quote the URL, password included.
func validHost(host string) bool {
	if strings.Contains(host, ":") {
		ip := net.ParseIP(host)
		return ip != nil && ip.To4() == nil
	}
	for _, c := range []byte(host) {
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9', c == '.', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
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
