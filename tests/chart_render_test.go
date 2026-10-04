//go:build chart

package tests

import (
	"bytes"
	"errors"
	"fmt"
	"io"
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
	// Objects (kind/name) the profile must and must not render.
	present, absent []string
	// collectorPeers are the sources the db-collector NetworkPolicy must
	// admit to port 8082, in peerString form; none means it denies all
	// ingress. Checked whenever the profile renders the policy.
	collectorPeers []string
}

// bundledPrometheusPeer is the bundled Prometheus server as a NetworkPolicy
// peer of the release "heartbeat".
const bundledPrometheusPeer = "pods{app.kubernetes.io/component=server,app.kubernetes.io/instance=heartbeat,app.kubernetes.io/name=prometheus}"

var chartProfiles = []chartProfile{
	{
		name:           "defaults",
		present:        []string{"StatefulSet/db-collector", "Deployment/otel-gateway", "Deployment/prometheus", "StatefulSet/alertmanager", "Deployment/grafana", "StatefulSet/loki", "Deployment/otel-collector", "NetworkPolicy/db-collector"},
		collectorPeers: []string{bundledPrometheusPeer},
	},
	{
		name:    "kind",
		values:  []string{"kind.yaml"},
		present: []string{"StatefulSet/db-collector", "Deployment/otel-gateway", "StatefulSet/loki", "Deployment/otel-collector", "NetworkPolicy/db-collector"},
		// The host through the loopback NodePort; pods stay limited to Prometheus.
		collectorPeers: []string{bundledPrometheusPeer, "ip 0.0.0.0/0 except 10.244.0.0/16"},
	},
	{
		name:           "kind-minimal",
		values:         []string{"kind.yaml", "minimal.yaml"},
		present:        []string{"StatefulSet/db-collector", "Deployment/prometheus", "StatefulSet/alertmanager", "Deployment/grafana", "NetworkPolicy/db-collector"},
		absent:         []string{"Deployment/otel-gateway", "StatefulSet/loki", "Deployment/otel-collector"},
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
			`dbCollector.networkPolicy.prometheus[0].namespaceSelector.matchLabels.kubernetes\.io/metadata\.name=monitoring`,
			`dbCollector.networkPolicy.prometheus[0].podSelector.matchLabels.app\.kubernetes\.io/name=prometheus`,
		},
		present:        []string{"StatefulSet/db-collector", "NetworkPolicy/db-collector"},
		absent:         []string{"Deployment/prometheus", "Deployment/grafana"},
		collectorPeers: []string{"ns{kubernetes.io/metadata.name=monitoring} pods{app.kubernetes.io/name=prometheus}"},
	},
	{
		// No peer at all must deny all ingress, never render an empty `from`
		// (which would admit every source).
		name:    "collector-policy-without-peers",
		set:     []string{"prometheus.enabled=false", "grafana.enabled=false"},
		present: []string{"StatefulSet/db-collector", "NetworkPolicy/db-collector"},
	},
	{
		name:    "collector-policy-disabled",
		set:     []string{"dbCollector.networkPolicy.enabled=false"},
		present: []string{"StatefulSet/db-collector"},
		absent:  []string{"NetworkPolicy/db-collector"},
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
	var stderr bytes.Buffer
	cmd := exec.Command(helm, args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", helm, strings.Join(args, " "), err, stderr.String())
	}
	return out
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
			checkInClusterEndpoints(t, objects)
			checkCollectorNetworkPolicy(t, objects, profile.collectorPeers)
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

// Every in-cluster address the engines are configured with (Prometheus scrape
// and Alertmanager targets, Alertmanager webhooks, Grafana datasources) must be
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
