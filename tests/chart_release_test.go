package tests

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The chart's image defaults must name what the release workflow publishes:
// v0.1.0 defaulted to heartbeat/db-collector, which resolves to Docker Hub,
// so `helm install` of the published chart could never pull its own image.
// The tag defaults to appVersion, which must be the release version the
// workflow tags the images with.

// releaseImageValues maps each image the release workflow builds (its images
// matrix) to the chart values key whose image deploys it.
var releaseImageValues = map[string]string{
	"db-collector": "dbCollector",
}

func TestChartDefaultsToReleasedArtifacts(t *testing.T) {
	root := repoRoot(t)

	var chart struct {
		Name       string `yaml:"name"`
		Version    string `yaml:"version"`
		AppVersion string `yaml:"appVersion"`
		Home       string `yaml:"home"`
	}
	readYAML(t, filepath.Join(root, "infra/helm/heartbeat/Chart.yaml"), &chart)
	if chart.AppVersion != chart.Version {
		t.Errorf("Chart.yaml appVersion %q differs from version %q; images are published with the release version, so the default tag would not exist", chart.AppVersion, chart.Version)
	}
	// The workflow publishes under the repository owner's namespace; the
	// chart's home names the canonical repository.
	home, err := url.Parse(chart.Home)
	if err != nil || home.Host != "github.com" {
		t.Fatalf("Chart.yaml home %q is not a github.com repository URL", chart.Home)
	}
	owner, _, _ := strings.Cut(strings.TrimPrefix(home.Path, "/"), "/")

	var workflow struct {
		Env  map[string]string `yaml:"env"`
		Jobs struct {
			Images struct {
				Env      map[string]string `yaml:"env"`
				Strategy struct {
					Matrix struct {
						Service []string `yaml:"service"`
					} `yaml:"matrix"`
				} `yaml:"strategy"`
			} `yaml:"images"`
		} `yaml:"jobs"`
	}
	readYAML(t, filepath.Join(root, ".github/workflows/release.yml"), &workflow)
	expand := func(s string) string {
		return strings.ReplaceAll(s, "${{ github.repository_owner }}", owner)
	}
	imagePrefix := expand(workflow.Env["IMAGE_PREFIX"])
	if imagePrefix != "ghcr.io/"+owner+"/heartbeat" {
		t.Errorf("release.yml IMAGE_PREFIX is %q; update this test and the chart's image defaults together", imagePrefix)
	}
	services := workflow.Jobs.Images.Strategy.Matrix.Service
	if len(services) == 0 {
		t.Fatal("release.yml jobs.images.strategy.matrix.service is empty; the test is not reading the workflow")
	}

	var values map[string]any
	readYAML(t, filepath.Join(root, "infra/helm/heartbeat/values.yaml"), &values)
	for _, service := range services {
		ref := strings.ReplaceAll(expand(workflow.Jobs.Images.Env["REF"]), "${{ matrix.service }}", service)
		repository := imagePrefix + "/" + service
		if !strings.HasPrefix(ref, repository+":") {
			t.Errorf("release.yml pushes %s as %q, not under IMAGE_PREFIX (%s)", service, ref, repository)
		}
		key, ok := releaseImageValues[service]
		if !ok {
			t.Errorf("release.yml builds %s, which no chart image deploys; add its values key to releaseImageValues", service)
			continue
		}
		image, _ := dig(values, key, "image").(map[string]any)
		if got := image["repository"]; got != repository {
			t.Errorf("values.yaml %s.image.repository is %v, want %s (where release.yml publishes it)", key, got, repository)
		}
		for _, field := range []string{"tag", "digest"} {
			if got := image[field]; got != "" {
				t.Errorf("values.yaml %s.image.%s is %v; leave it empty so the tag defaults to appVersion", key, field, got)
			}
		}
	}
}

func readYAML(t *testing.T, path string, out any) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(content, out); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}

// dig returns the value at a path of map keys, or nil.
func dig(v any, keys ...string) any {
	for _, key := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[key]
	}
	return v
}
