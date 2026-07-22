/**
 * ANI Console API auth middleware。
 *
 * - `setAuthToken(token)`：登录成功 / hydrate 时注入 Bearer
 * - `clearAuthToken()`：登出 / 401 时清理
 * - 401 统一处理：清会话 + 保存 returnTo + 跳 /login（登录端点除外，防无限重定向）
 * - Token Refresh：剩余有效期 < 5 分钟时主动 refresh
 *
 * 与 Services API（`api`）和 Core API（`coreApi`）共用同一 middleware。
 */
import { api } from './client'
import { coreApi } from './coreClient'
import {
  clearSession,
  consumeReturnTo,
  getSession,
  getReturnTo,
  REFRESH_THRESHOLD_MS,
  saveReturnTo,
  saveSession,
  timeToExpiry,
} from '../auth/session'

let bearerToken: string | null = null
let middlewareAttached = false
let refreshing: Promise<boolean> | null = null

/** 不拦截 401 的路径（登录端点本身 401 是认证失败，非会话过期）。 */
const AUTH_ENDPOINTS = new Set<string>([
  '/auth/oidc/begin',
  '/auth/token',
  '/auth/refresh',
  '/auth/logout',
  '/auth/password/login',
  '/auth/platform/password/login',
])

const authMiddleware = {
  onRequest({ request }: { request: Request }) {
    if (bearerToken) {
      request.headers.set('Authorization', `Bearer ${bearerToken}`)
    }
    return request
  },
}

const response401Middleware = {
  async onResponse({ response, request }: { response: Response; request: Request }) {
    if (response.status === 401) {
      const url = new URL(request.url)
      const pathname = url.pathname
      const apiPrefix = '/api/v1'
      const endpoint = pathname.startsWith(apiPrefix) ? pathname.slice(apiPrefix.length) : pathname
      const isAuthEndpoint = Array.from(AUTH_ENDPOINTS).some((e) => endpoint === e || endpoint.startsWith(`${e}/`))
      if (!isAuthEndpoint) {
        // 先尝试 refresh，成功则让调用方重试，失败才跳登录
        const refreshed = await refreshAccessToken()
        if (!refreshed) {
          handle401()
        }
      }
    }
    return response
  },
}

function ensureAuthMiddleware() {
  if (middlewareAttached) return
  api.use(authMiddleware)
  coreApi.use(authMiddleware)
  api.use(response401Middleware)
  coreApi.use(response401Middleware)
  middlewareAttached = true
}

/** 登录成功或 hydrate 时调用，注入 Bearer token。 */
export function setAuthToken(token: string) {
  bearerToken = token
  ensureAuthMiddleware()
}

/** 清理 Bearer token 与会话（登出 / 401）。不清理 returnTo（由调用方决定）。 */
export function clearAuthToken() {
  bearerToken = null
  clearSession()
}

function currentPath(): string {
  if (typeof window === 'undefined') return '/'
  return window.location.pathname + window.location.search
}

function handle401() {
  if (typeof window === 'undefined') return
  const current = currentPath()
  if (current.startsWith('/login') || current.startsWith('/auth/callback')) return

  // 先保存 returnTo（current），再清会话（clearSession 会清 returnTo），
  // 最后再写一次 returnTo 确保不被 clearSession 抹掉。
  saveReturnTo(current)
  bearerToken = null
  clearSession()
  saveReturnTo(current)

  const search = new URLSearchParams({ returnTo: current }).toString()
  window.location.assign(`/login?${search}`)
}

/** 剩余有效期 < 5 分钟时触发 refresh；返回是否仍有效。 */
export function maybeRefresh(): Promise<boolean> {
  const session = getSession()
  if (!session) return Promise.resolve(false)
  const remaining = timeToExpiry()
  if (remaining > REFRESH_THRESHOLD_MS) return Promise.resolve(true)
  return refreshAccessToken()
}

/** 调 Core /auth/refresh 更新 access token；401 触发会话过期流。 */
export function refreshAccessToken(): Promise<boolean> {
  if (refreshing) return refreshing
  const session = getSession()
  if (!session) return Promise.resolve(false)

  refreshing = (async () => {
    try {
      const { data, error, response } = await coreApi.POST('/auth/refresh', {
        body: { refresh_token: session.refresh_token },
      })
      if (error || !data || response.status !== 200) {
        if (response.status === 401) {
          handle401()
        }
        return false
      }
      saveSession(
        {
          access_token: data.access_token,
          refresh_token: session.refresh_token,
          expires_in: data.expires_in,
        },
        session.remember_me,
      )
      setAuthToken(data.access_token)
      return true
    } catch {
      return false
    } finally {
      refreshing = null
    }
  })()

  return refreshing
}

/** 登出：POST /auth/logout；无论成败清本地会话与 middleware；返回是否成功。 */
export async function logout(): Promise<boolean> {
  const session = getSession()
  let success = true
  if (session) {
    try {
      const jti = parseJti(session.access_token)
      if (jti) {
        const { response } = await coreApi.POST('/auth/logout', { body: { jti } })
        if (response.status !== 200) success = false
      }
    } catch {
      success = false
    }
  }
  clearAuthToken()
  return success
}

/** 从 JWT payload 读 jti；无效 JWT 返回 null。 */
function parseJti(token: string): string | null {
  try {
    const parts = token.split('.')
    if (parts.length !== 3) return null
    const payload = JSON.parse(atob(parts[1].replace(/-/g, '+').replace(/_/g, '/')))
    if (typeof payload?.jti === 'string') return payload.jti
    return null
  } catch {
    return null
  }
}

/** 获取当前 returnTo（用于登录页跳转前读取）。 */
export function getPendingReturnTo(): string | null {
  return getReturnTo()
}

/** 一次性消费 returnTo；调用后从 storage 清除。 */
export function consumePendingReturnTo(): string | null {
  return consumeReturnTo()
}

export { AUTH_ENDPOINTS }
