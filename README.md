# RabbitMQ Burst Scaler

A KEDA external scaler that scales services to X replicas for Y minutes when a RabbitMQ message is received.

## Overview

This scaler enables "burst scaling" - when a message arrives on a configured RabbitMQ exchange/routing key, the target deployment scales to a specified number of replicas and maintains that scale for a configured duration.

**Important**: This is a single shared service that handles multiple ScaledObjects. You deploy it once, then create ScaledObjects for each service you want to scale.

```
┌─────────────────────────────────────────────────────┐
│      One Burst Scaler (keda-system namespace)       │
│                                                     │
│  ┌─────────────┐ ┌─────────────┐ ┌─────────────┐   │
│  │ Consumer    │ │ Consumer    │ │ Consumer    │   │
│  │ order-svc   │ │ campaign-svc│ │ payment-svc │   │
│  └─────────────┘ └─────────────┘ └─────────────┘   │
└─────────────────────────────────────────────────────┘
         ▲                ▲                ▲
         │                │                │
    ScaledObject    ScaledObject    ScaledObject
    (order-svc)     (campaign-svc)  (payment-svc)
```

### Key Features

- **Configuration in ScaledObject**: No separate CRD needed - all config lives in KEDA's ScaledObject trigger metadata
- **State persistence**: Uses RabbitMQ queues with TTL to survive scaler restarts
- **Timer reset**: Each new message resets the burst duration countdown
- **Per-ScaledObject consumers**: Dynamically creates consumers based on KEDA configuration

### How It Works

1. KEDA calls the scaler with trigger metadata from the ScaledObject
2. Scaler creates a per-ScaledObject consumer bound to the specified exchange/routing key
3. When a message arrives, the scaler publishes a "burst marker" to a state queue with TTL
4. On `GetMetrics` calls, the scaler checks if a marker exists and returns the target replica count

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
  repository = "https://your-org.github.io/rabbitmq-burst-scaler"  # or use local chart
  chart      = "rabbitmq-burst-scaler"
  version    = "0.1.0"

  set {
    name  = "image.repository"
    value = "ghcr.io/your-org/rabbitmq-burst-scaler"
  }

  set {
    name  = "image.tag"
    value = "v1.0.0"
  }

  depends_on = [helm_release.keda]
}
```

### Option 2: Helm Chart directly

```bash
helm install rabbitmq-burst-scaler ./charts/burst-scaler \
  --namespace keda-system \
  --set image.repository=ghcr.io/your-org/rabbitmq-burst-scaler \
  --set image.tag=v1.0.0
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

### Option 3: Plain Kubernetes Manifests

```bash
kubectl apply -f deploy/
```

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
  - type: external
    metadata:
      scalerAddress: "rabbitmq-burst-scaler.keda-system:9090"
      host: "rabbitmq.default"
      exchange: "events"
      routingKey: "orders.created"
      burstReplicas: "5"
      burstDurationMinutes: "2"
    authenticationRef:
      name: rabbitmq-auth
```

### TriggerAuthentication (once per namespace or cluster)

```yaml
apiVersion: keda.sh/v1alpha1
kind: TriggerAuthentication
metadata:
  name: rabbitmq-auth
spec:
  secretTargetRef:
  - parameter: username
    name: rabbitmq-secret
    key: username
  - parameter: password
    name: rabbitmq-secret
    key: password
```

### Trigger Metadata Reference

| Parameter | Required | Description | Default |
|-----------|----------|-------------|---------|
| `scalerAddress` | Yes | Address of the burst scaler service | - |
| `host` | Yes | RabbitMQ hostname | - |
| `port` | No | RabbitMQ port | `5672` |
| `vhost` | No | RabbitMQ virtual host | `/` |
| `exchange` | Yes | Exchange to bind the consumer to | - |
| `routingKey` | Yes | Routing key to listen for | - |
| `burstReplicas` | Yes | Number of replicas to scale to | - |
| `burstDurationMinutes` | Yes | Duration to maintain burst replicas | - |

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
go run ./cmd/scaler
```

## Architecture

### State Persistence

The burst state is persisted in RabbitMQ using a state queue per ScaledObject:
- Queue name: `burst-state-{namespace}-{scaledObjectName}`
- Configuration: `max-length=1`, `x-message-ttl=Y*60*1000`
- New messages replace old ones (drop-head overflow)

This ensures state survives scaler restarts and each new message resets the timer.

## License

MIT
