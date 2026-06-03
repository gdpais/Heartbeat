# Kubernetes Manifests

This directory is reserved for staging/production manifests and Helm values.

Expected K8s delivery rules:
- ConfigMap + secret refs for integration config
- explicit reload support (`SIGHUP` and/or authenticated reload endpoint)
- atomic config swap in Go services
- fail closed on invalid config
