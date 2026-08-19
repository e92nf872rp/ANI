/**
 * BOSS 平台写权限：platform-admin / platform-ops 可写；platform-readonly 只读。
 * JWT payload 角色字段兼容 roles / realm_access.roles。
 */
import { getSession } from './session'

function decodeJwtPayload(token: string): Record<string, unknown> | null {
  try {
    const part = token.split('.')[1]
    if (!part) return null
    const json = atob(part.replace(/-/g, '+').replace(/_/g, '/'))
    return JSON.parse(json) as Record<string, unknown>
  } catch {
    return null
  }
}

function extractRoles(payload: Record<string, unknown> | null): string[] {
  if (!payload) return []
  if (Array.isArray(payload.roles)) {
    return payload.roles.filter((r): r is string => typeof r === 'string')
  }
  const realm = payload.realm_access
  if (realm && typeof realm === 'object' && Array.isArray((realm as { roles?: unknown }).roles)) {
    return ((realm as { roles: unknown[] }).roles).filter((r): r is string => typeof r === 'string')
  }
  return []
}

/** 是否可执行套餐写操作（创建/发布/禁用/删除/改限额/绑定） */
export function canWritePlatform(): boolean {
  const session = getSession()
  if (!session?.access_token) return false
  const roles = extractRoles(decodeJwtPayload(session.access_token))
  if (roles.includes('platform-admin') || roles.includes('platform-ops')) return true
  if (roles.includes('platform-readonly')) return false
  // 开发态无角色声明时默认允许写；后端 403 兜底
  return true
}
