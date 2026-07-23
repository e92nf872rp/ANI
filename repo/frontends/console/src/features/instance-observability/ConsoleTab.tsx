/**
 * 实例可观测性 · 控制台 Tab（VM console/VNC）。
 *
 * 对齐：
 * - PRD US-013 VM 控制台 Tab：仅 kind=vm；调用 createInstanceConsoleSession
 * - UX §4.6 控制台 Tab 布局、§5.5 控制台组件映射、§6.5 控制台 Tab 状态、§7.2 Messages
 * - SPEC §4.1.6 console API 调用契约、§5.4 VM Console 协议契约、§9.4(US-013) 测试矩阵
 *
 * 行为：
 * - 仅 `kind=vm` 渲染（其余 kind 无此 Tab，由 observabilityTabsConfig 过滤）
 * - 协议 `Select`：console / vnc / serial（默认 vnc）；novnc 若 API 支持则列入（OpenAPI enum 已含 novnc）
 * - 仅 `state=running` 可点击；非 running 打开按钮 disabled
 * - 无 console 权限（POST 返回 403）：`Alert theme="warning"`「无控制台权限」
 * - 调用 `coreApi.POST('/instances/{instance_id}/console', { body: { protocol } })` → `InstanceConsoleSession`
 * - 成功：`window.open(connect_url, '_blank', 'noopener,noreferrer')` + `Message.success`「已在新窗口打开控制台」
 * - `Alert theme="info"` 提示：将在新窗口打开会话，会话过期后请重新申请
 * - 失败：`Message.error`
 * - 打开中：Button loading
 */
import { useCallback, useState } from 'react'
import { Alert, Button, MessagePlugin, Select, Space } from 'tdesign-react'
import type { components } from '@/api/core-schema'
import { coreApi } from '@/api/coreClient'
import { useInstanceContext } from './InstanceContext'

/** Core OpenAPI `InstanceConsoleSession` 类型。 */
type InstanceConsoleSession = components['schemas']['InstanceConsoleSession']

/** Core OpenAPI `CreateInstanceConsoleSessionRequest` 类型。 */
type CreateInstanceConsoleSessionRequest = components['schemas']['CreateInstanceConsoleSessionRequest']

/** Console 协议值（来自 OpenAPI `CreateInstanceConsoleSessionRequest.protocol`）。 */
type ConsoleProtocol = 'console' | 'vnc' | 'novnc' | 'serial'

/** Core API 错误响应结构（来自 OpenAPI `ErrorResponse`）。 */
interface CoreApiError {
  code?: string
  message?: string
  request_id?: string
}

/**
 * 协议选项。对齐 UX §4.6：console / vnc / serial（默认 vnc）；novnc 若 API 支持则列入。
 * OpenAPI `CreateInstanceConsoleSessionRequest.protocol` enum 已含 novnc，故一并列入。
 */
const PROTOCOL_OPTIONS: Array<{ label: string; value: ConsoleProtocol }> = [
  { label: 'vnc', value: 'vnc' },
  { label: 'console', value: 'console' },
  { label: 'serial', value: 'serial' },
  { label: 'novnc', value: 'novnc' },
]

/** 默认协议。对齐 UX §4.6 / OpenAPI `CreateInstanceConsoleSessionRequest.protocol` default。 */
const DEFAULT_PROTOCOL: ConsoleProtocol = 'vnc'

/** 控制台 Tab 状态机；对齐 UX §6.5。 */
type ConsoleTabState =
  | 'idle' // 就绪，可提交
  | 'opening' // POST console 中
  | 'no-permission' // 403：无 console scope

/**
 * 控制台 Tab 组件（VM）。
 *
 * 状态机：idle → opening → 成功（新窗口 + Message.success）/ 失败（Message.error，回 idle）；
 * 403 → no-permission（Alert warning）。
 */
export function ConsoleTab() {
  const { instance, isRunning } = useInstanceContext()
  const instanceId = instance.id

  const [protocol, setProtocol] = useState<ConsoleProtocol>(DEFAULT_PROTOCOL)
  const [tabState, setTabState] = useState<ConsoleTabState>('idle')

  /**
   * 点击「打开控制台」：POST /console → 新窗口打开 connect_url。
   * 对齐 SPEC §4.1.6 console API 调用契约、§5.4 VM Console 协议契约。
   */
  const handleOpenConsole = useCallback(async () => {
    if (!isRunning) return

    setTabState('opening')

    const body: CreateInstanceConsoleSessionRequest = { protocol }

    try {
      const { data, error } = await coreApi.POST('/instances/{instance_id}/console', {
        params: { path: { instance_id: instanceId } },
        body,
      })

      if (error) {
        const err = error as CoreApiError
        const code = err?.code
        // 403：无 console 权限 → no-permission 态
        if (code === 'FORBIDDEN') {
          setTabState('no-permission')
          return
        }
        // 其余 4xx：Message.error + 回 idle 态
        setTabState('idle')
        MessagePlugin.error(err?.message ?? '打开控制台失败')
        return
      }

      const session = data as InstanceConsoleSession
      // 成功：新窗口打开 connect_url + Message.success
      // 对齐 PRD US-013 / UX §6.5 opened、§7.2 控制台已打开
      window.open(session.connect_url, '_blank', 'noopener,noreferrer')
      MessagePlugin.success('已在新窗口打开控制台')
      setTabState('idle')
    } catch {
      setTabState('idle')
      MessagePlugin.error('打开控制台失败，请稍后重试')
    }
  }, [instanceId, isRunning, protocol])

  // 无 console 权限：Alert warning「无控制台权限」
  // 对齐 Issue AC、UX §6.5 disabled-no-permission
  if (tabState === 'no-permission') {
    return (
      <Alert
        theme="warning"
        title="无控制台权限"
        message="当前账号无 scope:instances:console 权限，无法打开控制台会话。"
      />
    )
  }

  const opening = tabState === 'opening'

  return (
    <div>
      {/* Form inline：协议选择 + 打开控制台按钮 */}
      <div style={{ marginBottom: 12 }}>
        <Space>
          <Select
            value={protocol}
            options={PROTOCOL_OPTIONS}
            onChange={(value) => setProtocol(value as ConsoleProtocol)}
            style={{ width: 160 }}
            placeholder="选择协议"
          />
          {isRunning ? (
            <Button
              theme="primary"
              loading={opening}
              onClick={handleOpenConsole}
            >
              打开控制台
            </Button>
          ) : (
            // 非 running：按钮 disabled（不带 Tooltip，保持简洁；UX §6.5 仅要求 disabled）
            <Button theme="primary" disabled>
              打开控制台
            </Button>
          )}
        </Space>
      </div>

      {/* Alert info：将在新窗口打开会话，会话过期后请重新申请 */}
      <Alert
        theme="info"
        message="将在新窗口打开会话，会话过期后请重新申请。"
      />
    </div>
  )
}
