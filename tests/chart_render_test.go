//go:build chart

package tests

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"heartbeat/internal/config"
)

// Renders the Heartbeat chart for every supported values combination and
// checks the rules from ADR 0003 and ADR 0005. Needs helm and the chart
// dependencies (make chart-deps); run with `make chart-check`. HELM selects
// the binary, so CI can repeat the run with Helm 3.

type chartProfile struct {
	name   string
	values []string
	// set holds --set overrides applied after the values files.
	set []string
	// inline is a values document applied last.
	inline string
	// Objects (kind/name) the profile must and must not render.
	present, absent []string
	// collectorPeers are the sources the db-collector NetworkPolicy must
	// admit to port 8082, in peerString form; none means it denies all
	// ingress. Checked whenever the profile renders the policy.
	collectorPeers []string
	// collectorReceivers are the OTel Collector's receivers; none means only
	// otlp.
	collectorReceivers []string
	// stateEmptyDir means the collector's state is an emptyDir instead of the
	// db-collector-state claim (dbCollector.persistence.enabled=false).
	stateEmptyDir bool
	// goMemLimit is the collector's GOMEMLIMIT; empty means 204MiB, 80% of
	// the default 256Mi limit, and "-" means none.
	goMemLimit string
	// wantError, when set, is part of the error helm must fail with; nothing
	// else is checked.
	wantError string
}

// bundledPrometheusPeer is the bundled Prometheus server as a NetworkPolicy
// peer of the release "heartbeat".
const bundledPrometheusPeer = "pods{app.kubernetes.io/component=server,app.kubernetes.io/instance=heartbeat,app.kubernetes.io/name=prometheus}"

var chartProfiles = []chartProfile{
	{
		name:           "defaults",
		present:        []string{"StatefulSet/db-collector", "Deployment/prometheus", "StatefulSet/alertmanager", "Deployment/grafana", "StatefulSet/loki", "Deployment/otel-collector", "NetworkPolicy/db-collector"},
		collectorPeers: []string{bundledPrometheusPeer},
	},
	{
		name:    "kind",
		values:  []string{"kind.yaml"},
		present: []string{"StatefulSet/db-collector", "StatefulSet/loki", "Deployment/otel-collector", "NetworkPolicy/db-collector"},
		// The host through the loopback NodePort; pods stay limited to Prometheus.
		collectorPeers: []string{bundledPrometheusPeer, "ip 0.0.0.0/0 except 10.244.0.0/16"},
	},
	{
		name:           "kind-minimal",
		values:         []string{"kind.yaml", "minimal.yaml"},
		present:        []string{"StatefulSet/db-collector", "Deployment/prometheus", "StatefulSet/alertmanager", "Deployment/grafana", "NetworkPolicy/db-collector"},
		absent:         []string{"StatefulSet/loki", "Deployment/otel-collector"},
		collectorPeers: []string{bundledPrometheusPeer, "ip 0.0.0.0/0 except 10.244.0.0/16"},
	},
	{
		name:           "kind-sqlserver-dev",
		values:         []string{"kind.yaml", "sqlserver-dev.yaml"},
		present:        []string{"StatefulSet/db-collector", "NetworkPolicy/db-collector"},
		collectorPeers: []string{bundledPrometheusPeer, "ip 0.0.0.0/0 except 10.244.0.0/16"},
	},
	{
		name:           "production-example",
		values:         []string{"production.example.yaml"},
		present:        []string{"StatefulSet/db-collector", "Ingress/grafana", "NetworkPolicy/db-collector"},
		collectorPeers: []string{bundledPrometheusPeer, "ip 10.100.0.0/16"},
	},
	{
		// A shared Prometheus in another namespace replaces the bundled one.
		name: "external-prometheus",
		set: []string{
			"prometheus.enabled=false", "grafana.enabled=false",
			`dbCollector.networkPolicy.scrapers[0].namespaceSelector.matchLabels.kubernetes\.io/metadata\.name=monitoring`,
			`dbCollector.networkPolicy.scrapers[0].podSelector.matchLabels.app\.kubernetes\.io/name=prometheus`,
		},
		present:        []string{"StatefulSet/db-collector", "NetworkPolicy/db-collector"},
		absent:         []string{"Deployment/prometheus", "Deployment/grafana"},
		collectorPeers: []string{"ns{kubernetes.io/metadata.name=monitoring} pods{app.kubernetes.io/name=prometheus}"},
	},
	{
		// Another observability platform (ADR 0011): no bundled engines or
		// content; the Collector scrapes the collector and sends logs and
		// metrics over OTLP.
		name:               "external-platform-example",
		values:             []string{"external-platform.example.yaml"},
		present:            []string{"StatefulSet/db-collector", "Deployment/otel-collector", "NetworkPolicy/db-collector"},
		absent:             []string{"Deployment/prometheus", "StatefulSet/alertmanager", "Deployment/grafana", "StatefulSet/loki", "ConfigMap/heartbeat-prometheus-rules", "ConfigMap/heartbeat-dashboards"},
		collectorPeers:     []string{"pods{app.kubernetes.io/name=otelCollector,component=standalone-collector}"},
		collectorReceivers: []string{"otlp", "prometheus"},
	},
	{
		// No peer at all must deny all ingress, never render an empty `from`
		// (which would admit every source).
		name:    "collector-policy-without-peers",
		set:     []string{"prometheus.enabled=false", "grafana.enabled=false"},
		present: []string{"StatefulSet/db-collector", "NetworkPolicy/db-collector"},
	},
	{
		// The bundled peer follows the subchart's name, like its selector.
		name:           "prometheus-name-override",
		set:            []string{"prometheus.nameOverride=prom"},
		present:        []string{"Deployment/prometheus", "NetworkPolicy/db-collector"},
		collectorPeers: []string{"pods{app.kubernetes.io/component=server,app.kubernetes.io/instance=heartbeat,app.kubernetes.io/name=prom}"},
	},
	{
		// Only an empty podSelector admits every pod in the namespace.
		name:      "collector-peer-empty-pod-selector",
		inline:    "dbCollector: {networkPolicy: {operators: [{podSelector: {}}]}}",
		wantError: "values don't meet the specifications of the schema",
	},
	{
		name:      "collector-peer-empty-match-labels",
		inline:    "dbCollector: {networkPolicy: {scrapers: [{namespaceSelector: {matchLabels: {}}}]}}",
		wantError: "values don't meet the specifications of the schema",
	},
	{
		name:    "collector-policy-disabled",
		set:     []string{"dbCollector.networkPolicy.enabled=false"},
		present: []string{"StatefulSet/db-collector"},
		absent:  []string{"NetworkPolicy/db-collector"},
	},
	{
		name:           "collector-state-without-volume",
		set:            []string{"dbCollector.persistence.enabled=false"},
		present:        []string{"StatefulSet/db-collector"},
		collectorPeers: []string{bundledPrometheusPeer},
		stateEmptyDir:  true,
	},
	{
		name:           "collector-memory-limit",
		set:            []string{"dbCollector.resources.limits.memory=1Gi"},
		present:        []string{"StatefulSet/db-collector"},
		collectorPeers: []string{bundledPrometheusPeer},
		goMemLimit:     "819MiB",
	},
	{
		name:           "collector-go-memory-limit-override",
		set:            []string{"dbCollector.goMemLimit=300MiB"},
		present:        []string{"StatefulSet/db-collector"},
		collectorPeers: []string{bundledPrometheusPeer},
		goMemLimit:     "300MiB",
	},
	{
		name:           "collector-without-memory-limit",
		set:            []string{"dbCollector.resources.limits.memory=null"},
		present:        []string{"StatefulSet/db-collector"},
		collectorPeers: []string{bundledPrometheusPeer},
		goMemLimit:     "-",
	},
	{
		name:      "collector-memory-limit-unparsable",
		set:       []string{"dbCollector.resources.limits.memory=256m"},
		wantError: "dbCollector.resources.limits.memory",
	},
	{
		// Go rejects SI units such as M in GOMEMLIMIT.
		name:      "collector-go-memory-limit-invalid",
		set:       []string{"dbCollector.goMemLimit=300M"},
		wantError: "values don't meet the specifications of the schema",
	},
	{
		name:      "collector-state-without-size",
		set:       []string{"dbCollector.persistence.size="},
		wantError: "values don't meet the specifications of the schema",
	},
}

// Cluster-scoped kinds: the chart must install into a namespace it does not
// own, with no cluster-wide side effects.
var clusterScopedKinds = map[string]bool{
	"Namespace": true, "ClusterRole": true, "ClusterRoleBinding": true,
	"CustomResourceDefinition": true, "PersistentVolume": true, "StorageClass": true,
	"PriorityClass": true, "MutatingWebhookConfiguration": true,
	"ValidatingWebhookConfiguration": true, "APIService": true, "IngressClass": true,
}

type object struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name        string            `yaml:"name"`
		Annotations map[string]string `yaml:"annotations"`
	} `yaml:"metadata"`
	Data map[string]string `yaml:"data"`
	Spec yaml.Node         `yaml:"spec"`
}

func (o object) id() string { return o.Kind + "/" + o.Metadata.Name }

func helmTemplate(t *testing.T, profile chartProfile) []byte {
	t.Helper()
	out, stderr, err := runHelmTemplate(t, profile)
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, stderr)
	}
	return out
}

// runHelmTemplate renders profile and returns stdout, stderr and the error.
func runHelmTemplate(t *testing.T, profile chartProfile) ([]byte, string, error) {
	t.Helper()
	root := repoRoot(t)
	helm := os.Getenv("HELM")
	if helm == "" {
		helm = "helm"
	}
	args := []string{"template", "heartbeat", filepath.Join(root, "infra/helm/heartbeat"), "--namespace", "heartbeat"}
	for _, file := range profile.values {
		args = append(args, "-f", filepath.Join(root, "infra/helm/values", file))
	}
	for _, set := range profile.set {
		args = append(args, "--set", set)
	}
	if profile.inline != "" {
		path := filepath.Join(t.TempDir(), "inline.yaml")
		if err := os.WriteFile(path, []byte(profile.inline), 0o600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "-f", path)
	}
	var stderr bytes.Buffer
	cmd := exec.Command(helm, args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		err = fmt.Errorf("%s %s: %w", helm, strings.Join(args, " "), err)
	}
	return out, stderr.String(), err
}

func parseObjects(t *testing.T, rendered []byte) map[string]object {
	t.Helper()
	// An upstream template emits a trailing tab after a scalar; strict YAML
	// parsers reject it, the API server does not.
	rendered = bytes.ReplaceAll(rendered, []byte("\t\n"), []byte("\n"))
	objects := map[string]object{}
	decoder := yaml.NewDecoder(bytes.NewReader(rendered))
	for {
		var obj object
		err := decoder.Decode(&obj)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("parse rendered chart: %v", err)
		}
		if obj.Kind == "" {
			continue
		}
		if _, dup := objects[obj.id()]; dup {
			t.Errorf("%s rendered twice", obj.id())
		}
		objects[obj.id()] = obj
	}
	return objects
}

func TestChartProfiles(t *testing.T) {
	for _, profile := range chartProfiles {
		t.Run(profile.name, func(t *testing.T) {
			if profile.wantError != "" {
				_, stderr, err := runHelmTemplate(t, profile)
				if err == nil || !strings.Contains(stderr, profile.wantError) {
					t.Fatalf("expected helm to fail with %q, got %v\n%s", profile.wantError, err, stderr)
				}
				return
			}
			first := helmTemplate(t, profile)
			// Argo CD renders the chart itself on every sync; output must not
			// change between renders.
			if second := helmTemplate(t, profile); !bytes.Equal(first, second) {
				t.Fatal("rendering is not deterministic")
			}
			objects := parseObjects(t, first)

			for _, id := range profile.present {
				if _, ok := objects[id]; !ok {
					t.Errorf("expected %s", id)
				}
			}
			for _, id := range profile.absent {
				if _, ok := objects[id]; ok {
					t.Errorf("unexpected %s", id)
				}
			}
			for id, obj := range objects {
				if clusterScopedKinds[obj.Kind] {
					t.Errorf("cluster-scoped object %s", id)
				}
				if hook := obj.Metadata.Annotations["helm.sh/hook"]; hook != "" {
					t.Errorf("%s is a Helm hook (%s); Argo CD maps hooks differently", id, hook)
				}
			}

			checkIntegrations(t, objects)
			checkCollectorSingleton(t, objects)
			checkCollectorGoMemLimit(t, objects, profile.goMemLimit)
			checkInClusterEndpoints(t, objects)
			checkCollectorNetworkPolicy(t, objects, profile.collectorPeers)
			checkCollectorState(t, objects, profile.stateEmptyDir)
			checkCollectorPipelines(t, objects, profile.collectorReceivers)
			checkLogIdentifiers(t, objects)
			checkLokiLabelPolicy(t, objects)
		})
	}
}

// The rendered integrations.yaml must load with the same Go loader the
// services use.
func checkIntegrations(t *testing.T, objects map[string]object) {
	t.Helper()
	cm, ok := objects["ConfigMap/heartbeat-integrations"]
	if !ok {
		t.Fatal("missing ConfigMap/heartbeat-integrations")
	}
	path := filepath.Join(t.TempDir(), "integrations.yaml")
	if err := os.WriteFile(path, []byte(cm.Data["integrations.yaml"]), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadRuntimeConfig(path); err != nil {
		t.Errorf("rendered integrations.yaml does not load: %v", err)
	}
}

func checkCollectorSingleton(t *testing.T, objects map[string]object) {
	t.Helper()
	sts, ok := objects["StatefulSet/db-collector"]
	if !ok {
		return
	}
	var spec struct {
		Replicas int `yaml:"replicas"`
		Template struct {
			Spec struct {
				Containers []struct {
					Image string `yaml:"image"`
				} `yaml:"containers"`
			} `yaml:"spec"`
		} `yaml:"template"`
	}
	if err := sts.Spec.Decode(&spec); err != nil {
		t.Fatal(err)
	}
	if spec.Replicas != 1 {
		t.Errorf("db-collector replicas = %d, want 1 (singleton, ADR 0003)", spec.Replicas)
	}
	if image := spec.Template.Spec.Containers[0].Image; strings.HasSuffix(image, ":") {
		t.Errorf("db-collector image %q has no tag", image)
	}
}

// The collector's GOMEMLIMIT keeps the Go heap below the container's memory
// limit, so a large outsystems_logs backlog is collected instead of
// OOM-killing the pod, which would read the same batch again on restart.
func checkCollectorGoMemLimit(t *testing.T, objects map[string]object, want string) {
	t.Helper()
	sts, ok := objects["StatefulSet/db-collector"]
	if !ok {
		return
	}
	var spec struct {
		Template struct {
			Spec struct {
				Containers []struct {
					Env []struct {
						Name  string `yaml:"name"`
						Value string `yaml:"value"`
					} `yaml:"env"`
				} `yaml:"containers"`
			} `yaml:"spec"`
		} `yaml:"template"`
	}
	if err := sts.Spec.Decode(&spec); err != nil {
		t.Fatal(err)
	}
	if want == "" {
		want = "204MiB"
	}
	got := "-"
	for _, env := range spec.Template.Spec.Containers[0].Env {
		if env.Name == "GOMEMLIMIT" {
			got = env.Value
		}
	}
	if got != want {
		t.Errorf("db-collector GOMEMLIMIT = %q, want %q", got, want)
	}
}

// Every in-cluster address the engines are configured with (Prometheus scrape
// and Alertmanager targets, Alertmanager webhooks, Grafana datasources, the OTel
// Collector's exporters and scrape targets) must be
// a Service the same profile renders. Catches a profile that disables a
// component but still points at it.
func checkInClusterEndpoints(t *testing.T, objects map[string]object) {
	t.Helper()
	services := map[string]bool{}
	for _, obj := range objects {
		if obj.Kind == "Service" {
			services[obj.Metadata.Name] = true
		}
	}
	var hosts []string
	if cm, ok := objects["ConfigMap/prometheus"]; ok {
		var prom struct {
			ScrapeConfigs []struct {
				JobName       string `yaml:"job_name"`
				StaticConfigs []struct {
					Targets []string `yaml:"targets"`
				} `yaml:"static_configs"`
				DNSSDConfigs []struct {
					Names []string `yaml:"names"`
				} `yaml:"dns_sd_configs"`
			} `yaml:"scrape_configs"`
			Alerting struct {
				Alertmanagers []struct {
					StaticConfigs []struct {
						Targets []string `yaml:"targets"`
					} `yaml:"static_configs"`
				} `yaml:"alertmanagers"`
			} `yaml:"alerting"`
		}
		mustUnmarshal(t, cm.Data["prometheus.yml"], &prom)
		if len(prom.Alerting.Alertmanagers) == 0 {
			t.Error("Prometheus has no alerting.alertmanagers")
		}
		for _, job := range prom.ScrapeConfigs {
			for _, sc := range job.StaticConfigs {
				hosts = append(hosts, sc.Targets...)
			}
			for _, sd := range job.DNSSDConfigs {
				hosts = append(hosts, sd.Names...)
			}
		}
		for _, am := range prom.Alerting.Alertmanagers {
			for _, sc := range am.StaticConfigs {
				hosts = append(hosts, sc.Targets...)
			}
		}
	}
	if cm, ok := objects["ConfigMap/alertmanager"]; ok {
		var am struct {
			Receivers []struct {
				WebhookConfigs []struct {
					URL string `yaml:"url"`
				} `yaml:"webhook_configs"`
			} `yaml:"receivers"`
		}
		mustUnmarshal(t, cm.Data["alertmanager.yml"], &am)
		for _, r := range am.Receivers {
			for _, w := range r.WebhookConfigs {
				if w.URL != "" {
					hosts = append(hosts, w.URL)
				}
			}
		}
	}
	if cm, ok := objects["ConfigMap/grafana"]; ok {
		var ds struct {
			Datasources []struct {
				URL string `yaml:"url"`
			} `yaml:"datasources"`
		}
		mustUnmarshal(t, cm.Data["datasources.yaml"], &ds)
		for _, d := range ds.Datasources {
			hosts = append(hosts, d.URL)
		}
	}
	if cm, ok := objects["ConfigMap/otel-collector"]; ok {
		var relay struct {
			Receivers struct {
				Prometheus struct {
					Config struct {
						ScrapeConfigs []struct {
							DNSSDConfigs []struct {
								Names []string `yaml:"names"`
							} `yaml:"dns_sd_configs"`
						} `yaml:"scrape_configs"`
					} `yaml:"config"`
				} `yaml:"prometheus"`
			} `yaml:"receivers"`
			Exporters map[string]struct {
				Endpoint string `yaml:"endpoint"`
			} `yaml:"exporters"`
		}
		mustUnmarshal(t, cm.Data["relay"], &relay)
		for _, job := range relay.Receivers.Prometheus.Config.ScrapeConfigs {
			for _, sd := range job.DNSSDConfigs {
				hosts = append(hosts, sd.Names...)
			}
		}
		for _, exporter := range relay.Exporters {
			// ${env:...} is set from a Secret; the pod's own address is local.
			if strings.HasPrefix(exporter.Endpoint, "http") {
				hosts = append(hosts, exporter.Endpoint)
			}
		}
	}
	for _, raw := range hosts {
		host := hostOf(raw)
		// Dotted names are external or fully qualified; localhost is the pod itself.
		if host == "localhost" || strings.Contains(host, ".") {
			continue
		}
		if !services[host] {
			t.Errorf("configured address %q points at Service %q, which this profile does not render", raw, host)
		}
	}
}

// The OTel Collector runs exactly Heartbeat's config in every profile: only
// the profile's receivers (the OTLP receiver unless it says otherwise), and
// logs and metrics pipelines. Values merged over the
// subchart's defaults once brought back its other receivers and a traces
// pipeline in profiles that did not set otelCollector keys.
func checkCollectorPipelines(t *testing.T, objects map[string]object, receivers []string) {
	t.Helper()
	if receivers == nil {
		receivers = []string{"otlp"}
	}
	cm, ok := objects["ConfigMap/otel-collector"]
	if !ok {
		return
	}
	var relay struct {
		Receivers map[string]any `yaml:"receivers"`
		Service   struct {
			Pipelines map[string]any `yaml:"pipelines"`
		} `yaml:"service"`
	}
	mustUnmarshal(t, cm.Data["relay"], &relay)
	if got := slices.Sorted(maps.Keys(relay.Receivers)); !slices.Equal(got, receivers) {
		t.Errorf("OTel Collector receivers = %v, want %v", got, receivers)
	}
	if got := slices.Sorted(maps.Keys(relay.Service.Pipelines)); !slices.Equal(got, []string{"logs", "metrics"}) {
		t.Errorf("OTel Collector pipelines = %v, want [logs metrics]", got)
	}
}

// logIdentifiers are the log attributes session investigations correlate on.
var logIdentifiers = []string{"user_id", "session_id", "request_id", "client_ip"}

// The OTel Collector must pass the log identifiers through to Loki: no
// processor action may delete or hash them.
func checkLogIdentifiers(t *testing.T, objects map[string]object) {
	t.Helper()
	cm, ok := objects["ConfigMap/otel-collector"]
	if !ok {
		return
	}
	type action struct {
		Key    string `yaml:"key"`
		Action string `yaml:"action"`
	}
	var relay struct {
		Processors map[string]struct {
			Actions    []action `yaml:"actions"`
			Attributes []action `yaml:"attributes"`
		} `yaml:"processors"`
	}
	mustUnmarshal(t, cm.Data["relay"], &relay)
	for name, processor := range relay.Processors {
		for _, a := range append(processor.Actions, processor.Attributes...) {
			if slices.Contains(logIdentifiers, a.Key) && (a.Action == "delete" || a.Action == "hash") {
				t.Errorf("OTel Collector processor %s %ss %s; Loki keeps it as structured metadata, not a label", name, a.Action, a.Key)
			}
		}
	}
}

// Loki indexes only the agreed OTLP resource attributes; everything else,
// including the log identifiers, stays structured metadata.
func checkLokiLabelPolicy(t *testing.T, objects map[string]object) {
	t.Helper()
	cm, ok := objects["ConfigMap/loki"]
	if !ok {
		return
	}
	var config struct {
		LimitsConfig struct {
			AllowStructuredMetadata bool `yaml:"allow_structured_metadata"`
			OTLPConfig              struct {
				ResourceAttributes struct {
					IgnoreDefaults   bool `yaml:"ignore_defaults"`
					AttributesConfig []struct {
						Action     string   `yaml:"action"`
						Attributes []string `yaml:"attributes"`
					} `yaml:"attributes_config"`
				} `yaml:"resource_attributes"`
			} `yaml:"otlp_config"`
		} `yaml:"limits_config"`
	}
	mustUnmarshal(t, cm.Data["config.yaml"], &config)
	limits := config.LimitsConfig
	if !limits.AllowStructuredMetadata {
		t.Error("Loki limits_config.allow_structured_metadata is not true")
	}
	resource := limits.OTLPConfig.ResourceAttributes
	if !resource.IgnoreDefaults {
		t.Error("Loki otlp_config.resource_attributes.ignore_defaults is not true; Loki's default label list would apply")
	}
	var labels []string
	for _, entry := range resource.AttributesConfig {
		if entry.Action == "index_label" {
			labels = append(labels, entry.Attributes...)
		}
	}
	sort.Strings(labels)
	if want := []string{"deployment.environment.name", "outsystems.log_type", "service.name", "service.namespace"}; !reflect.DeepEqual(labels, want) {
		t.Errorf("Loki OTLP index labels = %q, want %q", labels, want)
	}
}

func hostOf(raw string) string {
	if !strings.Contains(raw, "://") {
		raw = "tcp://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return u.Hostname()
}

func mustUnmarshal(t *testing.T, content string, out any) {
	t.Helper()
	if err := yaml.Unmarshal([]byte(content), out); err != nil {
		t.Fatal(fmt.Errorf("parse embedded config: %w", err))
	}
}

// labelSelector is a Kubernetes label selector.
type labelSelector struct {
	MatchLabels      map[string]string `yaml:"matchLabels"`
	MatchExpressions []any             `yaml:"matchExpressions"`
}

// String renders the selector's labels as {k=v,...}, sorted by key.
func (s labelSelector) String() string {
	pairs := make([]string, 0, len(s.MatchLabels))
	for key, value := range s.MatchLabels {
		pairs = append(pairs, key+"="+value)
	}
	sort.Strings(pairs)
	if len(s.MatchExpressions) > 0 {
		pairs = append(pairs, fmt.Sprintf("expressions=%v", s.MatchExpressions))
	}
	return "{" + strings.Join(pairs, ",") + "}"
}

// networkPolicyPeer is one entry of a NetworkPolicy ingress rule's from.
type networkPolicyPeer struct {
	PodSelector       *labelSelector `yaml:"podSelector"`
	NamespaceSelector *labelSelector `yaml:"namespaceSelector"`
	IPBlock           *struct {
		CIDR   string   `yaml:"cidr"`
		Except []string `yaml:"except"`
	} `yaml:"ipBlock"`
}

// peerString renders a peer for comparison: "ns{...} pods{...}" for
// selectors, "ip <cidr> except <cidr>,..." for an ipBlock.
func peerString(peer networkPolicyPeer) string {
	var parts []string
	if peer.NamespaceSelector != nil {
		parts = append(parts, "ns"+peer.NamespaceSelector.String())
	}
	if peer.PodSelector != nil {
		parts = append(parts, "pods"+peer.PodSelector.String())
	}
	if peer.IPBlock != nil {
		block := "ip " + peer.IPBlock.CIDR
		if len(peer.IPBlock.Except) > 0 {
			block += " except " + strings.Join(peer.IPBlock.Except, ",")
		}
		parts = append(parts, block)
	}
	return strings.Join(parts, " ")
}

// The db-collector NetworkPolicy must select exactly the collector pod, admit
// only the expected peers to TCP 8082 and never render an empty `from`
// (which admits every source). The bundled Prometheus peer must match the
// rendered Prometheus Deployment's selector, so a subchart label change cannot
// silently cut off scraping.
func checkCollectorNetworkPolicy(t *testing.T, objects map[string]object, wantPeers []string) {
	t.Helper()
	policy, ok := objects["NetworkPolicy/db-collector"]
	if !ok {
		return
	}
	var spec struct {
		PodSelector labelSelector `yaml:"podSelector"`
		PolicyTypes []string      `yaml:"policyTypes"`
		Ingress     *[]struct {
			Ports []struct {
				Protocol string `yaml:"protocol"`
				Port     int    `yaml:"port"`
			} `yaml:"ports"`
			From *[]networkPolicyPeer `yaml:"from"`
		} `yaml:"ingress"`
	}
	if err := policy.Spec.Decode(&spec); err != nil {
		t.Fatal(err)
	}
	if strings.Join(spec.PolicyTypes, ",") != "Ingress" {
		t.Errorf("db-collector NetworkPolicy policyTypes = %v, want [Ingress]", spec.PolicyTypes)
	}
	if sts, ok := objects["StatefulSet/db-collector"]; ok {
		var stsSpec struct {
			Selector labelSelector `yaml:"selector"`
		}
		if err := sts.Spec.Decode(&stsSpec); err != nil {
			t.Fatal(err)
		}
		if got, want := spec.PodSelector.String(), stsSpec.Selector.String(); got != want {
			t.Errorf("db-collector NetworkPolicy selects %s, StatefulSet selects %s", got, want)
		}
	}
	if spec.Ingress == nil {
		t.Fatal("db-collector NetworkPolicy has no ingress field; render ingress: [] to deny all")
	}
	var gotPeers []string
	for _, rule := range *spec.Ingress {
		if len(rule.Ports) != 1 || rule.Ports[0].Port != 8082 || rule.Ports[0].Protocol != "TCP" {
			t.Errorf("db-collector NetworkPolicy rule ports = %+v, want only TCP 8082", rule.Ports)
		}
		if rule.From == nil || len(*rule.From) == 0 {
			t.Fatal("db-collector NetworkPolicy rule has an empty from, which admits every source")
		}
		for _, peer := range *rule.From {
			gotPeers = append(gotPeers, peerString(peer))
		}
	}
	if !reflect.DeepEqual(gotPeers, wantPeers) {
		t.Errorf("db-collector NetworkPolicy peers = %q, want %q", gotPeers, wantPeers)
	}
	if prom, ok := objects["Deployment/prometheus"]; ok {
		var promSpec struct {
			Selector labelSelector `yaml:"selector"`
		}
		if err := prom.Spec.Decode(&promSpec); err != nil {
			t.Fatal(err)
		}
		if bundled := "pods" + promSpec.Selector.String(); !slices.Contains(gotPeers, bundled) {
			t.Errorf("db-collector NetworkPolicy does not admit the bundled Prometheus (%s); peers %q", bundled, gotPeers)
		}
	}
	checkCollectorSourceAddresses(t, objects, gotPeers)
}

// An ipBlock only sees external clients' addresses when the collector
// Service keeps them: with externalTrafficPolicy Cluster, NodePort and
// LoadBalancer traffic is masqueraded to a node address first (on kind,
// kindnet then drops it).
func checkCollectorSourceAddresses(t *testing.T, objects map[string]object, peers []string) {
	t.Helper()
	svc, ok := objects["Service/db-collector"]
	if !ok || !slices.ContainsFunc(peers, func(peer string) bool { return strings.HasPrefix(peer, "ip ") }) {
		return
	}
	var spec struct {
		Type                  string `yaml:"type"`
		ExternalTrafficPolicy string `yaml:"externalTrafficPolicy"`
	}
	if err := svc.Spec.Decode(&spec); err != nil {
		t.Fatal(err)
	}
	if (spec.Type == "NodePort" || spec.Type == "LoadBalancer") && spec.ExternalTrafficPolicy != "Local" {
		t.Errorf("Service/db-collector is %s with externalTrafficPolicy %q; the NetworkPolicy ipBlock needs Local to see client addresses", spec.Type, spec.ExternalTrafficPolicy)
	}
}

// The collector keeps its outsystems_logs watermarks in a state volume at
// HEARTBEAT_STATE_DIR, the only writable path of its read-only root
// filesystem: the db-collector-state claim, not a volumeClaimTemplate, which
// could never change on an existing StatefulSet.
func checkCollectorState(t *testing.T, objects map[string]object, emptyDir bool) {
	t.Helper()
	sts, ok := objects["StatefulSet/db-collector"]
	if !ok {
		return
	}
	type volume struct {
		Name                  string    `yaml:"name"`
		EmptyDir              *struct{} `yaml:"emptyDir"`
		PersistentVolumeClaim *struct {
			ClaimName string `yaml:"claimName"`
		} `yaml:"persistentVolumeClaim"`
	}
	var spec struct {
		Template struct {
			Spec struct {
				SecurityContext struct {
					FSGroup int `yaml:"fsGroup"`
				} `yaml:"securityContext"`
				Containers []struct {
					Env []struct {
						Name  string `yaml:"name"`
						Value string `yaml:"value"`
					} `yaml:"env"`
					VolumeMounts []struct {
						Name      string `yaml:"name"`
						MountPath string `yaml:"mountPath"`
						ReadOnly  bool   `yaml:"readOnly"`
					} `yaml:"volumeMounts"`
					SecurityContext struct {
						ReadOnlyRootFilesystem bool `yaml:"readOnlyRootFilesystem"`
					} `yaml:"securityContext"`
				} `yaml:"containers"`
				Volumes []volume `yaml:"volumes"`
			} `yaml:"spec"`
		} `yaml:"template"`
		VolumeClaimTemplates []any `yaml:"volumeClaimTemplates"`
	}
	if err := sts.Spec.Decode(&spec); err != nil {
		t.Fatal(err)
	}
	pod := spec.Template.Spec
	container := pod.Containers[0]
	if !container.SecurityContext.ReadOnlyRootFilesystem {
		t.Error("db-collector root filesystem is writable")
	}
	if pod.SecurityContext.FSGroup == 0 {
		t.Error("db-collector pod has no fsGroup; the nonroot user cannot write the state volume")
	}
	var stateDir string
	for _, env := range container.Env {
		if env.Name == "HEARTBEAT_STATE_DIR" {
			stateDir = env.Value
		}
	}
	mounted := false
	for _, mount := range container.VolumeMounts {
		if mount.Name == "state" && mount.MountPath == stateDir && stateDir != "" && !mount.ReadOnly {
			mounted = true
		}
	}
	if !mounted {
		t.Errorf("db-collector does not mount a writable state volume at HEARTBEAT_STATE_DIR (%q)", stateDir)
	}
	if len(spec.VolumeClaimTemplates) > 0 {
		t.Error("db-collector has volumeClaimTemplates; they can never change on an existing StatefulSet")
	}
	hasEmptyDir := slices.ContainsFunc(pod.Volumes, func(v volume) bool { return v.Name == "state" && v.EmptyDir != nil })
	usesClaim := slices.ContainsFunc(pod.Volumes, func(v volume) bool {
		return v.Name == "state" && v.PersistentVolumeClaim != nil && v.PersistentVolumeClaim.ClaimName == "db-collector-state"
	})
	pvc, hasClaim := objects["PersistentVolumeClaim/db-collector-state"]
	switch {
	case emptyDir && (!hasEmptyDir || usesClaim || hasClaim):
		t.Errorf("state: emptyDir %v, claim %v, mounted %v; want an emptyDir only", hasEmptyDir, hasClaim, usesClaim)
	case !emptyDir && (hasEmptyDir || !usesClaim || !hasClaim):
		t.Errorf("state: emptyDir %v, claim %v, mounted %v; want the db-collector-state claim only", hasEmptyDir, hasClaim, usesClaim)
	case !emptyDir:
		var claim struct {
			Resources struct {
				Requests map[string]string `yaml:"requests"`
			} `yaml:"resources"`
		}
		if err := pvc.Spec.Decode(&claim); err != nil {
			t.Fatal(err)
		}
		if size := claim.Resources.Requests["storage"]; size != "256Mi" {
			t.Errorf("state claim requests %s, want 256Mi", size)
		}
	}
}
