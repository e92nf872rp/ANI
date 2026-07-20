/**
 * ANI Core API client (BOSS) — types generated from Core OpenAPI Spec.
 *
 * 与 Console 同栈，但独立工程。BOSS 消费 Core OIDC 端点（/auth/oidc/begin、/auth/token、
 * /auth/refresh、/auth/logout，与 Console 共用）以及平台账密登录端点
 * `/auth/platform/password/login`。
 */
import createClient from 'openapi-fetch'
import type { paths } from './core-schema'

export const coreApi = createClient<paths>({
  baseUrl: '/api/v1',
  headers: {
    'Content-Type': 'application/json',
  },
})

export { setAuthToken } from './auth'
