# 0003. Deliver the platform with Helm on kind and production Kubernetes

- Status: Accepted (direction). Implementation not started.
- Date: 2026-09-29

## Context

Local development uses Docker Compose (`infra/docker-compose.yml`, 9 services).
A small Kustomize bundle (`infra/k8s/local`) deploys only the DB collector,
the gateway and PostgreSQL. Production is intended to run on cloud Kubernetes.
Keeping Compose for development and a separate production definition means two
deployment systems that drift, and Compose never exercises Kubernetes behavior
such as pod replacement, readiness routing, projected ConfigMaps and rollouts.
The collector hardening work depends on exactly that behavior.

## Decision

- Package Heartbeat with **Helm** and use the same versioned chart locally, in CI
  and in production, with environment-specific values.
- Use **kind** for the local and integration-CI cluster.
- Keep **SQL Server in Docker** as an external test target, outside the cluster.
- Adapt Make targets and CI as part of the migration, then **retire the
  platform Compose definition** once equivalent workflows pass. Do not complete
  the Kustomize bundle first and translate it again later.
- **Principle:** what Heartbeat *deploys* goes through Helm; what Heartbeat
  *tests against* (the SQL Server target, the disposable PostgreSQL schema
  fixture) stays as Docker fixtures.
- Keep the **collector a singleton** until explicit target ownership across
  replicas exists; never scale it by raising the replica count.

## Proposed design (current working plan, subject to change)

- One umbrella chart `heartbeat` owns Heartbeat workloads and content
  (collector, gateway, integrations config, Prometheus rules, Grafana
  dashboards). Third-party engines are pinned, condition-gated dependencies,
  using namespaced charts only (`prometheus-community/prometheus` with
  Alertmanager, `grafana/grafana`, Loki single binary, OpenTelemetry Collector),
  with no cluster-scoped CRDs. Production can disable any engine and point at
  shared services.
- The collector runs as a StatefulSet with one replica, which gives at-most-one
  semantics during node partitions. A headless Service with
  `publishNotReadyAddresses` lets Prometheus scrape it via `dns_sd_configs`
  even when it is unready.
- Values files embed complete `integrations.yaml` documents per environment,
  validated by the same schema and Go loader.
- Two local profiles: `minimal` (collector, Prometheus, Alertmanager, Grafana)
  and `full` (adds Loki, OTel Collector, gateway). PostgreSQL and migrations
  stay out of the deployed stack until the API exists.
- Grafana is stateless; dashboards come from source.
- Access uses kind `extraPortMappings` on the existing Compose ports instead of
  `kubectl port-forward`.
- The Prometheus `alerting` block and a Watchdog rule (external dead-man's
  switch) close the current Prometheus → Alertmanager gap.

## Consequences and risks

Ranked by effect on the goal:

1. **Monitoring failure domain.** If Heartbeat and its alert path run on the
   infrastructure being investigated, a failure there removes the evidence too.
   Keep an external availability check or notification path; a separate
   namespace is not a separate failure domain.
2. **Collector ownership and database load.** Kubernetes can overlap or replace
   pods, but it does not know who owns a SQL target. Control update overlap and
   test query budgets.
3. **Durability.** PVCs are not backups. Metadata and telemetry need separate
   retention and restore policies, tested independently of pod startup.
4. **Environment fidelity.** kind validates objects and application behavior,
   not cloud storage, networking or identity. Test production values, ingress,
   TLS and SQL connectivity in the target environment.
5. **Release boundaries.** A Helm rollback does not reverse schema changes or
   restore telemetry. Pin dependencies; keep app, schema and stateful upgrades
   separate.
6. **Development cost.** A local cluster adds CPU/RAM and concepts. Keep unit
   tests fast, automate the loop, and measure the cost.

## Acceptance criteria

The migration is done when these pass with retained evidence:

- A fresh cluster loads both images on the node's actual CPU architecture and
  every workload becomes ready.
- The same chart renders for kind, CI and production values without separately
  maintained production templates.
- A pod reaches the Docker SQL target (DNS, TCP, authentication, TLS) without
  widening database exposure.
- A code change produces a new running image; a stale `:local` tag cannot mask it.
- With one SQL target configured, `heartbeat_collector_target_up` and probe
  metrics are fresh in Prometheus and Grafana shows the target.
- A valid ConfigMap update changes the active config version. An invalid one is
  rejected visibly while collection continues. A partial reload failure rolls
  back or reports divergence and unreadiness.
- An unavailable SQL target shows as failed, healthy targets continue, and the
  collector does not restart just because a database is down.
- A singleton collector update drains the old pod before the new one polls; the
  collection gap and SQL-side concurrency are measured.
- A stateful restart keeps data designated as persistent; concurrent CI runs
  are isolated and cannot delete developer resources.
- The daily development loop meets an agreed time and resource budget.

## Open decisions

Production Kubernetes distribution and cloud; image and chart registry; deploy
mechanism (GitOps or CI); where production values live; dead-man's-switch
service; secrets backend; Grafana SSO provider. These are tracked in
[TODO.md](../../../TODO.md).

Update 2026-09-30: decided in
[ADR 0005](0005-production-delivery-and-operations-defaults.md) (AWS EKS, Argo CD,
ECR, AWS Secrets Manager, alert channels, Grafana access).
