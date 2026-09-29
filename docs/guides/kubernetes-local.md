# Kubernetes (local)

A minimal Kustomize bundle in [`infra/k8s/local`](../../infra/k8s/local) runs
the Heartbeat services on a local cluster (for example kind). It is a stopgap:
[ADR 0003](../architecture/decisions/0003-helm-on-kind-and-production.md)
replaces it with a Helm chart shared by kind, CI and production.

## What it deploys

Namespace `heartbeat`:

| File | Creates |
| --- | --- |
| `namespace.yaml` | The `heartbeat` namespace |
| `secret-postgres.yaml` | PostgreSQL user, password, database, DSN, and the collector admin token (local values only) |
| `configmap-config.yaml` | `integrations.yaml`, mounted into the services. Ships with `targets: []` |
| `postgres.yaml` | PostgreSQL StatefulSet and Service |
| `db-collector.yaml` | DB collector Deployment (liveness/readiness probes, 45s shutdown grace) and Service on port 8082 |
| `otel-gateway.yaml` | OTel gateway Deployment and Service on port 8083 |
| `kustomization.yaml` | Lists the files applied together |

Prometheus, Grafana, Loki, Alertmanager and the OTel Collector are **not**
included. Use Docker Compose for the full stack.

## Run it

```bash
make k8s-up                        # build heartbeat/db-collector:local and kubectl apply -k infra/k8s/local
make kind-load-db-collector-image  # kind only: load the image into the cluster
```

`make k8s-up` builds only the collector image. For the gateway, build and load
`heartbeat/otel-gateway:local` yourself:

```bash
docker build -t heartbeat/otel-gateway:local -f services/otel-gateway/Dockerfile .
kind load docker-image heartbeat/otel-gateway:local
```

Expose PostgreSQL and the collector on your laptop:

```bash
make k8s-port-forward
curl http://localhost:8082/healthz
curl http://localhost:8082/readyz
```

Remove everything with `make k8s-down`.

## Add a SQL Server target

1. Add the target to the `integrations.yaml` embedded in
   `infra/k8s/local/configmap-config.yaml`
   (see [database targets](database-targets.md)).
2. Make the credential available to the collector pod as
   `HEARTBEAT_CREDENTIAL_<REF>`
   ([credential resolution](../reference/configuration.md#credential-resolution)).
3. Re-apply with `make k8s-up`. ConfigMap updates reach the pod after kubelet
   propagation (up to about a minute); then reload the collector or wait for its
   file polling.
