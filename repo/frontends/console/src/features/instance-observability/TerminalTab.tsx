/**
 * 实例可观测性 · 终端 Tab（exec）。
 *
 * 对齐：
 * - PRD US-012 终端 Tab（exec）：仅 container/gpu_container/sandbox 可用
 * - UX §4.5 终端 Tab 布局、§5.5 终端组件映射、§6.4 终端 Tab 状态、§7.2 Messages
 * - SPEC §4.1.5 exec API 调用契约、§5.3 exec WebSocket 客户端协议契约、§5.6.2 终端 Tab 状态机、§9.4(US-012) 测试矩阵
 *
 * 行为：
 * - 仅 `state=running` + 有 exec 权限时可连接（无权限由服务端 403 体现，见 §6.4 实现）
 * - 非 running：连接按钮 disabled + Tooltip「仅运行中的实例可连接终端」
 * - 无 exec 权限：POST /exec 返回 403 时展示 `Alert theme="warning"`「当前账号无终端访问权限」
 * - 连接流程：POST /instances/{id}/exec（body 含 idempotency_key）→ InstanceExecSession → 用 ws_url 建立 WebSocket
 * - 连接状态 Tag：未连接 / 连接中 / 已连接 / 已过期
 * - 连接中：Button loading
 * - 连接成功：终端区 active（xterm.js 渲染）
 * - session 过期（now > expires_at）：Alert warning「终端会话已过期，请重新连接」+ 重新连接按钮
 * - exec 4xx/422 失败：Message.error + 保留 idle 态
 * - WebSocket 帧格式遵循 SPEC §5.3.2 客户端契约（stdin/resize → stdout/stderr/exit/error）
 * - 终端容器 min-height 400px，bg container，monospace
 * - idle 态：Empty「点击连接终端开始会话」
 */
import { useCallback, useEffect, useRef, useState } from 'react'
import { Alert, Button, Empty, MessagePlugin, Space, Tag, Tooltip } from 'tdesign-react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import type { components } from '@/api/core-schema'
import { coreApi } from '@/api/coreClient'
import { useInstanceContext } from './InstanceContext'

/** Core OpenAPI `InstanceExecSession` 类型。 */
type InstanceExecSession = components['schemas']['InstanceExecSession']

/** Core OpenAPI `CreateInstanceExecSessionRequest` 类型。 */
type CreateInstanceExecSessionRequest = components['schemas']['CreateInstanceExecSessionRequest']

/** Core API 错误响应结构（来自 OpenAPI `ErrorResponse`）。 */
interface CoreApiError {
  code?: string
  message?: string
  request_id?: string
}

/** 终端连接状态机；对齐 SPEC §5.6.2。 */
type TerminalConnectionState =
  | 'idle' // 未连接
  | 'connecting' // POST exec 中
  | 'connected' // ws open
  | 'expired' // now > expires_at
  | 'no-permission' // 403：无 exec scope

/** 连接状态 Tag 的展示文案与主题；对齐 UX §6.4。 */
const CONNECTION_STATE_TAG: Record<
  TerminalConnectionState,
  { label: string; theme: 'default' | 'primary' | 'success' | 'warning' }
> = {
  idle: { label: '未连接', theme: 'default' },
  connecting: { label: '连接中', theme: 'primary' },
  connected: { label: '已连接', theme: 'success' },
  expired: { label: '已过期', theme: 'warning' },
  'no-permission': { label: '无权限', theme: 'warning' },
}

/**
 * 生成 idempotency_key。
 * 对齐 SPEC §7.2：Console 生成（crypto.randomUUID 或 `${prefix}-${Date.now()}` 模式）。
 */
function generateIdempotencyKey(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  return `exec-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

/** WebSocket 帧类型，对齐 SPEC §5.3.2。 */
type ExecFrame =
  | { type: 'stdout' | 'stderr'; data: string }
  | { type: 'exit'; code: number }
  | { type: 'error'; message: string }

/**
 * 终端 Tab 组件。
 *
 * 状态机：idle → connecting → connected；失败回 idle；过期 → expired；403 → no-permission。
 * WebSocket 生命周期由本组件管理，组件卸载时关闭 ws 并销毁 xterm 实例。
 */
export function TerminalTab() {
  const { instance, isRunning } = useInstanceContext()
  const instanceId = instance.id

  const [connState, setConnState] = useState<TerminalConnectionState>('idle')

  /** 当前 exec session 的过期时间（ISO 字符串）。 */
  const [expiresAt, setExpiresAt] = useState<string | null>(null)

  /** 最近一次 exec 请求的 idempotency_key；重试时复用同一 key 以保证幂等。 */
  const idempotencyKeyRef = useRef<string | null>(null)

  /** xterm 实例（lazy 创建）。 */
  const termRef = useRef<Terminal | null>(null)
  /** FitAddon 实例。 */
  const fitRef = useRef<FitAddon | null>(null)
  /** WebSocket 实例。 */
  const wsRef = useRef<WebSocket | null>(null)
  /** 终端挂载容器 div。 */
  const termHostRef = useRef<HTMLDivElement | null>(null)

  /**
   * 清理所有资源：关闭 ws、dispose xterm。
   * 切走 Tab / 组件卸载时调用。
   */
  const cleanup = useCallback(() => {
    const ws = wsRef.current
    if (ws) {
      ws.onopen = null
      ws.onmessage = null
      ws.onerror = null
      ws.onclose = null
      if (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING) {
        ws.close()
      }
      wsRef.current = null
    }
    const term = termRef.current
    if (term) {
      term.dispose()
      termRef.current = null
    }
    fitRef.current = null
  }, [])

  // 组件卸载时清理
  useEffect(() => {
    return () => {
      cleanup()
    }
  }, [cleanup])

  /**
   * 检测会话过期并切换到 expired 状态。
   * 连接成功后启动定时器轮询 expires_at。
   */
  useEffect(() => {
    if (connState !== 'connected' || !expiresAt) return
    const timer = window.setInterval(() => {
      if (Date.now() >= new Date(expiresAt).getTime()) {
        setConnState('expired')
        cleanup()
      }
    }, 1000)
    return () => window.clearInterval(timer)
  }, [connState, expiresAt, cleanup])

  /**
   * 建立 WebSocket 连接并绑定 xterm。
   * 对齐 SPEC §5.3.1 连接建立、§5.3.2 帧格式。
   */
  const connectWebSocket = useCallback(
    (wsUrl: string) => {
      const ws = new WebSocket(wsUrl)
      wsRef.current = ws

      ws.onopen = () => {
        // 创建 xterm 实例并挂载到容器
        if (!termHostRef.current) return
        const term = new Terminal({
          fontFamily: 'var(--td-font-family-mono, monospace)',
          fontSize: 13,
          cursorBlink: true,
          convertEol: true,
        })
        const fit = new FitAddon()
        term.loadAddon(fit)
        term.open(termHostRef.current)
        fit.fit()
        termRef.current = term
        fitRef.current = fit

        // stdin 帧 → 服务端
        // 对齐 SPEC §5.3.2 客户端契约
        term.onData((data) => {
          if (ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify({ type: 'stdin', data }))
          }
        })

        // resize 帧 → 服务端
        term.onResize(({ rows, cols }) => {
          if (ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify({ type: 'resize', rows, cols }))
          }
        })

        setConnState('connected')
      }

      ws.onmessage = (event) => {
        const term = termRef.current
        if (!term) return
        // 解析服务端帧；对齐 SPEC §5.3.2：stdout/stderr/exit/error
        try {
          const frame = JSON.parse(event.data) as ExecFrame
          if (frame.type === 'stdout' || frame.type === 'stderr') {
            term.write(frame.data)
          } else if (frame.type === 'exit') {
            term.write(`\r\n[进程已退出，退出码 ${frame.code}]\r\n`)
          } else if (frame.type === 'error') {
            term.write(`\r\n[错误] ${frame.message}\r\n`)
          }
        } catch {
          // 非预期帧格式：当作 stdout 原样写入，避免阻塞会话
          if (typeof event.data === 'string') {
            term.write(event.data)
          }
        }
      }

      ws.onerror = () => {
        // 连接异常：保留 idle 态并提示
        setConnState('idle')
        MessagePlugin.error('终端连接失败，请稍后重试')
        cleanup()
      }

      ws.onclose = () => {
        // 非过期态下连接关闭，回到 idle
        setConnState((prev) => (prev === 'connected' ? 'idle' : prev))
      }
    },
    [cleanup],
  )

  /**
   * 点击「连接终端」：POST /exec → 建立 ws。
   * 对齐 SPEC §4.1.5 exec API 调用契约、§5.6.2 状态机。
   */
  const handleConnect = useCallback(async () => {
    if (!isRunning) return

    // 重置过期态为 idle 以便重新连接
    if (connState === 'expired') {
      cleanup()
      setConnState('idle')
    }

    setConnState('connecting')

    // 生成或复用 idempotency_key（同一次重试复用，全新连接生成新 key）
    if (!idempotencyKeyRef.current) {
      idempotencyKeyRef.current = generateIdempotencyKey()
    }
    const idempotencyKey = idempotencyKeyRef.current

    // 默认请求体；对齐 OpenAPI CreateInstanceExecSessionRequest
    const body: CreateInstanceExecSessionRequest = {
      idempotency_key: idempotencyKey,
      command: ['/bin/sh'],
      tty: true,
      rows: 24,
      cols: 80,
    }

    try {
      const { data, error } = await coreApi.POST('/instances/{instance_id}/exec', {
        params: { path: { instance_id: instanceId } },
        body,
      })

      // 403：无 exec 权限 → no-permission 态
      if (error) {
        const err = error as CoreApiError
        const code = err?.code
        if (code === 'FORBIDDEN') {
          setConnState('no-permission')
          return
        }
        // 其余 4xx/422：Message.error + 保留 idle 态
        setConnState('idle')
        MessagePlugin.error(err?.message ?? '连接终端失败')
        return
      }

      const session = data as InstanceExecSession
      setExpiresAt(session.expires_at)
      connectWebSocket(session.ws_url)
    } catch {
      setConnState('idle')
      MessagePlugin.error('连接终端失败，请稍后重试')
    }
  }, [connState, connectWebSocket, cleanup, instanceId, isRunning])

  /** 重新连接：清理旧资源，生成新 idempotency_key，重新发起 POST。 */
  const handleReconnect = useCallback(() => {
    cleanup()
    idempotencyKeyRef.current = null
    setExpiresAt(null)
    setConnState('idle')
    void handleConnect()
  }, [cleanup, handleConnect])

  // ---- 渲染 ----

  // 无 exec 权限：整页 Alert warning
  if (connState === 'no-permission') {
    return (
      <Alert
        theme="warning"
        title="当前账号无终端访问权限"
        message="需要 scope:instances:exec 权限才能连接终端。"
      />
    )
  }

  // 会话过期：Alert warning + 重新连接按钮
  if (connState === 'expired') {
    return (
      <Alert
        theme="warning"
        title="终端会话已过期，请重新连接"
        message="会话已超过有效期，点击「重新连接」发起新会话。"
        operation={
          <Button theme="primary" variant="outline" size="small" onClick={handleReconnect}>
            重新连接
          </Button>
        }
      />
    )
  }

  const tag = CONNECTION_STATE_TAG[connState]
  const connecting = connState === 'connecting'
  const connected = connState === 'connected'

  return (
    <div>
      {/* Toolbar：连接终端按钮 + 连接状态 Tag */}
      <div style={{ marginBottom: 12 }}>
        <Space>
          {isRunning ? (
            <Button
              theme="primary"
              loading={connecting}
              disabled={connected}
              onClick={handleConnect}
            >
              {connected ? '已连接' : '连接终端'}
            </Button>
          ) : (
            <Tooltip content="仅运行中的实例可连接终端">
              <Button theme="primary" disabled>
                连接终端
              </Button>
            </Tooltip>
          )}
          <Tag theme={tag.theme}>{tag.label}</Tag>
        </Space>
      </div>

      {/* 终端容器：min-height 400px，bg container，monospace */}
      <div
        style={{
          minHeight: 400,
          background: 'var(--td-bg-color-container)',
          padding: 8,
          borderRadius: 4,
          fontFamily: 'var(--td-font-family-mono, monospace)',
        }}
      >
        {/* idle：Empty 引导 */}
        {!connected && (
          <div style={{ paddingTop: 120 }}>
            <Empty description="点击连接终端开始会话" />
          </div>
        )}
        {/* connected：xterm 挂载点 */}
        <div ref={termHostRef} style={{ display: connected ? 'block' : 'none' }} />
      </div>
    </div>
  )
}
