.PHONY: help up down config migrate health test k8s-up k8s-down k8s-apply k8s-port-forward build-db-collector-image kind-load-db-collector-image

COMPOSE_FILE := infra/docker-compose.yml
K8S_DIR := infra/k8s/local
DB_COLLECTOR_IMAGE := heartbeat/db-collector:local

help:
	@printf '%s\n' \
		'Heartbeat developer commands:' \
		'  make up                  Start postgres + db-collector with Docker Compose' \
		'  make down                Stop the local Docker Compose stack and remove volumes' \
		'  make config              Validate the Docker Compose file' \
		'  make migrate             Apply the foundation migration into local Postgres' \
		'  make health              Check Postgres and db-collector health endpoints' \
		'  make test                Run the Go test suite for the implemented service' \
		'  make build-db-collector-image   Build the local db-collector container image' \
		'  make k8s-apply           Apply the local Kubernetes bundle' \
		'  make k8s-up              Build the image and apply the local Kubernetes bundle' \
		'  make k8s-down            Delete the local Kubernetes bundle' \
		'  make k8s-port-forward    Forward Postgres and db-collector ports to localhost' \
		'  make kind-load-db-collector-image   Load the db-collector image into kind'

up:
	docker compose -f $(COMPOSE_FILE) up -d

down:
	docker compose -f $(COMPOSE_FILE) down -v

config:
	docker compose -f $(COMPOSE_FILE) config

migrate:
	docker compose -f $(COMPOSE_FILE) exec -T postgres psql -U heartbeat -d heartbeat < db/migrations/0001_foundations.up.sql

health:
	docker compose -f $(COMPOSE_FILE) exec postgres pg_isready -U heartbeat -d heartbeat
	curl http://localhost:8082/healthz
	curl http://localhost:8082/readyz

test:
	GOCACHE=$$(pwd)/.tmp/gocache go test ./services/db-collector/...

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
