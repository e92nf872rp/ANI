/**
 * BOSS 会话存储模块。
 *
 * storage key 前缀 `boss:`，与 Console `console:` 隔离（PRD NG-8）。
 * 按用户「记住我」勾选切换 localStorage（持久）与 sessionStorage（标签页）介质。
 *
 * Key 列表：
 *   boss:access_token   string
 *   boss:refresh_token  string
 *   boss:expires_at     number（ms epoch）
 *   boss:remember_me     'true' | 'false'
 *   boss:oidc_state      string（仅 sessionStorage，与 OIDC 流程绑定，与 Console 隔离防冲突）
 *   boss:return_to       string（重定向前临时保存，一次性消费）
 */

const KEY_PREFIX = 'boss:'

const ACCESS_TOKEN_KEY = `${KEY_PREFIX}access_token`
const REFRESH_TOKEN_KEY = `${KEY_PREFIX}refresh_token`
const EXPIRES_AT_KEY = `${KEY_PREFIX}expires_at`
const REMEMBER_ME_KEY = `${KEY_PREFIX}remember_me`
const OIDC_STATE_KEY = `${KEY_PREFIX}oidc_state`
const RETURN_TO_KEY = `${KEY_PREFIX}return_to`

export const REFRESH_THRESHOLD_MS = 5 * 60 * 1000

type StorageLike = Storage | null

function getStorage(remember: boolean): StorageLike {
  if (typeof window === 'undefined') return null
  return remember ? window.localStorage : window.sessionStorage
}

function activeStorage(): StorageLike {
  return getStorage(getRememberMe())
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
    // 隐私模式可能写失败
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

function computeExpiresAt(pair: TokenPair): number {
  const issuedAtMs = pair.issued_at ? Date.parse(pair.issued_at) : Date.now()
  if (Number.isNaN(issuedAtMs)) return Date.now() + pair.expires_in * 1000
  return issuedAtMs + pair.expires_in * 1000
}

export function saveSession(pair: TokenPair, remember: boolean): void {
  const expiresAt = computeExpiresAt(pair)
  const storage = getStorage(remember)
  if (!storage) return

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

export function isSessionValid(): boolean {
  const session = getSession()
  if (!session) return false
  return session.expires_at > Date.now()
}

export function timeToExpiry(): number {
  const session = getSession()
  if (!session) return 0
  return session.expires_at - Date.now()
}

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

export function hydrateSession(): string | null {
  const session = getSession()
  if (!session) return null
  if (session.expires_at <= Date.now()) return null
  return session.access_token
}

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

export function isSafeReturnTo(value: string | null | undefined): value is string {
  if (!value) return false
  if (!value.startsWith('/')) return false
  if (value.startsWith('//')) return false
  if (/^[a-z][a-z0-9+.-]*:/i.test(value)) return false
  return true
}

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
