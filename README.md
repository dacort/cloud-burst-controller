# Cloud Burst Controller

A Kubernetes controller that automatically provisions cloud instances (EC2) to handle unschedulable pods. When pods can't be scheduled on existing nodes, the controller matches them to a `BurstNodePool`, selects an appropriately-sized instance type, launches it with [Talos Linux](https://www.talos.dev/), and tears it down when idle.

## Features

- **Pod-aware instance selection** — picks the smallest instance type that fits pending pod CPU, memory, and GPU requirements
- **Multi-instance-type fallback** — tries candidates in order, automatically falling back on EC2 capacity errors
- **Architecture filtering** — respects `kubernetes.io/arch` node affinity for amd64/arm64 (Graviton) workloads
- **GPU support** — matches `nvidia.com/gpu` resource requests to GPU instance families (G5, P3)
- **Resource-based matching rules** — route pods to pools using toleration, node affinity, and resource request rules
- **Automatic scale-down** — reaps idle burst nodes after a configurable cooldown period
- **Orphan detection** — cleans up cloud instances that no longer have a corresponding Kubernetes node

## How It Works

1. The **Provisioner** watches for unschedulable pods with the `burst.homelab.dev/enabled: "true"` annotation
2. Pods are matched to a `BurstNodePool` based on tolerations, node affinity labels, and resource rules
3. The instance selector filters the pool's candidate instance types by CPU, memory, GPU, and architecture
4. EC2 instances are launched with Talos machine configs, with automatic fallback if a type is capacity-constrained
5. The **Reaper** monitors burst nodes and terminates them after the cooldown period with no running workloads
6. The **Orphan Detector** reconciles cloud state with the Kubernetes API to clean up stale instances

## Quick Start

### Prerequisites

- Go 1.24.6+
- Docker 17.03+
- kubectl v1.11.3+
- Access to a Kubernetes cluster
- AWS credentials with EC2 permissions

### Install CRDs and deploy

```sh
make install
make deploy IMG=ghcr.io/dacort/cloud-burst-controller:latest
```

### Create a BurstNodePool

```yaml
apiVersion: burst.homelab.dev/v1alpha1
kind: BurstNodePool
metadata:
  name: default-burst
spec:
  cloud: aws
  aws:
    region: us-east-1
    ami: ami-0123456789abcdef0
    instanceTypes:
      - name: m6i.large
      - name: m6i.xlarge
      - name: m6i.2xlarge
    subnetId: subnet-abc123
    securityGroupIds:
      - sg-abc123
  talos:
    machineConfigSecret: talos-worker-config
  scaling:
    maxNodes: 5
    cooldownPeriod: 5m
    bootTimeout: 5m
  matchRules:
    tolerations:
      - key: burst.homelab.dev/node
        operator: Exists
```

### Annotate pods for bursting

```yaml
metadata:
  annotations:
    burst.homelab.dev/enabled: "true"
```

## Configuration

### Instance Types

You can specify a single instance type (backward-compatible) or an ordered list of candidates:

```yaml
aws:
  # Simple (single type):
  instanceType: m6i.large

  # Advanced (multiple candidates with overrides):
  instanceTypes:
    - name: m7g.large        # arm64 Graviton
    - name: m6i.large        # amd64 fallback
    - name: g5.xlarge        # GPU workloads
      ami: ami-gpu-specific  # per-type AMI override
```

The provisioner selects the smallest type that fits pending pod requirements and falls back to larger types on capacity errors.

### Match Rules

Pods are routed to pools using three rule types:

| Rule | Description |
|------|-------------|
| `tolerations` | Pod must have matching tolerations |
| `nodeAffinityLabels` | Pod must request matching node affinity labels |
| `resources` | Pod must have (or not have) specific resource requests (`Exists` / `DoesNotExist`) |

### GPU Pools

```yaml
matchRules:
  resources:
    - resourceName: nvidia.com/gpu
      operator: Exists
aws:
  instanceTypes:
    - name: g5.xlarge
    - name: g5.2xlarge
```

## Development

```sh
make generate    # Regenerate CRD manifests and deepcopy
make test        # Run unit tests
make lint        # Run golangci-lint
make run         # Run controller locally against current kubeconfig
```

## License

Copyright 2026. Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for details.
