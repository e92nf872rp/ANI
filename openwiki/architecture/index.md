# Files

- [Core vs Services Boundary](core-vs-services-boundary.md) - Hard architectural boundary between ANI Core and ANI Services layers: directory ownership, dependency direction, import rules, API split
- [ANI Architecture Overview](overview.md) - Two-layer architecture of the KuberCloud ANI platform: ANI Core (infrastructure control plane) and ANI Services (cloud services and orchestration layer)
- [Ports and Adapters Pattern](ports-and-adapters.md) - Capability abstraction layer via pkg/ports (interfaces) and pkg/adapters (implementations): decoupling ANI platform from specific open-source components
- [ANI Technology Stack](tech-stack.md) - Technology stack decisions for the KuberCloud ANI platform: languages, runtime, networking, storage, GPU scheduling, and component selection rationale
