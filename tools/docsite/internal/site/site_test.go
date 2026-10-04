package site

import (
	"bytes"
	"html"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const testRepoURL = "https://example.test/heartbeat"

func buildRepoSite(t *testing.T) (Config, map[string][]byte) {
	t.Helper()
	cfg := Config{Root: repoRoot(t), OutDir: "docs/site", RepoURL: testRepoURL, Ref: "main"}
	files, err := Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, files
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// The generator is a nested module, so look for the docs index rather
	// than the nearest go.mod.
	for {
		if _, err := os.Stat(filepath.Join(dir, "docs", "README.md")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repository root (docs/README.md) not found")
		}
		dir = parent
	}
}

var (
	hrefPattern = regexp.MustCompile(`\s(?:href|src)="([^"]*)"`)
	idPattern   = regexp.MustCompile(`\sid="([^"]*)"`)
)

// TestSiteLinksResolve checks every link in the generated site: links
// between pages must reach an existing page and, when they carry one, an
// existing anchor; links to repository files must name a file or directory
// that exists.
func TestSiteLinksResolve(t *testing.T) {
	cfg, files := buildRepoSite(t)
	ids := map[string]map[string]bool{}
	for name, content := range files {
		if strings.HasSuffix(name, ".html") {
			ids[name] = map[string]bool{}
			for _, m := range idPattern.FindAllSubmatch(content, -1) {
				ids[name][html.UnescapeString(string(m[1]))] = true
			}
		}
	}
	checked := 0
	for name, content := range files {
		if !strings.HasSuffix(name, ".html") {
			continue
		}
		for _, m := range hrefPattern.FindAllSubmatch(content, -1) {
			raw := html.UnescapeString(string(m[1]))
			checked++
			if strings.HasPrefix(raw, cfg.RepoURL+"/") {
				checkRepoLink(t, cfg, name, raw)
				continue
			}
			if raw == MermaidURL || hasScheme(raw) || strings.HasPrefix(raw, "//") {
				continue
			}
			target, fragment, _ := strings.Cut(raw, "#")
			target, _, _ = strings.Cut(target, "?")
			if target == "" {
				target = name
			} else {
				target = path.Clean(path.Join(path.Dir(name), target))
			}
			if _, ok := files[target]; !ok {
				t.Errorf("%s: link %q: no page %s in the site", name, raw, target)
				continue
			}
			if fragment == "" {
				continue
			}
			anchor, err := url.PathUnescape(fragment)
			if err != nil {
				t.Errorf("%s: link %q: %v", name, raw, err)
			} else if !ids[target][anchor] {
				t.Errorf("%s: link %q: no id %q in %s", name, raw, anchor, target)
			}
		}
	}
	if checked < 100 {
		t.Fatalf("only %d links checked; link extraction is probably broken", checked)
	}
}

func checkRepoLink(t *testing.T, cfg Config, page, link string) {
	t.Helper()
	rest := strings.TrimPrefix(link, cfg.RepoURL+"/")
	rest, _, _ = strings.Cut(rest, "#")
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) != 3 || (parts[0] != "blob" && parts[0] != "tree") || parts[1] != cfg.Ref {
		t.Errorf("%s: malformed repository link %q", page, link)
		return
	}
	info, err := os.Stat(filepath.Join(cfg.Root, filepath.FromSlash(parts[2])))
	if err != nil {
		t.Errorf("%s: repository link %q: %v", page, link, err)
		return
	}
	if info.IsDir() != (parts[0] == "tree") {
		t.Errorf("%s: repository link %q uses the wrong kind for %s", page, link, parts[2])
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	_, first := buildRepoSite(t)
	_, second := buildRepoSite(t)
	if len(first) != len(second) {
		t.Fatalf("builds produced %d and %d files", len(first), len(second))
	}
	for name, content := range first {
		if !bytes.Equal(content, second[name]) {
			t.Errorf("%s differs between builds", name)
		}
	}
}

func TestSiteContents(t *testing.T) {
	_, files := buildRepoSite(t)
	for _, want := range []string{
		"index.html",
		"architecture/overview.html",
		"architecture/decisions/index.html",
		"reference/configuration.html",
		"archive/index.html",
		"services/db-collector/index.html",
		"services/otel-gateway/index.html",
	} {
		if files[want] == nil {
			t.Errorf("missing %s", want)
		}
	}
	for name := range files {
		if strings.HasPrefix(name, "plans/") || strings.HasPrefix(name, "reviews/") {
			t.Errorf("%s: docs/plans and docs/reviews must not be published", name)
		}
	}

	archived := string(files["archive/original-plan.html"])
	if !strings.Contains(archived, `class="archived-banner"`) {
		t.Error("archived pages must carry the archived banner")
	}
	for name, content := range files {
		if strings.HasSuffix(name, ".html") && !strings.HasPrefix(name, "archive/") &&
			bytes.Contains(content, []byte(`href="archive/original-plan.html"`)) {
			t.Errorf("%s: archived pages must stay out of the sidebar", name)
		}
	}

	home := string(files["index.html"])
	for _, group := range []string{"New to Heartbeat", "Developers", "Operators and SREs"} {
		if !strings.Contains(home, "<h2>"+group+"</h2>") {
			t.Errorf("sidebar is missing the %q audience group", group)
		}
	}
	if !strings.Contains(string(files["reference/configuration.html"]), `<div class="table-wrap"><table>`) {
		t.Error("tables must be wrapped in a scroll container")
	}
}

// Mermaid diagrams (the data model's ER diagrams) must reach mermaid.js
// exactly as written in the Markdown.
func TestDiagramSourcesUnchanged(t *testing.T) {
	cfg, files := buildRepoSite(t)
	source, err := os.ReadFile(filepath.Join(cfg.Root, "docs/architecture/data-model.md"))
	if err != nil {
		t.Fatal(err)
	}
	fences := regexp.MustCompile("(?s)```mermaid\n(.*?)```").FindAllSubmatch(source, -1)
	rendered := regexp.MustCompile(`(?s)<pre class="diagram-source"><code>(.*?)</code></pre>`).FindAllSubmatch(files["architecture/data-model.html"], -1)
	if len(fences) == 0 || len(fences) != len(rendered) {
		t.Fatalf("found %d mermaid blocks in Markdown and %d in HTML", len(fences), len(rendered))
	}
	for i := range fences {
		if got := html.UnescapeString(string(rendered[i][1])); got != string(fences[i][1]) {
			t.Errorf("diagram %d changed between Markdown and HTML", i+1)
		}
	}
	if !bytes.Contains(files["architecture/data-model.html"], []byte(MermaidURL)) {
		t.Error("pages with diagrams must load mermaid.js")
	}
	if bytes.Contains(files["reference/configuration.html"], []byte(MermaidURL)) {
		t.Error("pages without diagrams must not load mermaid.js")
	}
}

// The architecture overview embeds its diagrams as SVG files, which the
// site must carry byte for byte.
func TestImagesCopied(t *testing.T) {
	cfg, files := buildRepoSite(t)
	overview := string(files["architecture/overview.html"])
	for _, name := range []string{"current-runtime.svg", "target-platform.svg"} {
		if !strings.Contains(overview, `src="diagrams/`+name+`"`) {
			t.Errorf("overview does not embed diagrams/%s", name)
		}
		want, err := os.ReadFile(filepath.Join(cfg.Root, "docs/architecture/diagrams", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(files["architecture/diagrams/"+name], want) {
			t.Errorf("architecture/diagrams/%s is missing or differs from the repository file", name)
		}
	}
}

func TestSlugMatchesGitHub(t *testing.T) {
	s := newSlugger()
	for _, tc := range []struct{ in, want string }{
		{"Core systems", "core-systems"},
		{"`integrations.yaml`", "integrationsyaml"},
		{"DB collector (`:8082`)", "db-collector-8082"},
		{"Prometheus → Alertmanager", "prometheus--alertmanager"},
		{"A. API / control-plane service (`apps/api`)", "a-api--control-plane-service-appsapi"},
		{"snake_case & Co.", "snake_case--co"},
		{"Core systems", "core-systems-1"},
		{"Core systems", "core-systems-2"},
		{"Core systems 1", "core-systems-1-1"},
	} {
		if got := s.slug(tc.in); got != tc.want {
			t.Errorf("slug(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestOutPath(t *testing.T) {
	for src, want := range map[string]string{
		"docs/README.md":                        "index.html",
		"docs/product/overview.md":              "product/overview.html",
		"docs/architecture/decisions/README.md": "architecture/decisions/index.html",
		"services/db-collector/README.md":       "services/db-collector/index.html",
	} {
		if got := outPath(src); got != want {
			t.Errorf("outPath(%q) = %q, want %q", src, got, want)
		}
	}
}

func TestWriteRemovesStaleFiles(t *testing.T) {
	root := t.TempDir()
	cfg := Config{Root: root, OutDir: "site"}
	if err := Write(cfg, map[string][]byte{"index.html": []byte("old"), "a/b.html": []byte("b")}); err != nil {
		t.Fatal(err)
	}
	if err := Write(cfg, map[string][]byte{"index.html": []byte("new"), "c.html": []byte("c")}); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"index.html": "new", "c.html": "c"} {
		got, err := os.ReadFile(filepath.Join(root, "site", name))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q (%v), want %q", name, got, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "site", "a")); !os.IsNotExist(err) {
		t.Error("Write must remove files and directories the build no longer produces")
	}
}
