# Node Watchdog Helm Chart

Deploys the Node Watchdog service, which monitors Kubernetes node health and automatically power-cycles unresponsive nodes via Intel AMT.

## Prerequisites

- Kubernetes 1.26+
- Helm 3
- Nodes with Intel vPro/AMT enabled on port 16992
- At least 3 schedulable nodes (for pod anti-affinity)

## Install

```bash
helm install node-watchdog ./deploy/node-watchdog \
  --namespace node-watchdog --create-namespace \
  --set amt.password=YOUR_AMT_PASSWORD
```

From the OCI registry:

```bash
helm install node-watchdog \
  oci://ghcr.io/josephtknight/node-watchdog \
  --namespace node-watchdog --create-namespace \
  --set amt.password=YOUR_AMT_PASSWORD
```

## Uninstall

```bash
helm uninstall node-watchdog --namespace node-watchdog
```

## Values

### Image

| Key | Default | Description |
|-----|---------|-------------|
| `image.repository` | `node-watchdog` | Container image repository |
| `image.tag` | `latest` | Image tag |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy |

### AMT

| Key | Default | Description |
|-----|---------|-------------|
| `amt.username` | `admin` | AMT admin username |
| `amt.password` | `CHANGEME` | AMT admin password |
| `amt.port` | `16992` | AMT WSMAN port |
| `amt.annotation` | `watchdog.example.com/amt-ip` | Node annotation key for AMT IP override |
| `amt.existingSecret` | `""` | Name of an existing Secret to use instead of creating one. Must contain `AMT_USERNAME` and `AMT_PASSWORD` keys. |

### Watchdog Configuration

| Key | Default | Description |
|-----|---------|-------------|
| `config.notReadyThreshold` | `15m` | How long a node must be NotReady before power-cycling |
| `config.pollInterval` | `30s` | How often to check node status |
| `config.cooldownPeriod` | `1h` | Minimum time between restarts of the same node |
| `config.maxConcurrentRestarts` | `1` | Max nodes being power-cycled simultaneously |
| `config.dryRun` | `true` | Log actions without actually power-cycling |

### Deployment

| Key | Default | Description |
|-----|---------|-------------|
| `replicaCount` | `3` | Number of replicas (only the leader is active) |
| `resources.requests.cpu` | `10m` | CPU request |
| `resources.requests.memory` | `64Mi` | Memory request |
| `resources.limits.memory` | `128Mi` | Memory limit |
| `tolerations` | not-ready, unreachable, control-plane | Pod tolerations |
| `preferControlPlaneNodes` | `true` | Prefer scheduling on control-plane nodes |

## Using an Existing Secret

If you manage AMT credentials externally (e.g., via Sealed Secrets or External Secrets Operator), create a Secret with the required keys:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: my-amt-creds
  namespace: node-watchdog
type: Opaque
stringData:
  AMT_USERNAME: "admin"
  AMT_PASSWORD: "my-secret-password"
```

Then reference it:

```bash
helm install node-watchdog ./deploy/node-watchdog \
  --namespace node-watchdog --create-namespace \
  --set amt.existingSecret=my-amt-creds
```

## AMT IP Override

By default, each node's Kubernetes `InternalIP` is used as the AMT endpoint. To override per-node (e.g., for a separate management network):

```bash
kubectl annotate node my-node watchdog.example.com/amt-ip=192.168.1.100
```

The annotation key is configurable via `amt.annotation`.
