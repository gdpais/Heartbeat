.PHONY: help tools-check install-tools kind-up kind-down kind-deploy kind-images kind-status kind-e2e health sqlserver-dev-init sqlserver-dev-up sqlserver-dev-down chart-deps chart-check rules-check test test-integration test-sqlserver test-race vet docs-site

SQLSERVER_DEV_ENV_FILE := .env.sqlserver-dev
# Health checks fail fast on HTTP errors (keeping the body) and never hang.
CURL_CHECK := curl -sS --fail-with-body --connect-timeout 2 --max-time 5 -w '\n'
# Toolchain pins (ADR 0005). The kind node image tracks the newest Kubernetes
# minor Amazon EKS supports; kubectl must stay within one minor of it.
KIND_VERSION := v0.33.0
KIND_NODE_IMAGE := kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed
HELM_MIN_VERSION := v4.2.0
# Exact versions CI installs (make install-tools). Helm 3 only checks that the
# chart still renders with it (ADR 0005).
KUBECTL_VERSION := v1.36.4
HELM_VERSION := v4.2.4
HELM3_VERSION := v3.22.0
TOOLS_DIR ?= .tmp/bin
HELM ?= helm
CHART_DIR := infra/helm/heartbeat
# full: every component. minimal: collector, Prometheus, Alertmanager, Grafana.
PROFILE ?= full
# promtool comes from the same image as the Prometheus server.
PROMETHEUS_IMAGE := prom/prometheus:v3.15.0
# The HTML docs link source files (Go, YAML, SQL, ...) to this repository URL.
DOCS_REPO_URL := https://github.com/gdpais/Heartbeat
DOCS_REPO_REF := master
# The generator is its own Go module (tools/docsite/go.mod), so the services'
# module does not depend on its Markdown parser.
DOCSITE_MODULE := tools/docsite
DOCSITE := GOCACHE=$$(pwd)/.tmp/gocache go -C $(DOCSITE_MODULE) run ./cmd/docsite -root ../.. -repo-url $(DOCS_REPO_URL) -ref $(DOCS_REPO_REF)

help:
	@printf '%s\n' \
		'Heartbeat developer commands:' \
		'  make tools-check         Verify docker, kind, kubectl, helm and go against the pinned versions' \
		'  make install-tools       Install the pinned kind, kubectl, helm and helm3 into $$TOOLS_DIR (CI)' \
		'  make kind-up             Create the kind cluster, build and load images, deploy the chart (PROFILE=full|minimal)' \
		'  make kind-deploy         Redeploy the chart to the existing kind cluster (PROFILE=full|minimal)' \
		'  make kind-images         Rebuild both images and load them into kind' \
		'  make kind-status         Show pods and services in the kind cluster' \
		'  make kind-down           Delete the kind cluster' \
		'  make health              Check the collector, Prometheus, Alertmanager and Grafana on their local ports' \
		'  make sqlserver-dev-init  Create the ignored .env.sqlserver-dev with random SA and collector passwords' \
		'  make sqlserver-dev-up    Start a dev-only SQL Server container and point the collector at it' \
		'  make sqlserver-dev-down  Remove the dev SQL Server container and its data; redeploy without it' \
		'  make kind-e2e            Run the acceptance checks on a separate, temporary kind cluster' \
		'  make chart-deps          Fetch the chart dependencies listed in Chart.lock' \
		'  make chart-check         Lint and render the chart for every values profile (needs chart-deps)' \
		'  make rules-check         Validate Prometheus rules and run their promtool unit tests (requires Docker)' \
		'  make test                Run all Go tests without Docker integration tests' \
		'  make test-integration    Run isolated PostgreSQL integration tests (requires Docker)' \
		'  make test-sqlserver      Run every SQL Server probe against a disposable SQL Server container (requires Docker)' \
		'  make test-race           Run all Docker-free tests with the race detector' \
		'  make vet                 Run Go static checks' \
		'  make docs-site           Build the HTML docs site into docs/site (not committed; CI publishes it)'

tools-check:
	@KIND_VERSION=$(KIND_VERSION) KIND_NODE_IMAGE=$(KIND_NODE_IMAGE) HELM_MIN_VERSION=$(HELM_MIN_VERSION) scripts/tools-check.sh

install-tools:
	@KIND_VERSION=$(KIND_VERSION) KUBECTL_VERSION=$(KUBECTL_VERSION) HELM_VERSION=$(HELM_VERSION) HELM3_VERSION=$(HELM3_VERSION) scripts/install-tools.sh $(TOOLS_DIR)

kind-up:
	@KIND_NODE_IMAGE='$(KIND_NODE_IMAGE)' PROFILE=$(PROFILE) scripts/kind.sh up

kind-deploy:
	@PROFILE=$(PROFILE) scripts/kind.sh deploy

kind-images:
	@scripts/kind.sh images

kind-status:
	@scripts/kind.sh status

kind-down:
	@scripts/kind.sh down

# kind maps these NodePorts to the same 127.0.0.1 ports Compose used.
health:
	$(CURL_CHECK) http://localhost:8082/healthz
	$(CURL_CHECK) http://localhost:8082/readyz
	$(CURL_CHECK) http://localhost:9090/-/ready
	$(CURL_CHECK) http://localhost:9093/-/ready
	$(CURL_CHECK) http://localhost:3000/api/health

# Generates random per-machine passwords instead of copying shared ones: sa
# for container setup, and the collector's least-privilege login
# (scripts/sqlserver-login.sh). A file from before the dedicated login, whose
# collector credential is sa, gets one added. The "Dev-" prefix plus hex
# digits satisfies SQL Server password complexity.
sqlserver-dev-init:
	@set -eu; umask 077; file=$(SQLSERVER_DEV_ENV_FILE); key=HEARTBEAT_CREDENTIAL_ENV_SQLSERVER_DEV; \
	if [ ! -f "$$file" ]; then \
		printf 'MSSQL_SA_PASSWORD=Dev-%s\n' "$$(openssl rand -hex 16)" > "$$file"; \
		echo "Created $$file with a random SA password"; \
	fi; \
	if ! grep -q "^$$key=" "$$file" || grep -qi "^$$key=sa:" "$$file"; then \
		{ grep -v "^$$key=" "$$file" || true; printf '%s=heartbeat_collector:Dev-%s\n' "$$key" "$$(openssl rand -hex 16)"; } > "$$file.tmp"; \
		mv "$$file.tmp" "$$file"; \
		echo "Added a least-privilege collector login to $$file"; \
	fi
	chmod 600 $(SQLSERVER_DEV_ENV_FILE)

sqlserver-dev-up:
	@KIND_NODE_IMAGE='$(KIND_NODE_IMAGE)' PROFILE=$(PROFILE) SQLSERVER_ENV_FILE=$(SQLSERVER_DEV_ENV_FILE) scripts/kind.sh sqlserver-up

sqlserver-dev-down:
	@PROFILE=$(PROFILE) scripts/kind.sh sqlserver-down

kind-e2e:
	@KIND_NODE_IMAGE='$(KIND_NODE_IMAGE)' scripts/kind-e2e.sh

chart-deps:
	scripts/chart-deps.sh

# HELM=<path> repeats the checks with another Helm binary (CI uses Helm 3).
chart-check:
	$(HELM) lint --strict $(CHART_DIR) -f infra/helm/values/kind.yaml
	HELM=$(HELM) GOCACHE=$$(pwd)/.tmp/gocache go test -tags=chart -count=1 -run TestChartProfiles ./tests

rules-check:
	docker run --rm -v "$$(pwd)/infra/helm/heartbeat/files/prometheus/rules:/rules:ro" --entrypoint promtool $(PROMETHEUS_IMAGE) check rules /rules/heartbeat.rules.yml /rules/generated/heartbeat-rendered.rules.yml
	docker run --rm -v "$$(pwd)/infra/helm/heartbeat/files/prometheus/rules:/rules:ro" -w /rules/tests --entrypoint promtool $(PROMETHEUS_IMAGE) test rules heartbeat.rules.test.yml

test:
	GOCACHE=$$(pwd)/.tmp/gocache go test ./...
	GOCACHE=$$(pwd)/.tmp/gocache go -C $(DOCSITE_MODULE) test ./...

test-integration:
	GOCACHE=$$(pwd)/.tmp/gocache go test -tags=integration -count=1 -timeout=10m ./tests

test-sqlserver:
	scripts/sqlserver-test.sh

test-race:
	GOCACHE=$$(pwd)/.tmp/gocache go test -race ./...
	GOCACHE=$$(pwd)/.tmp/gocache go -C $(DOCSITE_MODULE) test -race ./...

vet:
	GOCACHE=$$(pwd)/.tmp/gocache go vet ./...
	GOCACHE=$$(pwd)/.tmp/gocache go -C $(DOCSITE_MODULE) vet ./...

docs-site:
	$(DOCSITE)
