//go:build chart

package tests

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// PatientZero's chart (tools/patientzero/chart, ADR 0010) takes the caller's
// user, session and client IP from request headers, so its NetworkPolicies
// must deny every other source and admit port 8080 only from the k6 load Job
// that tools/patientzero/scripts/patientzero.sh starts, and its Service must
// stay ClusterIP. Needs helm; run with `make chart-check`.
func TestPatientZeroChart(t *testing.T) {
	root := repoRoot(t)
	const namespace = "patientzero"
	objects := parseObjects(t, helmPatientZero(t, root, namespace))

	var policies []string
	for id, obj := range objects {
		if obj.Kind == "NetworkPolicy" {
			policies = append(policies, id)
		}
	}
	if len(policies) != 2 {
		t.Errorf("NetworkPolicies %q, want only default-deny-ingress and patientzero", policies)
	}

	// Every pod in the namespace, the load Job's included, denies ingress.
	deny := decodeIngressPolicy(t, objects, "NetworkPolicy/default-deny-ingress")
	if len(deny.PodSelector.MatchLabels) > 0 || len(deny.PodSelector.MatchExpressions) > 0 {
		t.Errorf("default-deny-ingress selects %s, want every pod ({})", deny.PodSelector)
	}
	if deny.Ingress == nil || len(*deny.Ingress) > 0 {
		t.Errorf("default-deny-ingress must render ingress: [] to deny all, got %v", deny.Ingress)
	}

	// PatientZero admits its http port from the load Job's pods only.
	pods := podTemplateLabels(t, objects)["Deployment/patientzero"]
	httpPort := patientZeroHTTPPort(t, objects)
	job := loadJobPodLabels(t, root, namespace)
	policy := decodeIngressPolicy(t, objects, "NetworkPolicy/patientzero")
	if !selects(policy.PodSelector.MatchLabels, pods) || len(policy.PodSelector.MatchExpressions) > 0 {
		t.Errorf("patientzero NetworkPolicy selects %s, not PatientZero's pods %v", policy.PodSelector, pods)
	}
	if policy.Ingress == nil || len(*policy.Ingress) != 1 {
		t.Fatalf("patientzero NetworkPolicy ingress %v, want one rule", policy.Ingress)
	}
	rule := (*policy.Ingress)[0]
	if len(rule.Ports) != 1 || rule.Ports[0].Protocol != "TCP" || rule.Ports[0].Port != httpPort {
		t.Errorf("patientzero NetworkPolicy ports %+v, want only TCP %d (the http container port)", rule.Ports, httpPort)
	}
	if rule.From == nil || len(*rule.From) != 1 {
		t.Fatalf("patientzero NetworkPolicy from %v, want one peer (an empty from admits every source)", rule.From)
	}
	peer := (*rule.From)[0]
	if peer.PodSelector == nil || peer.NamespaceSelector != nil || peer.IPBlock != nil {
		t.Fatalf("patientzero NetworkPolicy peer %s, want a podSelector in the namespace only", peerString(peer))
	}
	if !selects(peer.PodSelector.MatchLabels, job) || len(peer.PodSelector.MatchExpressions) > 0 {
		t.Errorf("patientzero NetworkPolicy admits %s, which does not select the load Job's pods %v", peerString(peer), job)
	}
	if selects(peer.PodSelector.MatchLabels, pods) {
		t.Errorf("patientzero NetworkPolicy peer %s also selects PatientZero's own pods", peerString(peer))
	}

	var svc struct {
		Type string `yaml:"type"`
	}
	service := objects["Service/patientzero"]
	if err := service.Spec.Decode(&svc); err != nil {
		t.Fatal(err)
	}
	if svc.Type != "ClusterIP" {
		t.Errorf("Service/patientzero is %q, want ClusterIP: PatientZero is never exposed outside the cluster", svc.Type)
	}
}

// ingressPolicy is the part of a NetworkPolicy spec these checks read.
type ingressPolicy struct {
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

func decodeIngressPolicy(t *testing.T, objects map[string]object, id string) ingressPolicy {
	t.Helper()
	obj, ok := objects[id]
	if !ok {
		t.Fatalf("missing %s", id)
	}
	var spec ingressPolicy
	if err := obj.Spec.Decode(&spec); err != nil {
		t.Fatal(err)
	}
	if strings.Join(spec.PolicyTypes, ",") != "Ingress" {
		t.Errorf("%s policyTypes %v, want [Ingress]", id, spec.PolicyTypes)
	}
	return spec
}

// patientZeroHTTPPort is the container port named http, which the Service
// targets.
func patientZeroHTTPPort(t *testing.T, objects map[string]object) int {
	t.Helper()
	var spec struct {
		Template struct {
			Spec struct {
				Containers []struct {
					Ports []struct {
						Name          string `yaml:"name"`
						ContainerPort int    `yaml:"containerPort"`
					} `yaml:"ports"`
				} `yaml:"containers"`
			} `yaml:"spec"`
		} `yaml:"template"`
	}
	deployment := objects["Deployment/patientzero"]
	if err := deployment.Spec.Decode(&spec); err != nil {
		t.Fatal(err)
	}
	for _, c := range spec.Template.Spec.Containers {
		for _, p := range c.Ports {
			if p.Name == "http" {
				return p.ContainerPort
			}
		}
	}
	t.Fatal("Deployment/patientzero has no container port named http")
	return 0
}

// loadJobPodLabels returns the pod labels of the load Job that patientzero.sh
// applies in start_load, with LOAD_JOB and NAMESPACE as the script sets them;
// the Job must run in PatientZero's namespace, since the policy's peer is a
// podSelector there.
func loadJobPodLabels(t *testing.T, root, namespace string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "tools/patientzero/scripts/patientzero.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	name := regexp.MustCompile(`(?m)^LOAD_JOB=([a-z0-9-]+)$`).FindStringSubmatch(script)
	if name == nil {
		t.Fatal("patientzero.sh sets no LOAD_JOB")
	}
	if !strings.Contains(script, "NAMESPACE=${PATIENTZERO_NAMESPACE:-"+namespace+"}") {
		t.Fatalf("patientzero.sh's NAMESPACE no longer defaults to %s", namespace)
	}
	manifest := regexp.MustCompile(`(?s)\nstart_load\(\) \{\n.*?<<EOF\n(.*?)\nEOF\n`).FindStringSubmatch(script)
	if manifest == nil {
		t.Fatal("patientzero.sh's start_load applies no manifest")
	}
	body := strings.NewReplacer("$LOAD_JOB", name[1], "$NAMESPACE", namespace).Replace(manifest[1])
	body = regexp.MustCompile(`\$\{[^}]*\}|\$[A-Za-z_][A-Za-z0-9_]*`).ReplaceAllString(body, "x")
	var job struct {
		Kind     string `yaml:"kind"`
		Metadata struct {
			Namespace string `yaml:"namespace"`
		} `yaml:"metadata"`
		Spec struct {
			Template struct {
				Metadata struct {
					Labels map[string]string `yaml:"labels"`
				} `yaml:"metadata"`
			} `yaml:"template"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal([]byte(body), &job); err != nil {
		t.Fatalf("start_load's manifest: %v\n%s", err, body)
	}
	if job.Kind != "Job" || job.Metadata.Namespace != namespace || len(job.Spec.Template.Metadata.Labels) == 0 {
		t.Fatalf("start_load applies a %s in %q with pod labels %v, want a labelled Job in %s",
			job.Kind, job.Metadata.Namespace, job.Spec.Template.Metadata.Labels, namespace)
	}
	return job.Spec.Template.Metadata.Labels
}

// helmPatientZero renders PatientZero's chart with the values make
// chart-check uses, against an empty kubeconfig.
func helmPatientZero(t *testing.T, root, namespace string) []byte {
	t.Helper()
	helm := os.Getenv("HELM")
	if helm == "" {
		helm = "helm"
	}
	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(kubeconfig, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(helm, "template", "patientzero", filepath.Join(root, "tools/patientzero/chart"),
		"--namespace", namespace, "--set", "image.tag=ci", "--set", "sql.host=ci")
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfig)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helm template PatientZero's chart: %v\n%s", err, stderr.String())
	}
	return out
}
