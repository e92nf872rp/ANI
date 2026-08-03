import { createFileRoute, redirect } from '@tanstack/react-router'
import { useState } from 'react'
import { Alert, Button, Card, Checkbox, Form, Input, MessagePlugin, Tabs } from 'tdesign-react'
import { coreApi } from '@/api/coreClient'
import { setAuthToken } from '@/api/auth'
import {
  consumeReturnTo,
  isSessionValid,
  saveOidcState,
  saveRememberMe,
  saveSession,
  safeReturnTo,
} from '@/auth/session'

/**
 * 生成幂等键：`boss-pwd-<timestamp>-<random>`，符合 OpenAPI `^[A-Za-z0-9_-]{1,128}$` 约束。
 */
function generateIdempotencyKey(): string {
  const ts = Date.now().toString(36)
  const rand = Math.random().toString(36).slice(2, 10)
  return `boss-pwd-${ts}-${rand}`
}

/**
 * BOSS `/login` — Plain 企业风登录卡（P1）。
 *
 * 双 Tab：企业登录（OIDC，与 Console 共用 Core /auth/oidc/begin、/auth/token）
 * 与账号密码（POST /auth/platform/password/login，平台专属）。
 *
 * 与 Console 关键差异：
 *   - 标题「ANI 平台运营台」
 *   - 无 tenant_name 字段（平台管理员无租户上下文）
 *   - storage key 前缀 `boss:`，与 Console 隔离
 *   - OIDC state 用 `boss:oidc_state`，与 Console `console:oidc_state` 隔离防冲突
 *   - 页内 Alert info 提示「本入口仅供平台管理员」
 */

export const Route = createFileRoute('/login')({
  beforeLoad: () => {
    if (isSessionValid()) {
      const stored = consumeReturnTo()
      throw redirect({ to: safeReturnTo(stored, '/') })
    }
  },
  component: LoginPage,
})

type LoginState = 'idle' | 'validating' | 'loading' | 'redirecting' | 'error'

function LoginPage() {
  const [tab, setTab] = useState<'oidc' | 'password'>('oidc')
  const [rememberMe, setRememberMe] = useState(false)

  // OIDC fields
  // (none — 平台管理员无 tenant_name)

  // 账密 fields
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')

  const [state, setState] = useState<LoginState>('idle')
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})

  function validatePasswordForm(): boolean {
    const errors: Record<string, string> = {}
    if (!username.trim()) errors.username = '请输入用户名'
    if (!password) errors.password = '请输入密码'
    setFieldErrors(errors)
    return Object.keys(errors).length === 0
  }

  async function handleOidcBegin() {
    setFieldErrors({})
    setState('loading')
    try {
      const redirectUri = `${window.location.origin}/auth/callback`
      // 与 Console 共用 /auth/oidc/begin；平台管理员不传 tenant_name（Core 侧按缺省处理）
      const { data, error, response } = await coreApi.POST('/auth/oidc/begin', {
        body: { tenant_name: '', redirect_uri: redirectUri } as { tenant_name: string; redirect_uri: string },
      })
      if (error || !data || response.status !== 200) {
        const code = (error as { code?: string } | undefined)?.code
        if (code === 'IDP_UNAVAILABLE') {
          MessagePlugin.error('身份服务暂不可用，请稍后重试')
        } else if (!navigator.onLine) {
          MessagePlugin.error('网络异常，请稍后重试')
        } else {
          MessagePlugin.error((error as { message?: string } | undefined)?.message ?? '登录发起失败，请稍后重试')
        }
        setState('idle')
        return
      }
      saveOidcState(data.state)
      // remember_me 偏好先写入，callback 完成后据此选择 storage 介质
      saveRememberMe(rememberMe)
      setState('redirecting')
      window.location.assign(data.authorization_url)
    } catch {
      MessagePlugin.error('网络异常，请稍后重试')
      setState('idle')
    }
  }

  async function handlePasswordLogin() {
    if (!validatePasswordForm()) {
      setState('validating')
      return
    }
    setState('loading')
    try {
      const idempotencyKey = generateIdempotencyKey()
      const { data, error, response } = await coreApi.POST('/auth/platform/password/login', {
        body: { username: username.trim(), password, idempotency_key: idempotencyKey },
      })
      if (error || !data || response.status !== 200) {
        const code = (error as { code?: string } | undefined)?.code
        if (code === 'INVALID_CREDENTIALS') {
          MessagePlugin.error('用户名或密码错误')
        } else if (!navigator.onLine) {
          MessagePlugin.error('网络异常，请稍后重试')
        } else {
          MessagePlugin.error((error as { message?: string } | undefined)?.message ?? '登录失败，请稍后重试')
        }
        setPassword('')
        setState('idle')
        return
      }
      saveSession(data, rememberMe)
      setAuthToken(data.access_token)
      const returnTo = consumeReturnTo()
      const target = safeReturnTo(returnTo, '/')
      MessagePlugin.success('登录成功')
      // 硬跳转以确保 middleware 已注入新 token；BOSS basepath `/boss/` 会自动相对化
      window.location.assign(target)
    } catch {
      MessagePlugin.error('网络异常，请稍后重试')
      setPassword('')
      setState('idle')
    }
  }

  function handleSubmit() {
    if (tab === 'oidc') {
      void handleOidcBegin()
    } else {
      void handlePasswordLogin()
    }
  }

  function getConsoleLoginUrl(): string {
    // 跨端跳转：dev 用绝对 localhost:5173，prod 用 /login（Gateway 路由）
    const dev = typeof import.meta !== 'undefined' && Boolean((import.meta as { env?: { DEV?: boolean } }).env?.DEV)
    return dev ? 'http://localhost:5173/login' : '/login'
  }

  const loading = state === 'loading' || state === 'redirecting'

  return (
    <div className="auth-page">
      <Card className="auth-card" bordered>
        <h1 className="auth-card-title">ANI 平台运营台</h1>

        <Alert
          theme="info"
          closeBtn
          className="auth-card-alert"
          message="本入口仅供平台管理员。租户用户请使用 Console 登录。"
        />

        <Tabs
          value={tab}
          onChange={(v) => setTab(v as 'oidc' | 'password')}
          disabled={loading}
        >
          <Tabs.TabPanel value="oidc" label="企业登录">
            <Form
              labelAlign="top"
              colon={false}
              onSubmit={handleSubmit}
              disabled={loading}
            >
              <Form.FormItem name="remember_me">
                <Checkbox
                  checked={rememberMe}
                  onChange={(v) => setRememberMe(Boolean(v))}
                  disabled={loading}
                >
                  记住我
                </Checkbox>
              </Form.FormItem>

              <Button
                theme="primary"
                block
                loading={loading}
                onClick={handleSubmit}
                disabled={loading}
              >
                {state === 'redirecting' ? '跳转中…' : '登录'}
              </Button>

              <p className="auth-card-desc">将跳转到企业身份提供商完成认证</p>
            </Form>
          </Tabs.TabPanel>

          <Tabs.TabPanel value="password" label="账号密码">
            <Form
              labelAlign="top"
              colon={false}
              onSubmit={handleSubmit}
              disabled={loading}
            >
              <Form.FormItem
                label="用户名"
                name="username"
                requiredMark
                rules={[{ required: true, message: '请输入用户名' }]}
                status={fieldErrors.username ? 'error' : undefined}
                help={fieldErrors.username ?? undefined}
              >
                <Input
                  value={username}
                  onChange={(v) => setUsername(String(v ?? ''))}
                  maxlength={64}
                  clearable
                  placeholder="用户名"
                  disabled={loading}
                />
              </Form.FormItem>

              <Form.FormItem
                label="密码"
                name="password"
                requiredMark
                rules={[{ required: true, message: '请输入密码' }]}
                status={fieldErrors.password ? 'error' : undefined}
                help={fieldErrors.password ?? undefined}
              >
                <Input
                  type="password"
                  value={password}
                  onChange={(v) => setPassword(String(v ?? ''))}
                  placeholder="密码"
                  disabled={loading}
                />
              </Form.FormItem>

              <Form.FormItem name="remember_me">
                <Checkbox
                  checked={rememberMe}
                  onChange={(v) => setRememberMe(Boolean(v))}
                  disabled={loading}
                >
                  记住我
                </Checkbox>
              </Form.FormItem>

              <Button
                theme="primary"
                block
                loading={loading}
                onClick={handleSubmit}
                disabled={loading}
              >
                登录
              </Button>
            </Form>
          </Tabs.TabPanel>
        </Tabs>

        <p className="auth-card-desc">
          租户用户？
          <a href={getConsoleLoginUrl()}>进入 Console</a>
        </p>
      </Card>
    </div>
  )
}
