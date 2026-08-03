/**
 * ANI Services API client (BOSS) — types generated from OpenAPI Spec.
 *
 * 与 Console 同栈，但独立工程，baseUrl 相同（Services 业务 API）。
 * 如 BOSS 暂不使用 Services API，可忽略此文件；保留以备未来扩展。
 */
import createClient from 'openapi-fetch'
import type { paths } from './schema'

export const api = createClient<paths>({
  baseUrl: '/api/v1/svc',
  headers: {
    'Content-Type': 'application/json',
  },
})

export { setAuthToken } from './auth'
