/**
 * 配额套餐（tenant-plans）API — Services OpenAPI typed client。
 */
import { api } from './client'
import type { components } from './schema'

export type TenantPlan = components['schemas']['TenantPlan']
export type TenantPlanListItem = components['schemas']['TenantPlanListItem']
export type TenantPlanListResponse = components['schemas']['TenantPlanListResponse']
export type CreateTenantPlanRequest = components['schemas']['CreateTenantPlanRequest']
export type UpdateTenantPlanRequest = components['schemas']['UpdateTenantPlanRequest']
export type PlanQuotaLimitView = components['schemas']['PlanQuotaLimitView']
export type PlanQuotaLimitInput = components['schemas']['PlanQuotaLimitInput']
export type PlanQuotaLimitsResponse = components['schemas']['PlanQuotaLimitsResponse']
export type QuotaMetaItem = components['schemas']['QuotaMetaItem']
export type QuotaMetaListResponse = components['schemas']['QuotaMetaListResponse']
export type BoundTenant = components['schemas']['BoundTenant']
export type BoundTenantsResponse = components['schemas']['BoundTenantsResponse']
export type PlanAuditLog = components['schemas']['PlanAuditLog']
export type PlanAuditLogListResponse = components['schemas']['PlanAuditLogListResponse']
export type IdempotentResult = components['schemas']['IdempotentResult']
export type BindPlanRequest = components['schemas']['BindPlanRequest']

export type ApiError = { code?: string; message?: string; status?: number }

function throwApiError(error: unknown, status: number): never {
  const err = error as { code?: string; message?: string }
  throw { code: err.code, message: err.message, status } satisfies ApiError
}

export interface ListTenantPlansParams {
  limit?: number
  cursor?: string
  status?: 'draft' | 'active' | 'disabled'
  search?: string
}

export async function listTenantPlans(
  params: ListTenantPlansParams = {},
): Promise<TenantPlanListResponse> {
  const { data, error, response } = await api.GET('/tenant-plans', {
    params: { query: params },
  })
  if (error) throwApiError(error, response.status)
  return data as TenantPlanListResponse
}

export async function createTenantPlan(
  body: Omit<CreateTenantPlanRequest, 'idempotency_key'> & { idempotency_key?: string },
): Promise<IdempotentResult> {
  const { data, error, response } = await api.POST('/tenant-plans', {
    body: {
      ...body,
      idempotency_key: body.idempotency_key ?? crypto.randomUUID(),
    },
  })
  if (error) throwApiError(error, response.status)
  return data as IdempotentResult
}

export async function getTenantPlan(planId: string): Promise<TenantPlan> {
  const { data, error, response } = await api.GET('/tenant-plans/{planId}', {
    params: { path: { planId } },
  })
  if (error) throwApiError(error, response.status)
  return data as TenantPlan
}

export async function updateTenantPlan(
  planId: string,
  body: Omit<UpdateTenantPlanRequest, 'idempotency_key'> & { idempotency_key?: string },
): Promise<IdempotentResult> {
  const { data, error, response } = await api.PUT('/tenant-plans/{planId}', {
    params: { path: { planId } },
    body: {
      ...body,
      idempotency_key: body.idempotency_key ?? crypto.randomUUID(),
    },
  })
  if (error) throwApiError(error, response.status)
  return data as IdempotentResult
}

export async function deleteTenantPlan(planId: string): Promise<IdempotentResult> {
  const { data, error, response } = await api.DELETE('/tenant-plans/{planId}', {
    params: { path: { planId } },
  })
  if (error) throwApiError(error, response.status)
  return data as IdempotentResult
}

export async function getTenantPlanQuotaLimits(
  planId: string,
): Promise<PlanQuotaLimitsResponse> {
  const { data, error, response } = await api.GET('/tenant-plans/{planId}/quota-limits', {
    params: { path: { planId } },
  })
  if (error) throwApiError(error, response.status)
  return data as PlanQuotaLimitsResponse
}

export async function updateTenantPlanQuotaLimits(
  planId: string,
  items: PlanQuotaLimitInput[],
): Promise<IdempotentResult> {
  const { data, error, response } = await api.PUT('/tenant-plans/{planId}/quota-limits', {
    params: { path: { planId } },
    body: {
      idempotency_key: crypto.randomUUID(),
      items,
    },
  })
  if (error) throwApiError(error, response.status)
  return data as IdempotentResult
}

export async function activateTenantPlan(planId: string): Promise<IdempotentResult> {
  const { data, error, response } = await api.POST('/tenant-plans/{planId}/activate', {
    params: { path: { planId } },
    body: { idempotency_key: crypto.randomUUID() },
  })
  if (error) throwApiError(error, response.status)
  return data as IdempotentResult
}

export async function disableTenantPlan(planId: string): Promise<IdempotentResult> {
  const { data, error, response } = await api.POST('/tenant-plans/{planId}/disable', {
    params: { path: { planId } },
    body: { idempotency_key: crypto.randomUUID() },
  })
  if (error) throwApiError(error, response.status)
  return data as IdempotentResult
}

export async function listTenantPlanBoundTenants(
  planId: string,
): Promise<BoundTenantsResponse> {
  const { data, error, response } = await api.GET('/tenant-plans/{planId}/tenants', {
    params: { path: { planId } },
  })
  if (error) throwApiError(error, response.status)
  return data as BoundTenantsResponse
}

export async function listBindableTenants(
  planId: string,
): Promise<BoundTenantsResponse> {
  const { data, error, response } = await api.GET(
    '/tenant-plans/{planId}/bindable-tenants',
    { params: { path: { planId } } },
  )
  if (error) throwApiError(error, response.status)
  return data as BoundTenantsResponse
}

export interface ListPlanAuditLogsParams {
  limit?: number
  cursor?: string
}

export async function listTenantPlanAuditLogs(
  planId: string,
  params: ListPlanAuditLogsParams = {},
): Promise<PlanAuditLogListResponse> {
  const { data, error, response } = await api.GET('/tenant-plans/{planId}/audit-logs', {
    params: { path: { planId }, query: params },
  })
  if (error) throwApiError(error, response.status)
  return data as PlanAuditLogListResponse
}

export async function listQuotaMeta(): Promise<QuotaMetaListResponse> {
  const { data, error, response } = await api.GET('/quota-meta')
  if (error) throwApiError(error, response.status)
  return data as QuotaMetaListResponse
}

export async function bindTenantPlan(
  tenantId: string,
  planId: string,
): Promise<IdempotentResult> {
  const body: BindPlanRequest = {
    idempotency_key: crypto.randomUUID(),
    plan_id: planId,
  }
  const { data, error, response } = await api.POST('/tenants/{tenantId}/plan', {
    params: { path: { tenantId } },
    body,
  })
  if (error) throwApiError(error, response.status)
  return data as IdempotentResult
}
