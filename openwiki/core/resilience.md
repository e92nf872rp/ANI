---
type: concept
title: Core Resilience Subsystem
description: "pkg/adapters/resilience/: circuit breaker, retry, timeout, degradation — cross-cutting resilience wrappers used by all provider adapters"
tags: [core, resilience, circuit-breaker, retry, timeout, degradation]
---

# Core Resilience Subsystem

## Overview

`pkg/adapters/resilience/` provides cross-cutting retry, circuit breaker, timeout, and degradation wrappers. These are the foundation of Sprint 14 (Core resilience) and are composed into adapter calls throughout the platform.

## Policy Struct

```go
type Policy struct {
    Timeout           time.Duration // per-call timeout
    BaseAttempts      int           // max attempts (including first)
    BaseBackoff       time.Duration // initial backoff
    MaxBackoff        time.Duration // exponential backoff upper bound
    BreakerName       string        // circuit breaker identity (for metrics)
    FailureRatio      float64       // open threshold (e.g. 0.5 = 50%)
    MinRequests       int           // minimum samples before breaker activates
    CooldownPeriod    time.Duration // time in open state before half-open
    HalfOpenMaxReqs   int           // allowed probes in half-open state
}
```

## Circuit Breaker State Machine

```text
         ┌──────────────────────────────┐
         │                              │
         │  CLOSED                       │
         │  (normal operation)           │
         │                              │
         └──────┬───────────────────────┘
                │ FailureRatio exceeded after MinRequests
                ▼
         ┌──────────────────────────────┐
         │  OPEN                         │
         │  (all calls fail fast)        │
         │  ─ CooldownPeriod timer       │
         └──────┬───────────────────────┘
                │ CooldownPeriod elapsed
                ▼
         ┌──────────────────────────────┐
         │  HALF-OPEN                    │
         │  (limited probe requests)     │
         │  ─ HalfOpenMaxReqs probes     │
         └──────┬───────────────────────┘
            ┌───┴───┐
            ▼       ▼
        success    failure
        (→ CLOSED) (→ OPEN)
```

## Retryable Error Classification

From `resilience.go:Retryable()`:

| Error | Classification | Action |
|-------|---------------|--------|
| `StatusError{StatusCode: 429}` | **Retryable** (rate limit) | Backoff + retry |
| `StatusError{StatusCode: 500+}` | **Retryable** (server error) | Backoff + retry |
| `net.OpError` | **Retryable** (connection failure) | Backoff + retry |
| `context.DeadlineExceeded` | **Retryable** (timeout from context deadline) | Backoff + retry |
| `context.Canceled` | **Non-retryable** | Return immediately |
| `net.Error` with `Timeout() == true` | **Retryable** (network timeout) | Backoff + retry |
| `net.Error` implementing `Temporary()` returning true | **Retryable** (transient network error) | Backoff + retry |
| Other errors | **Non-retryable** | Return immediately |

Note: `context.DeadlineExceeded` is classified as **retryable** in the source — a downstream timeout may succeed on retry. Only `context.Canceled` stops retries.

### Circuit Breaker Test Coverage

| Test | File | What It Verifies |
|------|------|------------------|
| `TestRetryableClassification` | `resilience_test.go` | All 9 error category classifications |
| `TestRetrySucceeds` | `resilience_test.go` | Retry succeeds on 3rd attempt after 2 transient failures |
| `TestCircuitBreakerClosesAfterSuccess` | `resilience_test.go` | CLOSED → OPEN → HALF-OPEN → CLOSED transition |
| `TestCircuitBreakerOpensAndStaysOpen` | `resilience_test.go` | CLOSED → OPEN and stays open before cooldown |
| `TestExponentialBackoff` | `resilience_test.go` | Backoff doubles each attempt until capped at MaxBackoff |
| `TestTimeoutCausesContextDeadlineExceeded` | `resilience_test.go` | Per-call `context.WithTimeout` cancellation |

## Degradation Mode

When a circuit breaker opens or a dependency is unavailable, the platform enters **degradation mode** for non-critical dependencies:

- `/readyz` returns HTTP 200 with `"status": "degraded"` instead of 503
- Degraded dependencies are skipped in request processing
- The response signals which capabilities are degraded via the `X-ANI-Degraded-Capabilities` header

## Sprint 14 Resilience Live Gate

| Component | Record | Evidence |
|-----------|--------|----------|
| R-P0-0 | Gateway Shared Store | `development-records/r-p0-0-gateway-shared-store.md` |
| R-P0-1 | Gateway Rate Limit | `development-records/r-p0-1-gateway-rate-limit.md` |
| R-P0-2 | Gateway Idempotency Replay | `development-records/r-p0-2-gateway-idempotency-replay.md` |
| R-P0-3 | Adapter Resilience Timeout | `development-records/r-p0-3-adapter-resilience-timeout.md` |
| R-P0-4 | Readyz Data-Plane Health | `development-records/r-p0-4-readyz-dataplane-health.md` |
| R-P1-5 | Retry / Circuit Breaker | `development-records/r-p1-5-retry-circuit-breaker.md` |
| R-P1-6 | Resilience Degradation | `development-records/r-p1-6-resilience-degradation.md` |
| R-P2-7 | Multi-Endpoint Failover | `development-records/r-p2-7-multi-endpoint-failover-config.md` |

## Usage

```go
policy := resilience.NewPolicy(resilience.Policy{
    Timeout:         5 * time.Second,
    BaseAttempts:    3,
    BaseBackoff:     100 * time.Millisecond,
    MaxBackoff:      2 * time.Second,
    BreakerName:     "object-store",
    FailureRatio:    0.5,
    MinRequests:     10,
    CooldownPeriod:  30 * time.Second,
    HalfOpenMaxReqs: 3,
})

result, err := resilience.WrapCall(ctx, policy, func(ctx context.Context) (any, error) {
    return objectStore.PutObject(ctx, input)
})
```

## References

- [Adapters](adapters.md) — All adapter implementations use these wrappers
- [Observability & Metering](observability-metering.md) — Degradation status in /readyz
- Source: `repo/pkg/adapters/resilience/resilience.go`, `repo/pkg/adapters/resilience/degradation.go`