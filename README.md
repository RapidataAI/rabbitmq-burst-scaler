# RabbitMQ Burst Scaler

A KEDA external scaler that scales services to X replicas for Y duration when a RabbitMQ message is received.

## Overview

This scaler enables "burst scaling" - when a message arrives on a configured RabbitMQ exchange/routing key, the target deployment scales to a specified number of replicas and maintains that scale for a configured duration.

**Important**: This is a single shared service that handles multiple ScaledObjects. You deploy it once, then create ScaledObjects for each service you want to scale.

```
┌─────────────────────────────────────────────────────────────┐
│      One Burst Scaler (keda-system namespace)               │
│                                                             │
│  ┌─────────────┐ ┌─────────────┐ ┌─────────────┐           │
│  │ Consumer    │ │ Consumer    │ │ Consumer    │           │
│  │ order-svc   │ │ campaign-svc│ │ payment-svc │           │
│  └─────────────┘ └─────────────┘ └─────────────┘           │
└─────────────────────────────────────────────────────────────┘
         ▲                ▲                ▲
         │                │                │
    ScaledObject    ScaledObject    ScaledObject
    (order-svc)     (campaign-svc)  (payment-svc)
```

### Key Features

- **Push-based scaling**: Uses KEDA's `external-push` trigger for immediate scaling when messages arrive
- **Configuration in ScaledObject**: No separate CRD needed - all config lives in KEDA's ScaledObject trigger metadata
- **State persistence**: Uses RabbitMQ queues with TTL to survive scaler restarts
- **Timer reset**: Each new message resets the burst duration countdown
- **Per-ScaledObject consumers**: Dynamically creates consumers based on KEDA configuration

### How It Works

1. KEDA calls the scaler with trigger metadata from the ScaledObject
2. Scaler creates a per-ScaledObject consumer bound to the specified exchange/routing key
3. When a message arrives, the scaler publishes a "burst marker" to a state queue with TTL
4. The scaler immediately notifies KEDA via the `StreamIsActive` gRPC stream (push-based)
5. On `GetMetrics` calls, the scaler checks if a marker exists and returns the target replica count

## Installation

### Prerequisites

- Kubernetes cluster
- [KEDA](https://keda.sh/) installed
- RabbitMQ accessible from the cluster

### Option 1: Helm Chart via Terraform (Recommended for GitOps)

```hcl
resource "helm_release" "rabbitmq_burst_scaler" {
  name       = "rabbitmq-burst-scaler"
  namespace  = "keda-system"
  repository = "https://your-org.github.io/rabbitmq-burst-scaler"
  chart      = "rabbitmq-burst-scaler"
  version    = "0.3.0"

  set {
    name  = "image.repository"
    value = "ghcr.io/your-org/rabbitmq-burst-scaler"
  }

  set {
    name  = "image.tag"
    value = "v1.0.0"
  }

  # RabbitMQ connection settings
  set {
    name  = "rabbitmq.host"
    value = "rabbitmq.default.svc.cluster.local"
  }

  set_sensitive {
    name  = "rabbitmq.username"
    value = var.rabbitmq_username
  }

  set_sensitive {
    name  = "rabbitmq.password"
    value = var.rabbitmq_password
  }

  depends_on = [helm_release.keda]
}
```

### Option 2: Helm Chart directly

```bash
helm repo add rabbitmq-burst-scaler https://your-org.github.io/rabbitmq-burst-scaler
helm install rabbitmq-burst-scaler rabbitmq-burst-scaler/rabbitmq-burst-scaler \
  --namespace keda-system \
  --set image.repository=ghcr.io/your-org/rabbitmq-burst-scaler \
  --set image.tag=v1.0.0 \
  --set rabbitmq.host=rabbitmq.default.svc.cluster.local \
  --set rabbitmq.username=guest \
  --set rabbitmq.password=guest
```

### Option 3: Plain Kubernetes Manifests

```bash
kubectl apply -f deploy/
```

### Helm Values

| Parameter | Description | Default |
|-----------|-------------|---------|
| `image.repository` | Image repository | `ghcr.io/your-org/rabbitmq-burst-scaler` |
| `image.tag` | Image tag | `appVersion` |
| `service.port` | gRPC service port | `9090` |
| `logLevel` | Log level (info/debug) | `info` |
| `resources.requests.cpu` | CPU request | `50m` |
| `resources.requests.memory` | Memory request | `64Mi` |
| `rabbitmq.host` | RabbitMQ hostname | `""` |
| `rabbitmq.port` | RabbitMQ port | `5672` |
| `rabbitmq.vhost` | RabbitMQ virtual host | `/` |
| `rabbitmq.username` | RabbitMQ username | `""` |
| `rabbitmq.password` | RabbitMQ password | `""` |
| `rabbitmq.existingSecret` | Use existing secret for credentials | `""` |

## Configuration

### RabbitMQ Connection (Environment Variables)

RabbitMQ connection settings are configured via environment variables on the burst-scaler deployment. This is the standard pattern for KEDA external scalers, as KEDA does not pass TriggerAuthentication values to external scalers via gRPC.

| Environment Variable | Required | Description | Default |
|---------------------|----------|-------------|---------|
| `RABBITMQ_HOST` | Yes | RabbitMQ hostname | - |
| `RABBITMQ_PORT` | No | RabbitMQ port | `5672` |
| `RABBITMQ_USERNAME` | Yes | RabbitMQ username | - |
| `RABBITMQ_PASSWORD` | Yes | RabbitMQ password | - |
| `RABBITMQ_VHOST` | No | RabbitMQ virtual host | `/` |

Example deployment configuration:

```yaml
env:
- name: RABBITMQ_HOST
  value: "rabbitmq.default.svc.cluster.local"
- name: RABBITMQ_PORT
  value: "5672"
- name: RABBITMQ_USERNAME
  valueFrom:
    secretKeyRef:
      name: rabbitmq-credentials
      key: username
- name: RABBITMQ_PASSWORD
  valueFrom:
    secretKeyRef:
      name: rabbitmq-credentials
      key: password
- name: RABBITMQ_VHOST
  value: "/"
```

### Trigger Metadata (ScaledObject)

Trigger-specific settings are configured in the ScaledObject metadata:

| Parameter | Required | Description | Default |
|-----------|----------|-------------|---------|
| `scalerAddress` | Yes | Address of the burst scaler service | - |
| `exchange` | Yes | Exchange to bind the consumer to | - |
| `routingKey` | Yes | Routing key to listen for | - |
| `burstReplicas` | Yes | Number of replicas to scale to | - |
| `burstDuration` | Yes | Duration to maintain burst replicas (e.g., "30s", "2m", "1h") | - |

## Usage

Once the scaler is deployed, create ScaledObjects for each service. These go in your GitOps repo alongside your application manifests.

### ScaledObject (per service)

```yaml
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: order-service-burst
spec:
  scaleTargetRef:
    name: order-service
  pollingInterval: 5
  cooldownPeriod: 30
  minReplicaCount: 0
  maxReplicaCount: 10
  triggers:
  - type: external-push
    metadata:
      scalerAddress: "rabbitmq-burst-scaler.keda-system:9090"
      exchange: "events"
      routingKey: "orders.created"
      burstReplicas: "5"
      burstDuration: "2m"
```

### Duration Format

The `burstDuration` parameter accepts standard Go duration strings:
- `30s` - 30 seconds
- `2m` - 2 minutes
- `1h` - 1 hour
- `1h30m` - 1 hour and 30 minutes

Minimum value is `1s`.

### burstDuration vs cooldownPeriod

- **burstDuration**: How long the scaler returns the burst replica count after receiving a message. Each new message resets this timer.
- **cooldownPeriod**: KEDA's built-in setting for how long to wait after metrics drop to 0 before scaling to minReplicaCount.

Example: With `burstDuration: "2m"` and `cooldownPeriod: 30`:
1. Message arrives → scaler returns 5 replicas
2. 2 minutes pass with no messages → scaler returns 0 replicas
3. 30 more seconds pass → KEDA scales to minReplicaCount

## Development

### Building

```bash
go build -o burst-scaler ./cmd/scaler
docker build -t rabbitmq-burst-scaler:latest .
```

### Testing

```bash
go test -v ./...
go test -race -coverprofile=coverage.out ./...
```

### Running Locally

```bash
export GRPC_PORT=9090
export LOG_LEVEL=debug
export RABBITMQ_HOST=localhost
export RABBITMQ_USERNAME=guest
export RABBITMQ_PASSWORD=guest
go run ./cmd/scaler
```

## Architecture

### State Persistence

The burst state is persisted in RabbitMQ using a state queue per ScaledObject:
- Queue name: `burst-state-{namespace}-{scaledObjectName}`
- Configuration: `max-length=1`, `x-message-ttl=duration_in_ms`
- New messages replace old ones (drop-head overflow)

This ensures state survives scaler restarts and each new message resets the timer.

### Push-based Scaling

This scaler uses KEDA's `external-push` trigger type, which enables immediate notification when a burst should occur:

1. When a message arrives on RabbitMQ, the consumer triggers a burst
2. The scaler immediately notifies all connected KEDA instances via the `StreamIsActive` gRPC stream
3. KEDA then calls `GetMetrics` to get the actual replica count

This provides faster scaling response compared to polling-based triggers.

## License

MIT
