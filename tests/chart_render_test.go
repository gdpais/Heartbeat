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
	// Objects (kind/name) the profile must and must not render.
	present, absent []string
}

var chartProfiles = []chartProfile{
	{
		name:    "defaults",
		present: []string{"StatefulSet/db-collector", "Deployment/otel-gateway", "Deployment/prometheus", "StatefulSet/alertmanager", "Deployment/grafana", "StatefulSet/loki", "Deployment/otel-collector"},
	},
	{
		name:    "kind",
		values:  []string{"kind.yaml"},
		present: []string{"StatefulSet/db-collector", "Deployment/otel-gateway", "StatefulSet/loki", "Deployment/otel-collector"},
	},
	{
		name:    "kind-minimal",
		values:  []string{"kind.yaml", "minimal.yaml"},
		present: []string{"StatefulSet/db-collector", "Deployment/prometheus", "StatefulSet/alertmanager", "Deployment/grafana"},
		absent:  []string{"Deployment/otel-gateway", "StatefulSet/loki", "Deployment/otel-collector"},
	},
	{
		name:    "kind-sqlserver-dev",
		values:  []string{"kind.yaml", "sqlserver-dev.yaml"},
		present: []string{"StatefulSet/db-collector"},
	},
	{
		name:    "production-example",
		values:  []string{"production.example.yaml"},
		present: []string{"StatefulSet/db-collector", "Ingress/grafana"},
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
