# Kubernetes Manifests

This directory contains a local Kubernetes bundle for the only implemented workload: `db-collector`.

The bundle is intentionally small:

- `db-collector`
- PostgreSQL for metadata storage
- a `ConfigMap` for `integrations.yaml`
- a `Secret` for the Postgres credentials and DSN

## Local apply

Build the image, then apply the bundle:

```bash
docker build -t heartbeat/db-collector:local -f services/db-collector/Dockerfile .
kubectl apply -k infra/k8s/local
```

If you are using `kind`, load the image first:

```bash
kind load docker-image heartbeat/db-collector:local
```

## Access from your laptop

Use port-forwarding:

```bash
kubectl -n heartbeat port-forward svc/postgres 5432:5432
kubectl -n heartbeat port-forward svc/db-collector 8082:8082
```

Then test:

```bash
curl http://localhost:8082/healthz
curl http://localhost:8082/readyz
```

## YAML files, in plain English

- `namespace.yaml`: creates the `heartbeat` namespace
- `secret-postgres.yaml`: stores database login values and the Postgres DSN
- `configmap-config.yaml`: stores the app config file that the collector reads
- `postgres.yaml`: starts PostgreSQL and gives it a stable network name inside the cluster
- `db-collector.yaml`: starts the collector and wires in its environment variables and mounted config file
- `kustomization.yaml`: lists the files that belong to this local bundle
