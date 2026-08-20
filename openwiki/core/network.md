---
type: concept
title: Core Network Subsystem
description: "Network resource model (VPC/subnet/security group), ports.NetworkService, Kube-OVN provider adapter, network status reconciliation"
tags: [core, network, vpc, subnet, security-group, kube-ovn]
---

# Core Network Subsystem

## Network Resource Model

| Resource | Port Type | Key Fields |
|----------|-----------|------------|
| **VPC** | `NetworkVPCRecord` | TenantID, VPCID, Name, CIDR, State, Reason, CreatedAt, UpdatedAt |
| **Subnet** | `NetworkSubnetRecord` | TenantID, SubnetID, VPCID, Name, CIDR, Gateway, State, Reason |
| **Security Group** | `NetworkSecurityGroupRecord` | TenantID, SGID, Name, Rules[], State |
| **Security Group Rule** | `NetworkSecurityGroupRule` | Priority, Direction (ingress/egress), Protocol, PortRange, CIDR, Action (allow/deny) |
| **Overview** | `NetworkOverviewRecord` | Resources (per-type count), Capabilities (feature status), Relationships, DeleteRisks |

Shared state machine for all network resources: `Pending → Available → Failed → Deleting → Deleted`.

## Port Interfaces

| Port | Key Methods | Purpose |
|------|-------------|---------|
| `NetworkService` | `CreateVPC`, `GetVPC`, `ListVPCs`, `DeleteVPC`, `CreateSubnet`, ... | VPC/subnet CRUD and overview |
| `NetworkResourceStore` | `Create`, `Get`, `List`, `Delete`, `UpdateStatus` | Persistence for network resources |
| `NetworkProviderRenderer` | `RenderVPCManifest`, `RenderSubnetManifest`, ... | Generate Kube-OVN CRD manifests |
| `NetworkProviderDryRun` | `DryRun(manifest) -> DryRunResult` | Server-side dry-run via K8s API |
| `NetworkProviderApply` | `Apply(manifest) -> ApplyResult` | Apply Kube-OVN CRDs to K8s |
| `NetworkStatusReconciler` | `Reconcile(ctx, records) -> []status` | Reconcile stored vs observed network state |

## Kube-OVN Provider

Default network provider implementation in `pkg/adapters/runtime/`:

- **`kubeovn_network_renderer.go`** — Generates Kube-OVN CRDs: `VPC`, `Subnet`, `SecurityGroup` (native Kube-OVN CRD types)
- **`kubeovn_network_provider.go`** — Implements `NetworkProviderDryRun`/`Apply` via K8s REST client
- **`network_service.go`** — `NetworkService` implementation using the store + renderer + provider pipeline
- **`network_store.go`** — `NetworkResourceStore` implementation (Postgres-backed via metadata store)
- **`network_status_reconciler.go`** — `NetworkStatusReconciler` implementation

## References

- [Architecture Overview](../architecture/overview.md)
- [Ports Catalog](ports-catalog.md) — All port definitions
- [Adapters](adapters.md) — All adapter implementations
- Source: `repo/pkg/ports/network_resources.go`, `repo/pkg/adapters/runtime/kubeovn_*.go`, `repo/pkg/adapters/runtime/network_*.go`