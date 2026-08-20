---
type: reference
title: BOSS Admin Portal (Frontend)
description: "Admin BOSS portal (React 18 + TypeScript + TDesign + TanStack Router): route structure, tenant plan management components, GPU pool, notification email settings, quota management pages."
tags: [frontends, boss, admin, react, tdesign, tenant-plans]
---

# BOSS Admin Portal

## Overview

**Directory**: `frontends/boss/` · **Stack**: React 18 + TypeScript + TDesign React + TanStack Router + TanStack Query + Zustand + Vite

Admin operations portal for platform administrators and operators.

## Route Structure

| Path | Component | Description |
|------|-----------|-------------|
| `/login` | LoginPage | Admin login (password only; OIDC not implemented yet) |
| `/auth/callback` | AuthCallback | OIDC callback handler |
| `/` (authenticated) | OverviewDashboard | Platform overview widget dashboard |
| `/tenants/plans` | TenantPlanList | Plan listing with CRUD |
| `/tenants/plans/new` | CreatePlanWizard | Multi-step plan creation wizard |
| `/tenants/plans/$planId` | PlanDetailPage | Plan detail with tabs (info, bound tenants, audit, quota limits) |
| `/tenants/quotas` | QuotaList | Quota management listing |
| `/tenants/quotas/new` | CreateQuotaPage | Create new quota entry |
| `/tenants/quotas/$planId` | QuotaDetailPage | Quota detail view |
| `/ops/gpu-pool` | GPUPoolPage | GPU resource pool management |
| `/integration/notification-settings/email/smtp` | SmtpForm | SMTP server configuration |
| `/integration/notification-settings/email/recipients` | RecipientTable | Email recipient management |
| `/integration/notification-settings/email/subscriptions` | SubscriptionTable | Event subscription management |

## Tenant Plan Components

| Component | File | Purpose |
|-----------|------|---------|
| `CreatePlanWizard` | `components/tenant-plans/CreatePlanWizard.tsx` | Multi-step wizard: basic info → quota limits → review |
| `PlanTable` | `components/tenant-plans/PlanTable.tsx` | Plan list with status badges, search, pagination |
| `PlanDetailPage` | `components/tenant-plans/PlanDetailPage.tsx` | Detail with tabbed sub-views |
| `EditPlanInfoDialog` | `components/tenant-plans/EditPlanInfoDialog.tsx` | Inline edit dialog for plan metadata |
| `BoundTenantsTab` | `components/tenant-plans/BoundTenantsTab.tsx` | Tenants associated with this plan |
| `AuditLogsTab` | `components/tenant-plans/AuditLogsTab.tsx` | Audit trail for plan changes |
| `QuotaLimitsTab` | `components/tenant-plans/QuotaLimitsTab.tsx` | Per-dimension quota limits display |
| `planStatus` | `components/tenant-plans/planStatus.tsx` | Status badge (draft/active/disabled) helper |

## Notification Email Components

| Component | File | Purpose |
|-----------|------|---------|
| `SmtpForm` | `components/notification-email/SmtpForm.tsx` | SMTP server host/auth settings |
| `RecipientTable` | `components/notification-email/RecipientTable.tsx` | Recipient CRUD with inline editing |
| `RecipientDrawer` | `components/notification-email/RecipientDrawer.tsx` | Slide-out drawer for add/edit recipient |
| `SubscriptionTable` | `components/notification-email/SubscriptionTable.tsx` | Event type → email channel mapping |
| `TestSendButton` | `components/notification-email/TestSendButton.tsx` | Send test email button |

## Build Commands

| Command | Description |
|---------|-------------|
| `pnpm install` | Install dependencies |
| `pnpm run dev` | Vite dev server |
| `pnpm run build` | Production build |
| `pnpm run type-check` | TypeScript type checking |
| `pnpm run gen-api` | Regenerate TS types from services OpenAPI |
| `pnpm run lint` | ESLint |

## PRD References

- `repo/services/docs/boss-modules/governance/` — Governance status boards, completion matrix, delivery workflow
- `repo/services/docs/boss-modules/metering/` — Metering module PRD/SPEC docs
- `repo/services/docs/boss-modules/tenant/` — Tenant plan module PRD/SPEC docs
- `repo/prd/boss/login/` — BOSS login PRD (P1 password login, OIDC deferred)
- `repo/spec/boss/login/` — BOSS login SPEC (P1 implementation spec)

## References

- [Console Portal](console.md) — User-facing counterpart
- [Module Documentation](module-docs.md) — Governance docs index
- Source: `frontends/boss/`, `services/docs/boss-modules/`