import { useCallback, useEffect, useState } from 'react'
import { Alert, Button, Loading, Space, Tag } from 'tdesign-react'
import { api } from '@/api/client'

type DemoInstance = {
  id: string
  name: string
  kind: 'vm' | 'container' | 'gpu_container'
  status: string
  provider: string
  resource_refs: string[]
}

type OpsResponse = {
  accepted: boolean
  session_id: string
  protocol: string
  connect_url: string
  output: string
  reason: string
  expires_at: string
}

type ShellExecResponse = {
  command: string
  output: string
  exit_code: number
  cwd: string
}

export function DemoConsolePage() {
  const params = new URLSearchParams(window.location.search)
  const instanceID = params.get('instance_id') ?? ''
  const protocol = params.get('protocol') ?? 'vnc'
  const [instance, setInstance] = useState<DemoInstance | null>(null)
  const [session, setSession] = useState<OpsResponse | null>(null)
  const [lines, setLines] = useState<string[]>(['Connecting to ANI demo VM console...'])
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const boot = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const loaded = await requestJSON<DemoInstance>(`/api/v1/demo/instances/${instanceID}`)
      const created = await requestJSON<OpsResponse>(`/api/v1/demo/instances/${instanceID}/console`, {
        method: 'POST',
        body: JSON.stringify({ protocol }),
      })
      setInstance(loaded)
      setSession(created)
      setLines(initialConsoleLines(loaded, created))
    } catch (err) {
      setError(err instanceof Error ? err.message : '控制台连接失败')
    } finally {
      setLoading(false)
    }
  }, [instanceID, protocol])

  useEffect(() => {
    boot()
  }, [boot])

  async function execCommand() {
    if (!instance) return
    const command = input.trim()
    if (!command) return
    if (command === 'clear') {
      setLines([`root@${instance.name}:~#`])
      setInput('')
      return
    }
    setLoading(true)
    setError('')
    try {
      const result = await requestJSON<ShellExecResponse>(`/api/v1/demo/instances/${instance.id}/console/exec`, {
        method: 'POST',
        body: JSON.stringify({ command }),
      })
      const output = result.output ? result.output.split('\n') : []
      const exitLine = result.exit_code === 0 ? [] : [`[exit ${result.exit_code}]`]
      setLines((current) => [...current, `root@${instance.name}:${shortCWD(result.cwd)}# ${command}`, ...output, ...exitLine])
      setInput('')
    } catch (err) {
      setError(err instanceof Error ? err.message : '命令执行失败')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="console-page">
      <header className="console-page-header">
        <div>
          <h1>{instance?.name ?? 'VM Console'}</h1>
          <div className="console-page-meta">
            <Tag theme={instance?.status === 'running' ? 'success' : 'warning'}>{instance?.status ?? 'connecting'}</Tag>
            <span>{instance?.provider ?? '-'}</span>
            <span>{session?.protocol ?? protocol}</span>
            <span>{session?.session_id ?? '-'}</span>
          </div>
        </div>
        <Space>
          <Button variant="outline" onClick={boot}>重新连接</Button>
          <Button variant="outline" onClick={() => window.close()}>关闭</Button>
        </Space>
      </header>

      {error && <Alert theme="error" message={error} style={{ marginBottom: 12 }} />}

      <Loading loading={loading}>
        <div className="console-shell console-shell-full">
          <div className="console-toolbar">
            <span>ANI VM OS Shell</span>
            <span>{session?.connect_url ?? '-'}</span>
            <span>expires {session?.expires_at ?? '-'}</span>
          </div>
          <pre className="console-screen console-screen-full">{lines.join('\n')}</pre>
          <div className="console-input-row">
            <span>root@{instance?.name ?? 'vm'}:~#</span>
            <input
              value={input}
              onChange={(event) => setInput(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Enter') {
                  event.preventDefault()
                  execCommand()
                }
              }}
              autoFocus
            />
            <Button size="small" onClick={execCommand}>执行</Button>
            <Button size="small" variant="outline" onClick={() => instance && session && setLines(initialConsoleLines(instance, session))}>重置</Button>
          </div>
        </div>
      </Loading>
    </div>
  )
}

type ApiResponse<T> = {
  data?: T
  error?: { message?: string }
}

type PathApiClient = {
  GET: (path: string) => Promise<ApiResponse<unknown>>
  POST: (path: string, init: { body?: unknown }) => Promise<ApiResponse<unknown>>
}

async function requestJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const demoApi = api as unknown as PathApiClient
  const method = init?.method ?? 'GET'
  if (method === 'POST') {
    const body = init?.body ? JSON.parse(String(init.body)) : undefined
    const response = await demoApi.POST(path.replace('/api/v1', ''), { body })
    if (response.error) throw new Error(response.error.message || '请求失败')
    return response.data as T
  }
  const response = await demoApi.GET(path.replace('/api/v1', ''))
  if (response.error) throw new Error(response.error.message || '请求失败')
  return response.data as T
}

function initialConsoleLines(instance: DemoInstance, session: OpsResponse) {
  return [
    'ANI Demo VM Console',
    'Connected to backend shell workspace.',
    `Instance: ${instance.name}`,
    `Provider: ${instance.provider}`,
    `Protocol: ${session.protocol || 'console'}`,
    `Resource: ${instance.resource_refs.join(', ') || '-'}`,
    `Session: ${session.session_id}`,
    '',
    'Commands execute on a real backend shell workspace for this demo VM.',
    'Try: pwd, ls -la, cat README.txt, uname -a, env | grep ANI_DEMO, touch hello.txt, cat hello.txt',
    '',
    `root@${instance.name}:~#`,
  ]
}

function shortCWD(cwd: string) {
  if (!cwd) return '~'
  const parts = cwd.split('/')
  return parts.length > 0 ? `.../${parts[parts.length - 1]}` : cwd
}
