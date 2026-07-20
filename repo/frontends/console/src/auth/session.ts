/**
 * Console 会话存储模块。
 *
 * storage key 前缀 `console:`，与 BOSS `boss:` 隔离（PRD NG-8）。
 * 按用户「记住我」勾选切换 localStorage（持久）与 sessionStorage（标签页）介质。
 * 两种介质下 schema 完全一致，仅生命周期不同。
 *
 * Key 列表：
 *   console:access_token   string
 *   console:refresh_token  string
 *   console:expires_at     number（ms epoch）
 *   console:remember_me     'true' | 'false'
 *   console:oidc_state      string（仅 sessionStorage，与 OIDC 流程绑定）
 *   console:return_to       string（重定向前临时保存，一次性消费）
 */

const KEY_PREFIX = 'console:'

const ACCESS_TOKEN_KEY = `${KEY_PREFIX}access_token`
const REFRESH_TOKEN_KEY = `${KEY_PREFIX}refresh_token`
const EXPIRES_AT_KEY = `${KEY_PREFIX}expires_at`
const REMEMBER_ME_KEY = `${KEY_PREFIX}remember_me`
const OIDC_STATE_KEY = `${KEY_PREFIX}oidc_state`
const RETURN_TO_KEY = `${KEY_PREFIX}return_to`

/** 提前刷新阈值：剩余有效期 < 5 分钟时触发 refresh。 */
export const REFRESH_THRESHOLD_MS = 5 * 60 * 1000

type StorageLike = Storage | null

function getStorage(remember: boolean): StorageLike {
  if (typeof window === 'undefined') return null
  return remember ? window.localStorage : window.sessionStorage
}

/** 当前 remember_me 状态决定活动介质。 */
function activeStorage(): StorageLike {
  const remember = getRememberMe()
  return getStorage(remember)
}

function readKey(storage: StorageLike, key: string): string | null {
  if (!storage) return null
  try {
    return storage.getItem(key)
  } catch {
    return null
  }
}

function writeKey(storage: StorageLike, key: string, value: string): void {
  if (!storage) return
  try {
    storage.setItem(key, value)
  } catch {
    // 隐私模式可能写失败，静默忽略
  }
}

function removeKey(storage: StorageLike, key: string): void {
  if (!storage) return
  try {
    storage.removeItem(key)
  } catch {
    // 忽略
  }
}

export interface TokenPair {
  access_token: string
  refresh_token: string
  expires_in: number
  issued_at?: string
}

export interface SessionState {
  access_token: string
  refresh_token: string
  expires_at: number
  remember_me: boolean
}

/** 当前是否勾选「记住我」。默认 false（sessionStorage）。 */
export function getRememberMe(): boolean {
  if (typeof window === 'undefined') return false
  const val = readKey(window.sessionStorage, REMEMBER_ME_KEY) ?? readKey(window.localStorage, REMEMBER_ME_KEY)
  return val === 'true'
}

function setRememberMe(remember: boolean): void {
  const value = remember ? 'true' : 'false'
  writeKey(window.sessionStorage, REMEMBER_ME_KEY, value)
  writeKey(window.localStorage, REMEMBER_ME_KEY, value)
}

/** 计算 access token 过期时间戳（ms epoch）。优先用 issued_at，缺失则用当前时间。 */
function computeExpiresAt(pair: TokenPair): number {
  const issuedAtMs = pair.issued_at ? Date.parse(pair.issued_at) : Date.now()
  if (Number.isNaN(issuedAtMs)) return Date.now() + pair.expires_in * 1000
  return issuedAtMs + pair.expires_in * 1000
}

/**
 * 保存 TokenPair 到当前介质。
 * @param pair Core API 返回的 TokenPair
 * @param remember 是否勾选「记住我」，决定写入 localStorage 或 sessionStorage
 */
export function saveSession(pair: TokenPair, remember: boolean): void {
  const expiresAt = computeExpiresAt(pair)
  const storage = getStorage(remember)
  if (!storage) return

  // 切换介质时清理旧介质残留
  const oldStorage = getStorage(!remember)
  if (oldStorage && oldStorage !== storage) {
    removeKey(oldStorage, ACCESS_TOKEN_KEY)
    removeKey(oldStorage, REFRESH_TOKEN_KEY)
    removeKey(oldStorage, EXPIRES_AT_KEY)
  }

  writeKey(storage, ACCESS_TOKEN_KEY, pair.access_token)
  writeKey(storage, REFRESH_TOKEN_KEY, pair.refresh_token)
  writeKey(storage, EXPIRES_AT_KEY, String(expiresAt))
  setRememberMe(remember)
}

/** 读取当前会话；无 token 或介质不可用返回 null。 */
export function getSession(): SessionState | null {
  const storage = activeStorage()
  if (!storage) return null
  const accessToken = readKey(storage, ACCESS_TOKEN_KEY)
  const refreshToken = readKey(storage, REFRESH_TOKEN_KEY)
  const expiresAtStr = readKey(storage, EXPIRES_AT_KEY)
  if (!accessToken || !refreshToken || !expiresAtStr) return null
  const expiresAt = Number(expiresAtStr)
  if (!Number.isFinite(expiresAt)) return null
  return {
    access_token: accessToken,
    refresh_token: refreshToken,
    expires_at: expiresAt,
    remember_me: getRememberMe(),
  }
}

/** 当前 access token 是否仍有效（未过期）。 */
export function isSessionValid(): boolean {
  const session = getSession()
  if (!session) return false
  return session.expires_at > Date.now()
}

/** 剩余有效期（ms）。无会话返回 0。 */
export function timeToExpiry(): number {
  const session = getSession()
  if (!session) return 0
  return session.expires_at - Date.now()
}

/**
 * 清理当前介质下全部 auth 键。
 * 切换 remember_me 后，旧介质残留也一并清理，确保下次登录介质一致。
 */
export function clearSession(): void {
  if (typeof window === 'undefined') return
  const allKeys = [
    ACCESS_TOKEN_KEY,
    REFRESH_TOKEN_KEY,
    EXPIRES_AT_KEY,
    REMEMBER_ME_KEY,
    OIDC_STATE_KEY,
    RETURN_TO_KEY,
  ]
  for (const key of allKeys) {
    removeKey(window.sessionStorage, key)
    removeKey(window.localStorage, key)
  }
}

/** 启动时 hydrate：未过期 token 自动注入到 API middleware。返回 access token 或 null。 */
export function hydrateSession(): string | null {
  const session = getSession()
  if (!session) return null
  if (session.expires_at <= Date.now()) return null
  return session.access_token
}

/** OIDC state 保存（仅 sessionStorage，与流程绑定）。 */
export function saveOidcState(state: string): void {
  if (typeof window === 'undefined') return
  writeKey(window.sessionStorage, OIDC_STATE_KEY, state)
}

export function consumeOidcState(): string | null {
  if (typeof window === 'undefined') return null
  const state = readKey(window.sessionStorage, OIDC_STATE_KEY)
  removeKey(window.sessionStorage, OIDC_STATE_KEY)
  return state
}

/** 临时保存 returnTo（重定向前写入，登录成功后一次性消费）。 */
export function saveReturnTo(value: string): void {
  if (typeof window === 'undefined') return
  writeKey(window.sessionStorage, RETURN_TO_KEY, value)
}

export function consumeReturnTo(): string | null {
  if (typeof window === 'undefined') return null
  const value = readKey(window.sessionStorage, RETURN_TO_KEY)
  removeKey(window.sessionStorage, RETURN_TO_KEY)
  return value
}

export function getReturnTo(): string | null {
  if (typeof window === 'undefined') return null
  return readKey(window.sessionStorage, RETURN_TO_KEY)
}

/**
 * 校验 returnTo 是否为同源相对路径，防 open redirect。
 * 同源相对路径：以 `/` 开头、不以 `//` 开头、不含协议（`http:`、`https:`...）。
 */
export function isSafeReturnTo(value: string | null | undefined): value is string {
  if (!value) return false
  if (!value.startsWith('/')) return false
  if (value.startsWith('//')) return false
  if (/^[a-z][a-z0-9+.-]*:/i.test(value)) return false
  return true
}

/** 取安全的 returnTo：无效则回退到 fallback（默认 `/`）。 */
export function safeReturnTo(value: string | null | undefined, fallback = '/'): string {
  return isSafeReturnTo(value) ? value : fallback
}

export const STORAGE_KEYS = {
  ACCESS_TOKEN: ACCESS_TOKEN_KEY,
  REFRESH_TOKEN: REFRESH_TOKEN_KEY,
  EXPIRES_AT: EXPIRES_AT_KEY,
  REMEMBER_ME: REMEMBER_ME_KEY,
  OIDC_STATE: OIDC_STATE_KEY,
  RETURN_TO: RETURN_TO_KEY,
} as const
