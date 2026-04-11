# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test Commands

```bash
make build                # CGO_ENABLED=0 go build → bin/node-watchdog
make test                 # go test -v -race ./...
make docker-build         # multi-stage Docker build (golang:1.21-alpine → distroless/static)
make deploy               # helm upgrade --install into node-watchdog namespace
make template             # helm template (preview rendered manifests)
make uninstall            # helm uninstall
```

Run a single test or test package:
```bash
go test -v -run TestMaybeRestart_CooldownPreventsRestart ./internal/restarter
go test -v ./internal/monitor
```

No linter is configured.

## Architecture

Node Watchdog monitors Kubernetes node health and power-cycles locked-up nodes via Intel AMT WSMAN. It runs as a 3-replica Deployment with leader election — only the leader actively monitors.

### Component flow

```
main.go
  ├─ config.Load()           env vars → Config struct
  ├─ amt.NewClient()         WSMAN SOAP client with HTTP Digest Auth
  ├─ restarter.New()         safety orchestrator (wraps AMT client)
  ├─ monitor.New()           polling loop (wraps restarter)
  ├─ HTTP server (:8080)     /metrics, /healthz, /readyz — runs on ALL replicas
  └─ leaderelection.RunOrDie()
       └─ OnStartedLeading → mon.Run(ctx)  — only leader polls
```

### Key interfaces for testability

- **`monitor.Restarter`** — Monitor calls `MaybeRestart()` when threshold exceeded. Tests use a mock.
- **`amt.PowerCycler`** — Restarter calls `PowerCycle(ip)`. Tests use a mock.
- **`restarter.NewWithRecorder()`** — Injects a fake `record.EventRecorder` for testing.

Tests use `k8s.io/client-go/kubernetes/fake` for the Kubernetes clientset and `net/http/httptest` for AMT HTTP tests. No external test libraries.

### Safety check order in `restarter.MaybeRestart()`

1. Cooldown (same node not restarted within `COOLDOWN_PERIOD`)
2. Concurrency (max in-flight restarts within 5-minute window)
3. Cluster health floor (if Ready < majority, allow only 1 restart)
4. Control-plane quorum (won't risk etcd by restarting a CP node)
5. Resolve AMT IP (annotation override → InternalIP fallback)
6. Record pre-restart Event on the Node object
7. Execute power cycle (or log in dry-run mode)
8. On success: set cooldown. On failure: no cooldown (allows retry next poll)

### Important behaviors

- **DryRun defaults to `true`** — no power cycles until explicitly set to `false`.
- **Polling, not informers** — `Nodes().List()` every 30s is intentional for a 4-6 node cluster.
- **NotReady state is in-memory only** — not persisted across leader elections. A new leader independently re-tracks durations from when it observes NotReady.
- **Events recorded on Node objects** — `kubectl describe node` shows watchdog actions (`PowerCycleInitiated`, `PowerCycleSucceeded`, `PowerCycleFailed`, `PowerCycleDryRun`).
- **API failure = skip tick** — monitor never acts on stale data.
- **Control-plane detected by labels**: `node-role.kubernetes.io/control-plane` or `node-role.kubernetes.io/master`.

## Configuration

All config is via environment variables (no flags, no config files). Durations use Go `time.ParseDuration` syntax. `AMT_USERNAME` and `AMT_PASSWORD` are required. See `internal/config/config.go` for all fields and defaults.

## Dependencies

- `k8s.io/client-go` (v0.29.4) — K8s API, leader election, event recording
- `github.com/icholy/digest` — HTTP Digest Auth for AMT
- `github.com/prometheus/client_golang` — metrics via `promauto`
- Structured logging via stdlib `log/slog` (JSON handler)
