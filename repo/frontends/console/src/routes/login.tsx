import { createFileRoute, redirect } from '@tanstack/react-router'
import { useState } from 'react'
import { Button, Card, Checkbox, Form, Input, MessagePlugin, Tabs } from 'tdesign-react'
import { coreApi } from '@/api/coreClient'
import { setAuthToken } from '@/api/auth'
import {
  consumeReturnTo,
  isSessionValid,
  saveOidcState,
  saveSession,
  safeReturnTo,
} from '@/auth/session'

/**
 * `/login` — Plain 企业风登录卡（P0 OIDC + P1 账密 Tab）。
 *
 * 布局：`.auth-page` 全屏居中 + `Card.auth-card` max-width 400px。
 * - 企业登录（OIDC）：tenant_name + 记住我 → POST /auth/oidc/begin → IdP 跳转
 * - 账号密码（P1）：tenant_name + username + password + 记住我 → POST /auth/password/login
 *
 * 双 Tab 共用 remember_me 状态；Tab 切换保留 remember_me 偏好，不重置其他字段。
 * 错误码映射：TENANT_NOT_FOUND → 租户不存在；IDP_UNAVAILABLE → 身份服务暂不可用；
 * INVALID_CREDENTIALS → 用户名或密码错误（清密码）；网络异常统一提示。
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
type TabValue = 'oidc' | 'password'

function LoginPage() {
  const [tab, setTab] = useState<TabValue>('oidc')
  const [tenantName, setTenantName] = useState('')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [rememberMe, setRememberMe] = useState(false)
  const [state, setState] = useState<LoginState>('idle')
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})

  const loading = state === 'loading' || state === 'redirecting'

  function validateOidcForm(): boolean {
    const errors: Record<string, string> = {}
    if (!tenantName.trim()) errors.tenant_name = '请输入租户标识'
    setFieldErrors(errors)
    return Object.keys(errors).length === 0
  }

  function validatePasswordForm(): boolean {
    const errors: Record<string, string> = {}
    if (!tenantName.trim()) errors.tenant_name = '请输入租户标识'
    if (!username.trim()) errors.username = '请输入用户名'
    if (!password) errors.password = '请输入密码'
    setFieldErrors(errors)
    return Object.keys(errors).length === 0
  }

  async function handleOidcBegin() {
    if (!validateOidcForm()) {
      setState('validating')
      return
    }
    setState('loading')
    try {
      const redirectUri = `${window.location.origin}/auth/callback`
      const { data, error, response } = await coreApi.POST('/auth/oidc/begin', {
        body: { tenant_name: tenantName.trim(), redirect_uri: redirectUri },
      })
      if (error || !data || response.status !== 200) {
        const code = (error as { code?: string } | undefined)?.code
        if (code === 'TENANT_NOT_FOUND') {
          MessagePlugin.error('租户不存在，请检查租户标识')
        } else if (code === 'IDP_UNAVAILABLE') {
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
      // remember_me 偏好在跳转前写入，callback 完成后据此选择 storage 介质
      saveSession({ access_token: '', refresh_token: '', expires_in: 0 }, rememberMe)
      // 清掉空 token 占位，仅保留 remember_me 偏好
      sessionStorage.removeItem('console:access_token')
      sessionStorage.removeItem('console:refresh_token')
      sessionStorage.removeItem('console:expires_at')
      localStorage.removeItem('console:access_token')
      localStorage.removeItem('console:refresh_token')
      localStorage.removeItem('console:expires_at')
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
      const { data, error, response } = await coreApi.POST('/auth/password/login', {
        body: {
          tenant_name: tenantName.trim(),
          username: username.trim(),
          password,
        },
      })
      if (error || !data || response.status !== 200) {
        const code = (error as { code?: string } | undefined)?.code
        if (code === 'INVALID_CREDENTIALS') {
          MessagePlugin.error('用户名或密码错误')
          setPassword('')
        } else if (code === 'TENANT_NOT_FOUND') {
          MessagePlugin.error('租户不存在，请检查租户标识')
        } else if (!navigator.onLine) {
          MessagePlugin.error('网络异常，请稍后重试')
        } else {
          MessagePlugin.error((error as { message?: string } | undefined)?.message ?? '登录失败，请稍后重试')
        }
        setState('idle')
        return
      }
      saveSession(data, rememberMe)
      setAuthToken(data.access_token)
      const returnTo = consumeReturnTo()
      const target = safeReturnTo(returnTo, '/')
      MessagePlugin.success('登录成功')
      navigate(target)
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

  function navigate(target: string) {
    // 使用 location.assign 完成跳转，避免引入 router hook 增加耦合
    window.location.assign(target)
  }

  return (
    <div className="auth-page">
      <Card className="auth-card" bordered>
        <h1 className="auth-card-title">KuberCloud ANI</h1>

        <Tabs
          value={tab}
          onChange={(v) => setTab(v as TabValue)}
          disabled={loading}
        >
          <Tabs.TabPanel value="oidc" label="企业登录">
            <Form
              labelAlign="top"
              colon={false}
              onSubmit={handleSubmit}
              disabled={loading}
            >
              <Form.FormItem
                label="租户标识"
                name="tenant_name"
                requiredMark
                rules={[{ required: true, message: '请输入租户标识' }]}
                status={fieldErrors.tenant_name ? 'error' : undefined}
                help={fieldErrors.tenant_name ?? undefined}
              >
                <Input
                  value={tenantName}
                  onChange={(v) => setTenantName(String(v ?? ''))}
                  maxlength={128}
                  clearable
                  placeholder="租户标识"
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
                label="租户标识"
                name="tenant_name"
                requiredMark
                rules={[{ required: true, message: '请输入租户标识' }]}
                status={fieldErrors.tenant_name ? 'error' : undefined}
                help={fieldErrors.tenant_name ?? undefined}
              >
                <Input
                  value={tenantName}
                  onChange={(v) => setTenantName(String(v ?? ''))}
                  maxlength={128}
                  clearable
                  placeholder="租户标识"
                  disabled={loading}
                />
              </Form.FormItem>

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
          平台管理员？
          <a href={(import.meta as any).env?.DEV ? 'http://localhost:5174/boss/login' : '/boss/login'}>
            进入 BOSS
          </a>
        </p>
      </Card>
    </div>
  )
}
