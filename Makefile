.PHONY: help tools-check install-tools rules-check chart-deps chart-check kind-up kind-down kind-deploy kind-images kind-status kind-e2e up down config migrate health test test-integration test-race vet sqlserver-dev-init sqlserver-dev-up sqlserver-dev-down sqlserver-dev-config sqlserver-dev-health k8s-up k8s-down k8s-apply k8s-port-forward build-db-collector-image kind-load-db-collector-image

COMPOSE_FILE := infra/docker-compose.yml
SQLSERVER_DEV_COMPOSE_FILE := infra/docker-compose.sqlserver-dev.yml
SQLSERVER_DEV_ENV_FILE := .env.sqlserver-dev
K8S_DIR := infra/k8s/local
# Health checks fail fast on HTTP errors (keeping the body) and never hang.
CURL_CHECK := curl -sS --fail-with-body --connect-timeout 2 --max-time 5 -w '\n'
DB_COLLECTOR_IMAGE := heartbeat/db-collector:local
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

help:
	@printf '%s\n' \
		'Heartbeat developer commands:' \
		'  make tools-check         Verify docker, kind, kubectl, helm and go against the pinned versions' \
		'  make install-tools       Install the pinned kind, kubectl, helm and helm3 into $$TOOLS_DIR (CI)' \
		'  make kind-up             Create the kind cluster, build and load images, deploy the chart (PROFILE=full|minimal)' \
		'  make kind-deploy         Redeploy the chart to the existing kind cluster' \
		'  make kind-images         Rebuild both images and load them into kind' \
		'  make kind-status         Show pods and services in the kind cluster' \
		'  make kind-down           Delete the kind cluster' \
		'  make kind-e2e            Run the acceptance checks on a separate, temporary kind cluster' \
		'  make chart-deps          Fetch the chart dependencies listed in Chart.lock' \
		'  make chart-check         Lint and render the chart for every values profile (needs chart-deps)' \
		'  make up                  Start postgres + db-collector with Docker Compose' \
		'  make down                Stop the local Docker Compose stack and remove volumes' \
		'  make config              Validate the Docker Compose file' \
		'  make sqlserver-dev-init  Create ignored local-only SQL Server dev files from examples' \
		'  make sqlserver-dev-up    Start the local stack plus a dev-only SQL Server target' \
		'  make sqlserver-dev-down  Stop the local SQL Server dev overlay and remove volumes' \
		'  make sqlserver-dev-config Validate the SQL Server dev overlay compose config' \
		'  make sqlserver-dev-health Check collector, Prometheus, and Grafana for the SQL Server dev overlay' \
		'  make migrate             Apply the foundation migration into local Postgres' \
		'  make health              Check Postgres and db-collector health endpoints' \
		'  make test                Run all Go tests without Docker integration tests' \
		'  make test-integration    Run isolated PostgreSQL integration tests (requires Docker)' \
		'  make test-race           Run all Docker-free tests with the race detector' \
		'  make vet                 Run Go static checks' \
		'  make rules-check         Validate Prometheus rules and run their promtool unit tests (requires Docker)' \
		'  make build-db-collector-image   Build the local db-collector container image' \
		'  make k8s-apply           Apply the local Kubernetes bundle' \
		'  make k8s-up              Build the image and apply the local Kubernetes bundle' \
		'  make k8s-down            Delete the local Kubernetes bundle' \
		'  make k8s-port-forward    Forward Postgres and db-collector ports to localhost' \
		'  make kind-load-db-collector-image   Load the db-collector image into kind'

tools-check:
	@KIND_VERSION=$(KIND_VERSION) KIND_NODE_IMAGE=$(KIND_NODE_IMAGE) HELM_MIN_VERSION=$(HELM_MIN_VERSION) scripts/tools-check.sh

up:
	docker compose -f $(COMPOSE_FILE) up -d

down:
	docker compose -f $(COMPOSE_FILE) down -v

config:
	docker compose -f $(COMPOSE_FILE) config

# Generates a random SA password per machine instead of copying a shared one.
# The "Dev-" prefix plus hex digits satisfies SQL Server password complexity.
sqlserver-dev-init:
	@set -eu; if [ ! -f $(SQLSERVER_DEV_ENV_FILE) ]; then \
		umask 077; \
		password="Dev-$$(openssl rand -hex 16)"; \
		printf 'MSSQL_SA_PASSWORD=%s\nHEARTBEAT_CREDENTIAL_ENV_SQLSERVER_DEV=sa:%s\n' "$$password" "$$password" > $(SQLSERVER_DEV_ENV_FILE); \
		echo "Created $(SQLSERVER_DEV_ENV_FILE) with a random SA password"; \
	fi
	chmod 600 $(SQLSERVER_DEV_ENV_FILE)
	test -f config/integrations.local-dev.yaml || cp config/integrations.local-dev.example.yaml config/integrations.local-dev.yaml

sqlserver-dev-up:
	docker compose --env-file $(SQLSERVER_DEV_ENV_FILE) -f $(COMPOSE_FILE) -f $(SQLSERVER_DEV_COMPOSE_FILE) up -d --build

sqlserver-dev-down:
	docker compose --env-file $(SQLSERVER_DEV_ENV_FILE) -f $(COMPOSE_FILE) -f $(SQLSERVER_DEV_COMPOSE_FILE) down -v

# Validates with secrets interpolated, but prints the merged file without them.
sqlserver-dev-config:
	docker compose --env-file $(SQLSERVER_DEV_ENV_FILE) -f $(COMPOSE_FILE) -f $(SQLSERVER_DEV_COMPOSE_FILE) config --quiet
	docker compose --env-file $(SQLSERVER_DEV_ENV_FILE) -f $(COMPOSE_FILE) -f $(SQLSERVER_DEV_COMPOSE_FILE) config --no-interpolate

sqlserver-dev-health:
	$(CURL_CHECK) http://localhost:8082/healthz
	$(CURL_CHECK) http://localhost:8082/readyz
	$(CURL_CHECK) http://localhost:9090/-/healthy
	$(CURL_CHECK) http://localhost:3000/api/health

migrate:
	docker compose -f $(COMPOSE_FILE) exec -T postgres psql -U heartbeat -d heartbeat < db/migrations/0001_foundations.up.sql

health:
	docker compose -f $(COMPOSE_FILE) exec postgres pg_isready -U heartbeat -d heartbeat
	$(CURL_CHECK) http://localhost:8082/healthz
	$(CURL_CHECK) http://localhost:8082/readyz

test:
	GOCACHE=$$(pwd)/.tmp/gocache go test ./...

test-integration:
	GOCACHE=$$(pwd)/.tmp/gocache go test -tags=integration -count=1 -timeout=10m ./tests

test-race:
	GOCACHE=$$(pwd)/.tmp/gocache go test -race ./...

vet:
	GOCACHE=$$(pwd)/.tmp/gocache go vet ./...

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

build-db-collector-image:
	docker build -t $(DB_COLLECTOR_IMAGE) -f services/db-collector/Dockerfile .

kind-load-db-collector-image:
	kind load docker-image $(DB_COLLECTOR_IMAGE)

k8s-apply:
	kubectl apply -k $(K8S_DIR)

k8s-down:
	kubectl delete -k $(K8S_DIR)

k8s-up: build-db-collector-image k8s-apply

k8s-port-forward:
	@set -e; \
	pg_pid=""; \
	db_pid=""; \
	cleanup() { \
		if [ -n "$$pg_pid" ]; then kill "$$pg_pid" >/dev/null 2>&1 || true; fi; \
		if [ -n "$$db_pid" ]; then kill "$$db_pid" >/dev/null 2>&1 || true; fi; \
	}; \
	trap cleanup INT TERM EXIT; \
	kubectl -n heartbeat port-forward svc/postgres 5432:5432 & \
	pg_pid="$$!"; \
	kubectl -n heartbeat port-forward svc/db-collector 8082:8082 & \
	db_pid="$$!"; \
	wait "$$pg_pid" "$$db_pid"
