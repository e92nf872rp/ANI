---
type: reference
title: User Console (Frontend)
description: "User Console (React 18 + TypeScript + TDesign + TanStack Router): route structure, shell components, instance observability features, GPU scheduling pages, auth flow (OIDC + password login)."
tags: [frontends, console, react, tdesign, user-interface]
---

# User Console

## Overview

**Directory**: `frontends/console/` · **Stack**: React 18 + TypeScript + TDesign React + TanStack Router + TanStack Query + Zustand + Vite

The primary user-facing web application for ANI platform tenants.

### Architecture Notes

The Console is a **Vite + TanStack Router** SPA — **not** a Next.js application. There is **no SSR (Server-Side Rendering)** or RSC (React Server Components). All rendering is client-side with data fetching via TanStack Query + openapi-fetch. Routing is file-based via TanStack Router's `createFileRoute` pattern with pathless layout routes for auth protection (see [Auth Session](auth-session.md)).

## Tech Stack

| Component | Choice | Purpose |
|-----------|--------|---------|
| Framework | React 18 | Component model |
| UI Library | TDesign React (^1.10.0) | Enterprise-grade component library |
| Routing | TanStack Router (^1.40.0) | Type-safe file-based routing |
| Data Fetching | TanStack Query (^5.40.0) | Server state management |
| State | Zustand (^4.5.0) | Client state management |
| API Client | openapi-fetch (^0.12.2) | Typed HTTP client from OpenAPI spec |
| Schema Type Gen | openapi-typescript (^7.13.0) | TS types from OpenAPI |
| Terminal | @xterm/xterm + @xterm/addon-fit | Web terminal for instances |
| Charts | echarts + echarts-for-react | GPU/inventory charts |
| Build | Vite (^8.1.4) | Dev server + production build |
| Type Checking | TypeScript (^5.4.0) + tsc --noEmit | Type safety |

## Route Structure

### Public Routes

| Path | Component | Description |
|------|-----------|-------------|
| `/login` | Login page | OIDC redirect + password login tab (two tabs) |
| `/auth/callback` | Auth callback | OIDC callback handler (exchanges code for token) |

### Authenticated Routes (layout: `_authenticated.tsx`)

| Path | Component | Description |
|------|-----------|-------------|
| `/` | OverviewDashboard | Overview with GPU resource cards, usage summary |
| `/compute/gpu` | GPUComputePage | GPU compute overview |
| `/compute/gpu-containers` | GPUContainerList | GPU container instance list |
| `/compute/gpu-containers/$instanceId` | GPUContainerDetail | Single GPU container view |
| `/compute/gpu-containers/-create-dialog` | GPUContainerCreate | Create GPU container dialog |
| `/compute/instances/$instanceId` | InstanceDetail | Instance detail (observability tabs) |
| `/gpu-inventory` | GPUInventoryPage | GPU inventory overview (node list, device list) |
| `/kb` | KnowledgeBaseList | KB list |
| `/kb/$kbId/chat` | KBChat | KB chat interface (SSE streaming) |
| `/models` | ModelList | Model registry list |
| `/models/import` | ModelImport | Model import page |
| `/registry` | RegistryList | Container registry view |
| `/settings` | SettingsOverview | Settings page |
| `/settings/api-keys` | APIKeyManagement | API Key CRUD |
| `/settings/gpu-queues` | GPUQueueManagement | GPU scheduling queue management |
| `/usage` | UsageOverview | Usage/metering overview |

## Shell Components

| Component | File | Purpose |
|-----------|------|---------|
| `ConsolePage` | `components/shell/ConsolePage.tsx` | Standard page layout wrapper |
| `ConsolePageHeader` | `components/shell/ConsolePageHeader.tsx` | Page header with breadcrumbs + actions |
| `ConsoleContentCard` | `components/shell/ConsoleContentCard.tsx` | Content card container |

## Instance Observability Features

| Tab | Component | Description |
|-----|-----------|-------------|
| Console | `features/instance-observability/ConsoleTab.tsx` | Web terminal (Xterm.js) |
| Metrics | `features/instance-observability/MetricsTab.tsx` | Prometheus metric charts |
| Metrics Snapshot | `features/instance-observability/MetricsSnapshot.tsx` | Current metric snapshot |
| Metrics Chart | `features/instance-observability/MetricsChart.tsx` | Historical metric chart (ECharts) |
| Logs | `features/instance-observability/LogsTab.tsx` | Loki log viewer |
| Events | `features/instance-observability/EventsTab.tsx` | Instance events |
| Security Events | `features/instance-observability/SecurityEventsTab.tsx` | Security-related events |
| Instance Context | `features/instance-observability/InstanceContext.tsx` | Instance metadata display |

## GPU Scheduling Pages

| Page | Description |
|------|-------------|
| `/settings/gpu-queues` | Queue CRUD management (Volcano queue integration) |
| GPU inventory view | Node-class table, device-class table |
| GPU container create dialog | GPU type/shares selection, queue assignment |

## Auth Flow

```text
User visits /login
  ├── If OIDC configured → redirect to OIDC provider → callback → token exchange → redirect to /
  └── If password login tab → POST /api/v1/auth/password-login → JWT → redirect to /
```

Console uses `ANI_AUTH_MODE=auth_service` in production and `ANI_AUTH_MODE=dev` in local dev (with X-Dev-Tenant-ID / X-Dev-User-ID headers).

### Session & Token Management

See [Auth Session](../frontends/auth-session.md) for the complete session lifecycle: token refresh (5min threshold), remember-me localStorage vs sessionStorage, OIDC state management, and logout workflow.

### Route Protection

Protected routes are wrapped in the `_authenticated.tsx` pathless layout route. Its `beforeLoad` guard:

1. Calls `getSession()` — if no valid session, saves `returnTo` and redirects to `/login?returnTo=...`
2. Calls `maybeRefresh()` — silently refreshes access token if <5min until expiry
3. Calls `setAuthToken()` — injects Bearer token into `openapi-fetch` client

A 1-minute `setInterval` in the layout component triggers `maybeRefresh()` to keep the session alive during long-running user sessions.

## Build Commands

| Command | Description |
|---------|-------------|
| `pnpm install` | Install dependencies |
| `pnpm run dev` | Vite dev server |
| `pnpm run build` | Production build |
| `pnpm run type-check` | TypeScript type checking (tsc --noEmit) |
| `pnpm run gen-api` | Regenerate TS types from services OpenAPI |
| `pnpm run lint` | ESLint |

## References

- [BOSS Portal](boss.md) — Admin counterpart
- [Module Documentation](module-docs.md) — Per-module governance docs in `services/docs/console-modules/`
- [Services API](../services-layer/services-api.md) — API contract consumed by Console
- Source: `frontends/console/`, `services/docs/console-modules/`