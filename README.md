# Node Watchdog

Kubernetes service that automatically power-cycles locked-up nodes via Intel AMT out-of-band management. Designed for small (4-6 node) Talos Linux clusters with Intel vPro/AMT hardware.

When a node stays `NotReady` for a configurable threshold (default 15 minutes), Node Watchdog sends a WSMAN power-cycle command to the node's AMT interface on port 16992 — no IPMI, no BMC, no agent on the target node required.

## How It Works

1. **Leader election** — 3 replicas deployed across different nodes; only the leader actively monitors. If the leader's node dies, a standby takes over within ~30-40 seconds.
2. **Polling** — The leader lists all nodes every 30s and tracks how long each has been `NotReady`.
3. **Safety checks** — Before power-cycling, the restarter validates cooldown periods, concurrency limits, cluster health quorum, and etcd quorum for control-plane nodes.
4. **AMT power cycle** — Sends a `CIM_PowerManagementService.RequestPowerStateChange` (PowerState=5) via SOAP/WSMAN with HTTP Digest Auth.
5. **Events** — Actions are recorded as Kubernetes Events on the Node object, visible via `kubectl describe node`.

## Safety Guards

| Check | Behavior |
|---|---|
| **Cooldown** | Won't restart the same node within 1 hour (configurable) |
| **Concurrency** | Only 1 restart at a time by default |
| **Cluster health floor** | If less than a majority of nodes are Ready, limits to 1 restart and logs a degraded-cluster warning |
| **Control-plane quorum** | Won't restart a control-plane node if it would risk etcd quorum loss |
| **Dry run** | Enabled by default — logs what it *would* do without actually power-cycling |
| **API failure** | If the Kubernetes API is unreachable, the monitor skips the tick and does not act on stale data |
| **AMT failure** | Failed power-cycles don't set the cooldown, allowing retry on the next poll |

## Configuration

All configuration is via environment variables, typically injected from a ConfigMap and Secret.

| Variable | Default | Description |
|---|---|---|
| `NOT_READY_THRESHOLD` | `15m` | How long a node must be NotReady before restart |
| `POLL_INTERVAL` | `30s` | How often to check node status |
| `COOLDOWN_PERIOD` | `1h` | Minimum time between restarts of the same node |
| `MAX_CONCURRENT_RESTARTS` | `1` | Max nodes being power-cycled simultaneously |
| `AMT_PORT` | `16992` | AMT WSMAN port |
| `AMT_USERNAME` | *(required)* | AMT admin username |
| `AMT_PASSWORD` | *(required)* | AMT admin password |
| `AMT_ANNOTATION` | `watchdog.example.com/amt-ip` | Node annotation key for AMT IP override |
| `DRY_RUN` | `true` | Log actions without actually power-cycling |

### AMT IP Resolution

By default, the node's Kubernetes `InternalIP` is used as the AMT endpoint. To override (e.g., if AMT is on a separate management network), annotate the node:

```bash
kubectl annotate node my-node watchdog.example.com/amt-ip=192.168.1.100
```

## Deployment

### Prerequisites

- Kubernetes cluster with nodes that have Intel vPro/AMT enabled on port 16992
- AMT credentials (shared across nodes)
- At least 3 schedulable nodes (for HA pod anti-affinity)

### Deploy

Install via Helm:

```bash
helm upgrade --install node-watchdog deploy/node-watchdog \
  --namespace node-watchdog --create-namespace \
  --set amt.username=admin \
  --set amt.password=YOUR_AMT_PASSWORD
```

Or use the Makefile:

```bash
make deploy
```

Override any value from `deploy/node-watchdog/values.yaml`:

```bash
helm upgrade --install node-watchdog deploy/node-watchdog \
  --namespace node-watchdog --create-namespace \
  --set amt.password=YOUR_AMT_PASSWORD \
  --set config.dryRun=false \
  --set image.repository=myregistry/node-watchdog \
  --set image.tag=v1.0.0
```

Preview rendered manifests without installing:

```bash
make template
```

Uninstall:

```bash
make uninstall
```

### Validate

The service starts in dry-run mode by default. Check the logs to confirm it's detecting node states correctly:

```bash
kubectl -n node-watchdog logs -l app.kubernetes.io/name=node-watchdog --follow
```

Once satisfied, disable dry run:

```bash
helm upgrade node-watchdog deploy/node-watchdog \
  --namespace node-watchdog --reuse-values \
  --set config.dryRun=false
```

## Building

```bash
# Build binary
make build

# Run tests
make test

# Build Docker image
make docker-build

# Build and push
make docker-build docker-push IMAGE=myregistry/node-watchdog TAG=v1.0.0
```

## Metrics

Prometheus metrics are exposed on `:8080/metrics`:

| Metric | Type | Description |
|---|---|---|
| `watchdog_nodes_not_ready` | Gauge | Current number of NotReady nodes being tracked |
| `watchdog_restarts_total{node, result}` | Counter | Restart attempts by node and outcome (`success`, `error`, `dry_run`) |
| `watchdog_restart_duration_seconds{node}` | Histogram | AMT power-cycle call latency |
| `watchdog_quorum_blocked_total` | Counter | Restarts blocked by safety checks |
| `watchdog_leader` | Gauge | 1 if this replica is the current leader |

## HA / Failure Modes

| Scenario | Behavior |
|---|---|
| Leader's node locks up | New leader elected in ~30-40s, independently tracks NotReady duration |
| AMT call fails | Error Event recorded, no cooldown set, retry on next poll |
| Kubernetes API unreachable | Monitor skips tick, no action on stale data |
| Multiple nodes NotReady | Serialized restarts; quorum guard prevents action during massive outage |
| Watchdog pod evicted | Tolerations give 300s buffer; leader election handles failover |
| All 3 watchdog pods down | Requires 3+ simultaneous node failures — manual intervention needed |

## Project Structure

```
cmd/watchdog/main.go              Entrypoint, leader election, HTTP server
internal/config/config.go         Configuration loading and validation
internal/monitor/monitor.go       Node polling loop, NotReady tracking
internal/amt/client.go            Minimal WSMAN SOAP client with Digest Auth
internal/restarter/restarter.go   Restart orchestrator with safety checks
internal/metrics/metrics.go       Prometheus metric definitions
deploy/node-watchdog/             Helm chart
```
