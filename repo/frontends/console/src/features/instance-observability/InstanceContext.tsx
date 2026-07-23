/**
 * 实例可观测性详情页上下文。
 *
 * 提供 instance 记录、kind、state 给子 Tab 组件共享。
 * 对齐 SPEC §2.2.1 `InstanceContext`、§3.3 Console 内部类型。
 */
import { createContext, useContext } from 'react'
import type { components } from '@/api/core-schema'
import type { InstanceKind } from './observabilityTabsConfig'

/** Core OpenAPI `InstanceRecord` 类型。 */
export type InstanceRecord = components['schemas']['InstanceRecord']

/** 实例状态值（来自 OpenAPI `InstanceRecord.state`）。 */
export type InstanceState = InstanceRecord['state']

/** 上下文承载的值。 */
export interface InstanceContextValue {
  /** 完整实例记录。 */
  instance: InstanceRecord
  /** 实例类型（来自 `instance.kind`）。 */
  kind: InstanceKind
  /** 实例状态（来自 `instance.state`）。 */
  state: InstanceState
  /** 实例是否已删除（`state === 'deleted'`）。 */
  isDeleted: boolean
  /** 实例是否运行中（`state === 'running'`）。 */
  isRunning: boolean
}

export const InstanceContext = createContext<InstanceContextValue | null>(null)

/**
 * 从 `InstanceRecord` 构造上下文值。
 */
export function buildInstanceContextValue(instance: InstanceRecord): InstanceContextValue {
  const kind = instance.kind as InstanceKind
  const state = instance.state
  return {
    instance,
    kind,
    state,
    isDeleted: state === 'deleted',
    isRunning: state === 'running',
  }
}

/**
 * 消费实例上下文。必须在 `InstanceContext.Provider` 内调用。
 * 对齐 SPEC §2.2.1，被各 Tab 组件共享。
 */
export function useInstanceContext(): InstanceContextValue {
  const ctx = useContext(InstanceContext)
  if (!ctx) {
    throw new Error('useInstanceContext 必须在 InstanceContext.Provider 内使用')
  }
  return ctx
}
