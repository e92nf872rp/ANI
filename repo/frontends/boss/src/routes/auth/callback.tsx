import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import { Button, Card, Loading, MessagePlugin } from 'tdesign-react'
import { coreApi } from '@/api/coreClient'
import { setAuthToken } from '@/api/auth'
import {
  consumeOidcState,
  consumeReturnTo,
  getRememberMe,
  safeReturnTo,
  saveSession,
} from '@/auth/session'

/**
 * BOSS `/auth/callback` — OIDC 回调换 Token。
 *
 * 与 Console 共用 Core `/auth/token` 端点；state 通过 `boss:oidc_state` 隔离防冲突。
 */

export const Route = createFileRoute('/auth/callback')({
  component: CallbackPage,
})

type CallbackState =
  | 'loading'
  | 'error_missing_params'
  | 'error_state_mismatch'
  | 'error_token'
  | 'success'

function CallbackPage() {
  const [state, setState] = useState<CallbackState>('loading')
  const [errorMessage, setErrorMessage] = useState<string>('')
  const navigate = useNavigate()

  useEffect(() => {
    void runCallback()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  async function runCallback() {
    const params = new URLSearchParams(window.location.search)
    const code = params.get('code')
    const state = params.get('state')
    if (!code || !state) {
      setState('error_missing_params')
      return
    }
    const savedState = consumeOidcState()
    if (!savedState || savedState !== state) {
      setState('error_state_mismatch')
      return
    }
    const redirectUri = `${window.location.origin}/auth/callback`
    try {
      const { data, error, response } = await coreApi.POST('/auth/token', {
        body: { code, state, redirect_uri: redirectUri },
      })
      if (error || !data || response.status !== 200) {
        const message = (error as { message?: string } | undefined)?.message
        setErrorMessage(message ?? '登录验证失败，请重新登录')
        setState('error_token')
        return
      }
      const remember = getRememberMe()
      saveSession(data, remember)
      setAuthToken(data.access_token)
      const returnTo = consumeReturnTo()
      const target = safeReturnTo(returnTo, '/')
      MessagePlugin.success('登录成功')
      window.location.assign(target)
    } catch {
      setErrorMessage('登录验证失败，请重新登录')
      setState('error_token')
    }
  }

  function backToLogin() {
    navigate({ to: '/login' })
  }

  if (state === 'loading') {
    return (
      <div className="auth-page">
        <Loading loading text="正在完成登录..." />
      </div>
    )
  }

  return (
    <div className="auth-page">
      <Card className="auth-card" bordered>
        <h2 className="auth-card-title" style={{ textAlign: 'center' }}>登录未完成</h2>
        <p style={{ color: 'var(--td-text-color-secondary)', marginBottom: 24 }}>{errorMessage || '请重新登录'}</p>
        <Button theme="primary" block onClick={backToLogin}>返回登录</Button>
      </Card>
    </div>
  )
}
