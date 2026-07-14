import { useEffect, useMemo, useState } from 'react'
import { Alert, Button, Card, Loading, Space, Statistic, Table, Tag } from 'tdesign-react'
import { api } from '@/api/client'

type DemoKind = 'vm' | 'container' | 'gpu_container'
type LifecycleAction = 'start' | 'stop' | 'restart' | 'resize' | 'delete'
type OpsAction = 'logs' | 'events' | 'metrics' | 'terminal' | 'exec'
type DetailTab = 'overview' | 'metrics' | 'network' | 'storage' | 'security' | 'events' | 'audit' | 'snapshot' | 'backup'
type ResourceDomain = '实例' | '镜像' | '网络' | '存储' | 'GPU 资源池' | '控制台会话' | '任务中心' | '审计'

type DemoInstance = {
  id: string
  tenant_id: string
  name: string
  kind: DemoKind
  status: string
  provider: string
  resource_refs: string[]
  endpoint: string
  created_at: string
  updated_at: string
}

type DemoManifest = {
  name: string
  kind: string
  provider: string
  content: string
}

type DemoCreateResponse = {
  instance: DemoInstance
  audit_id: string
  manifests: DemoManifest[]
  timeline: Array<{ name: string; status: string; detail: string }>
  demo_notice: string
}

type DemoListResponse = {
  items: DemoInstance[]
  total: number
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

const presets: Record<DemoKind, {
  label: string
  name: string
  cpu: string
  memory: string
  image: string
  boot_image: string
  gpu_model: string
  gpu_count: number
  provider: string
}> = {
  vm: {
    label: 'VM',
    name: 'demo-vm-01',
    cpu: '4',
    memory: '8Gi',
    image: '',
    boot_image: 'images/ubuntu-22.04.qcow2',
    gpu_model: '',
    gpu_count: 0,
    provider: 'KubeVirt',
  },
  container: {
    label: '容器',
    name: 'demo-app-01',
    cpu: '2',
    memory: '4Gi',
    image: 'registry.local/ani/demo-app:1.0.0',
    boot_image: '',
    gpu_model: '',
    gpu_count: 0,
    provider: 'Kubernetes',
  },
  gpu_container: {
    label: 'GPU 容器',
    name: 'demo-gpu-01',
    cpu: '8',
    memory: '32Gi',
    image: 'registry.local/ani/demo-gpu:1.0.0',
    boot_image: '',
    gpu_model: 'A100',
    gpu_count: 1,
    provider: 'Kubernetes + GPU',
  },
}

const lifecycleActions: Array<{ action: LifecycleAction; label: string; danger?: boolean }> = [
  { action: 'start', label: '启动' },
  { action: 'stop', label: '停止' },
  { action: 'restart', label: '重启' },
  { action: 'resize', label: '变配' },
  { action: 'delete', label: '删除', danger: true },
]

const resourceDomains: ResourceDomain[] = ['实例', '镜像', '网络', '存储', 'GPU 资源池', '控制台会话', '任务中心', '审计']
const detailTabs: Array<{ key: DetailTab; label: string }> = [
  { key: 'overview', label: '概览' },
  { key: 'metrics', label: '监控' },
  { key: 'network', label: '网络' },
  { key: 'storage', label: '存储' },
  { key: 'security', label: '安全' },
  { key: 'events', label: '事件' },
  { key: 'audit', label: '审计' },
  { key: 'snapshot', label: '快照' },
  { key: 'backup', label: '备份' },
]

export function InstanceDemoPage() {
  const [kind, setKind] = useState<DemoKind>('vm')
  const [instances, setInstances] = useState<DemoInstance[]>([])
  const [selected, setSelected] = useState<DemoInstance | null>(null)
  const [lastCreate, setLastCreate] = useState<DemoCreateResponse | null>(null)
  const [activity, setActivity] = useState('')
  const [filter, setFilter] = useState<'all' | DemoKind>('all')
  const [domain, setDomain] = useState<ResourceDomain>('实例')
  const [detailTab, setDetailTab] = useState<DetailTab>('overview')
  const [consoleProtocol, setConsoleProtocol] = useState('vnc')
  const [domainMessage, setDomainMessage] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const visibleInstances = useMemo(
    () => instances.filter((item) => filter === 'all' || item.kind === filter),
    [instances, filter],
  )
  const runningCount = useMemo(() => instances.filter((item) => item.status === 'running').length, [instances])
  const stoppedCount = useMemo(() => instances.filter((item) => item.status === 'stopped').length, [instances])
  const deletedCount = useMemo(() => instances.filter((item) => item.status === 'deleted').length, [instances])

  useEffect(() => {
    run(refresh)
  }, [])

  async function refresh() {
    const data = await requestJSON<DemoListResponse>('/api/v1/demo/instances')
    setInstances(data.items)
    setSelected((current) => {
      if (!current) return data.items[0] ?? null
      return data.items.find((item) => item.id === current.id) ?? data.items[0] ?? null
    })
  }

  async function createInstance(nextKind = kind) {
    const preset = presets[nextKind]
    await run(async () => {
      const response = await requestJSON<DemoCreateResponse>('/api/v1/demo/instances', {
        method: 'POST',
        body: JSON.stringify({
          kind: nextKind,
          name: uniqueName(preset.name),
          cpu: preset.cpu,
          memory: preset.memory,
          image: preset.image,
          boot_image: preset.boot_image,
          gpu_model: preset.gpu_model,
          gpu_count: preset.gpu_count,
          auto_start: true,
          description: 'ANI instance workspace',
        }),
      })
      setKind(nextKind)
      setLastCreate(response)
      setSelected(response.instance)
      setActivity(formatCreateResult(response))
      await refresh()
    })
  }

  async function lifecycle(instance: DemoInstance, action: LifecycleAction) {
    await run(async () => {
      const updated = await requestJSON<DemoInstance>(`/api/v1/demo/instances/${instance.id}/lifecycle`, {
        method: 'POST',
        body: JSON.stringify({ action, cpu: '6', memory: '12Gi' }),
      })
      setSelected(updated)
      setActivity(formatLifecycleResult(action, updated))
      await refresh()
    })
  }

  async function ops(instance: DemoInstance, action: OpsAction) {
    await run(async () => {
      const result = await requestJSON<OpsResponse>(`/api/v1/demo/instances/${instance.id}/ops/${action}`)
      setActivity(formatOpsResult(action, result))
    })
  }

  async function openConsole(instance: DemoInstance) {
    const popup = window.open('', '_blank')
    await run(async () => {
      const result = await requestJSON<OpsResponse>(`/api/v1/demo/instances/${instance.id}/console`, {
        method: 'POST',
        body: JSON.stringify({ protocol: consoleProtocol }),
      })
      setActivity(formatOpsResult('console', result))
      const url = `/demo-console?instance_id=${encodeURIComponent(instance.id)}&protocol=${encodeURIComponent(result.protocol || consoleProtocol)}`
      if (popup) {
        popup.location.href = url
      } else {
        window.open(url, '_blank', 'noopener,noreferrer')
      }
    })
  }

  async function run(task: () => Promise<void>) {
    setError('')
    setLoading(true)
    try {
      await task()
    } catch (err) {
      setError(err instanceof Error ? err.message : '请求失败')
    } finally {
      setLoading(false)
    }
  }

  const columns = [
    {
      title: '实例',
      colKey: 'name',
      cell: ({ row }: { row: DemoInstance }) => (
        <button className="link-button" onClick={() => setSelected(row)}>{row.name}</button>
      ),
    },
    { title: '类型', colKey: 'kind', cell: ({ row }: { row: DemoInstance }) => <Tag>{kindLabel(row.kind)}</Tag> },
    {
      title: '状态',
      colKey: 'status',
      cell: ({ row }: { row: DemoInstance }) => <Tag theme={statusTheme(row.status)}>{row.status}</Tag>,
    },
    { title: 'Provider', colKey: 'provider' },
    {
      title: '操作',
      colKey: 'actions',
      cell: ({ row }: { row: DemoInstance }) => (
        <Space>
          <Button size="small" variant="outline" onClick={() => lifecycle(row, 'start')}>启动</Button>
          <Button size="small" variant="outline" onClick={() => lifecycle(row, 'stop')}>停止</Button>
          {row.kind === 'vm' ? (
            <Button size="small" onClick={() => openConsole(row)}>控制台</Button>
          ) : (
            <Button size="small" variant="outline" onClick={() => ops(row, 'logs')}>日志</Button>
          )}
        </Space>
      ),
    },
  ]

  return (
    <div className="workspace-page">
      <div className="workspace-header">
        <div>
          <h1 className="page-title">实例工作台</h1>
          <div className="workspace-meta">
            <span>M1 Instance Fabric</span>
            <span>VM</span>
            <span>Container</span>
            <span>GPU Container</span>
          </div>
        </div>
        <Space>
          <Button variant="outline" onClick={() => run(refresh)}>刷新</Button>
          <Button onClick={() => createInstance(kind)}>创建 {kindLabel(kind)}</Button>
        </Space>
      </div>

      {error && <Alert theme="error" message={error} style={{ marginBottom: 16 }} />}

      <div className="summary-grid summary-grid-four">
        <Card><Statistic title="全部实例" value={instances.length} /></Card>
        <Card><Statistic title="运行中" value={runningCount} /></Card>
        <Card><Statistic title="已停止" value={stoppedCount} /></Card>
        <Card><Statistic title="已删除" value={deletedCount} /></Card>
      </div>

      <div className="resource-shell">
        <aside className="resource-nav">
          {resourceDomains.map((item) => (
            <button
              key={item}
              className={domain === item ? 'resource-nav-active' : ''}
              onClick={() => setDomain(item)}
            >
              {item}
            </button>
          ))}
        </aside>

        <div className="resource-content">
          {domain === '实例' ? (
            <div className="instance-layout">
        <section className="instance-create-panel">
          <h2>创建</h2>
          <div className="wizard-steps">
            <span>基础</span>
            <span>规格</span>
            <span>网络</span>
            <span>存储</span>
            <span>确认</span>
          </div>
          <div className="blueprint-list">
            {(Object.keys(presets) as DemoKind[]).map((item) => (
              <button
                key={item}
                className={`blueprint ${kind === item ? 'blueprint-active' : ''}`}
                onClick={() => setKind(item)}
              >
                <span>{presets[item].label}</span>
                <strong>{presets[item].cpu} vCPU / {presets[item].memory}</strong>
                <small>{presets[item].provider}</small>
              </button>
            ))}
          </div>
          <div className="create-spec">
            <label>
              名称
              <input value={uniqueName(presets[kind].name)} readOnly />
            </label>
            <label>
              镜像
              <input value={presets[kind].image || presets[kind].boot_image} readOnly />
            </label>
            <label>
              控制台协议
              <select value={consoleProtocol} onChange={(event) => setConsoleProtocol(event.target.value)}>
                <option value="vnc">VNC / noVNC</option>
                <option value="serial-console">Serial Console</option>
                <option value="cloud-console">Public Cloud Console</option>
                <option value="spice">SPICE</option>
                <option value="rdp">RDP</option>
              </select>
            </label>
          </div>
          <Button block onClick={() => createInstance(kind)}>创建 {kindLabel(kind)}</Button>
        </section>

        <section className="instance-main-panel">
          <div className="panel-toolbar">
            <Space>
              {(['all', 'vm', 'container', 'gpu_container'] as Array<'all' | DemoKind>).map((item) => (
                <Button
                  key={item}
                  size="small"
                  variant={filter === item ? 'base' : 'outline'}
                  onClick={() => setFilter(item)}
                >
                  {item === 'all' ? '全部' : kindLabel(item)}
                </Button>
              ))}
            </Space>
          </div>
          <Loading loading={loading}>
            <Table data={visibleInstances} columns={columns} rowKey="id" />
          </Loading>
        </section>

        <section className="instance-detail-panel">
          {selected ? (
            <>
              <div className="detail-heading">
                <div>
                  <h2>{selected.name}</h2>
                  <span>{selected.id}</span>
                </div>
                <Tag theme={statusTheme(selected.status)}>{selected.status}</Tag>
              </div>
              <dl className="detail-list">
                <dt>类型</dt><dd>{kindLabel(selected.kind)}</dd>
                <dt>Provider</dt><dd>{selected.provider}</dd>
                <dt>Tenant</dt><dd>{selected.tenant_id}</dd>
                <dt>资源</dt><dd>{selected.resource_refs.join(', ') || '-'}</dd>
                <dt>更新时间</dt><dd>{selected.updated_at || '-'}</dd>
              </dl>
              <div className="detail-tabs">
                {detailTabs.map((item) => (
                  <button
                    key={item.key}
                    className={detailTab === item.key ? 'detail-tab-active' : ''}
                    onClick={() => setDetailTab(item.key)}
                  >
                    {item.label}
                  </button>
                ))}
              </div>
              <div className="detail-tab-panel">
                {renderDetailTab(selected, detailTab)}
              </div>
              <div className="action-grid">
                {lifecycleActions.map((item) => (
                  <Button
                    key={item.action}
                    theme={item.danger ? 'danger' : 'default'}
                    variant={item.danger ? 'outline' : 'base'}
                    onClick={() => lifecycle(selected, item.action)}
                  >
                    {item.label}
                  </Button>
                ))}
              </div>
              {selected.kind === 'vm' ? (
                <div className="action-grid action-grid-two">
                  <Button onClick={() => openConsole(selected)}>打开控制台</Button>
                  <Button variant="outline" onClick={() => ops(selected, 'metrics')}>指标</Button>
                </div>
              ) : (
                <div className="action-grid action-grid-two">
                  <Button onClick={() => ops(selected, 'terminal')}>终端</Button>
                  <Button variant="outline" onClick={() => ops(selected, 'exec')}>执行</Button>
                  <Button variant="outline" onClick={() => ops(selected, 'logs')}>日志</Button>
                  <Button variant="outline" onClick={() => ops(selected, selected.kind === 'gpu_container' ? 'metrics' : 'events')}>监控</Button>
                </div>
              )}
            </>
          ) : (
            <div className="empty-panel">暂无实例</div>
          )}
        </section>

            </div>
          ) : (
            <ResourceDomainPanel
              domain={domain}
              message={domainMessage}
              onAction={(label, row) => setDomainMessage(`${domain} / ${label}: ${row.name} 已进入 Demo 流程。生产环境将由对应服务 API 执行。`)}
            />
          )}
        </div>
      </div>

      {domain === '实例' && <div className="bottom-grid">
        <Card title="执行结果">
          <pre className="manifest-view">{activity || '暂无输出'}</pre>
        </Card>
        <Card title="创建链路">
          {lastCreate ? (
            <div className="timeline-list">
              {lastCreate.timeline.map((step) => (
                <div key={step.name}>
                  <Tag theme={step.status === 'completed' || step.status === 'running' ? 'success' : 'default'}>{step.status}</Tag>
                  <strong>{step.name}</strong>
                  <span>{step.detail || '-'}</span>
                </div>
              ))}
            </div>
          ) : (
            <div className="empty-panel">暂无记录</div>
          )}
        </Card>
      </div>}
    </div>
  )
}

type DomainRow = {
  id: string
  name: string
  type: string
  status: string
  owner: string
  summary: string
}

function ResourceDomainPanel({
  domain,
  message,
  onAction,
}: {
  domain: ResourceDomain
  message: string
  onAction: (label: string, row: DomainRow) => void
}) {
  const [selectedIndex, setSelectedIndex] = useState(0)
  const rows = domainRows(domain)
  const selected = rows[selectedIndex] ?? rows[0]
  const columns = [
    {
      title: '名称',
      colKey: 'name',
      cell: ({ row }: { row: DomainRow }) => (
        <button className="link-button" onClick={() => setSelectedIndex(rows.findIndex((item) => item.id === row.id))}>
          {row.name}
        </button>
      ),
    },
    { title: '类型', colKey: 'type', cell: ({ row }: { row: DomainRow }) => <Tag>{row.type}</Tag> },
    { title: '状态', colKey: 'status', cell: ({ row }: { row: DomainRow }) => <Tag theme={row.status === '正常' || row.status === '运行中' ? 'success' : 'warning'}>{row.status}</Tag> },
    { title: '负责人', colKey: 'owner' },
  ]

  return (
    <div className="domain-page">
      <div className="domain-header">
        <div>
          <h2>{domain}</h2>
          <p>{domainIntro(domain)}</p>
        </div>
        <Space>
          <Button variant="outline" onClick={() => onAction('刷新', selected)}>刷新</Button>
          <Button onClick={() => onAction('新建', selected)}>新建</Button>
        </Space>
      </div>
      <div className="mock-metric-grid">
        {domainMetrics(domain).map((item) => (
          <Card key={item.label}>
            <div className="mock-metric">
              <span>{item.label}</span>
              <strong>{item.value}</strong>
            </div>
          </Card>
        ))}
      </div>
      <div className="mock-console-grid">
        <Card title={`${domain}列表`}>
          <Table data={rows} columns={columns} rowKey="id" />
        </Card>
        <Card title="详情与动作">
          <div className="mock-detail">
            <h2>{selected.name}</h2>
            <dl className="detail-list">
              <dt>ID</dt><dd>{selected.id}</dd>
              <dt>类型</dt><dd>{selected.type}</dd>
              <dt>状态</dt><dd>{selected.status}</dd>
              <dt>摘要</dt><dd>{selected.summary}</dd>
            </dl>
            <div className="action-grid">
              {domainActions(domain).map((label) => (
                <Button key={label} variant="outline" onClick={() => onAction(label, selected)}>{label}</Button>
              ))}
            </div>
            <pre className="mock-output">{message || '点击左侧资源域、资源行或操作按钮后，这里展示 Demo 反馈。'}</pre>
          </div>
        </Card>
      </div>
    </div>
  )
}

function renderDetailTab(instance: DemoInstance, tab: DetailTab) {
  switch (tab) {
    case 'overview':
      return <FeatureLines lines={[
        `运行状态: ${instance.status}`,
        `资源类型: ${kindLabel(instance.kind)}`,
        `调度 Provider: ${instance.provider}`,
        `访问端点: ${instance.endpoint || '未暴露'}`,
      ]} />
    case 'metrics':
      return <FeatureLines lines={[
        'CPU: 24%',
        'Memory: 41%',
        instance.kind === 'gpu_container' ? 'GPU: 63% / A100 x1' : 'GPU: N/A',
        '采样窗口: 5m',
      ]} />
    case 'network':
      return <FeatureLines lines={['tenant_vpc: connected', 'foundation_mesh: connected', 'management: connected', '安全策略: default-isolated']} />
    case 'storage':
      return <FeatureLines lines={instance.kind === 'vm' ? ['root_disk: 40Gi', 'storage_class: demo-standard', 'snapshot: available'] : ['ephemeral volume: enabled', 'image layer cache: enabled']} />
    case 'security':
      return <FeatureLines lines={['RBAC: tenant-admin', '操作审计: enabled', '控制台 TTL: 15m', '危险操作: 二次确认待接入']} />
    case 'events':
      return <FeatureLines lines={['Created instance plan', 'Admission accepted', 'Dry-run accepted', 'Status reconciled']} />
    case 'audit':
      return <FeatureLines lines={['create: demo user', 'lifecycle: recorded', 'ops: recorded', 'console: session scoped']} />
    case 'snapshot':
      return <FeatureLines lines={instance.kind === 'vm' ? ['创建快照', '恢复快照', '快照策略'] : ['容器实例暂不支持磁盘快照']} />
    case 'backup':
      return <FeatureLines lines={['备份策略: daily', '保留周期: 7 days', '恢复演练: pending']} />
    default:
      return null
  }
}

function FeatureLines({ lines }: { lines: string[] }) {
  return (
    <ul className="feature-lines">
      {lines.map((line) => <li key={line}>{line}</li>)}
    </ul>
  )
}

function domainIntro(domain: ResourceDomain) {
  const intro: Record<ResourceDomain, string> = {
    实例: '',
    镜像: '展示 VM 启动盘、容器镜像和模型运行镜像，演示导入、扫描、发布和回滚流程。',
    网络: '展示租户 VPC、基础网格、管理网络和安全策略，演示实例创建前的网络预置关系。',
    存储: '展示根盘、PVC、对象存储桶和快照策略，演示 VM 与容器对存储能力的不同依赖。',
    'GPU 资源池': '展示异构 GPU 库存、节点标签、分区策略和调度健康状态。',
    控制台会话: '展示 VM VNC/Serial/noVNC 会话和容器终端会话，演示审计和 TTL 管控。',
    任务中心: '展示创建、变配、删除、导入、索引等异步任务的阶段和重试入口。',
    审计: '展示用户、API Key、生命周期操作和控制台命令的审计轨迹。',
  }
  return intro[domain]
}

function domainMetrics(domain: ResourceDomain) {
  const metrics: Record<ResourceDomain, Array<{ label: string; value: string }>> = {
    实例: [],
    镜像: [
      { label: '镜像总数', value: '24' },
      { label: '已扫描', value: '21' },
      { label: '待发布', value: '3' },
      { label: '风险', value: '1' },
    ],
    网络: [
      { label: 'VPC', value: '7' },
      { label: '基础网格', value: '3' },
      { label: '策略', value: '18' },
      { label: '冲突', value: '0' },
    ],
    存储: [
      { label: '卷', value: '38' },
      { label: '快照', value: '14' },
      { label: '对象桶', value: '6' },
      { label: '容量', value: '7.4TB' },
    ],
    'GPU 资源池': [
      { label: 'GPU', value: '32' },
      { label: '可用', value: '11' },
      { label: '型号', value: '4' },
      { label: '健康', value: '98%' },
    ],
    控制台会话: [
      { label: '活跃会话', value: '5' },
      { label: 'VM', value: '3' },
      { label: '容器', value: '2' },
      { label: '审计', value: '100%' },
    ],
    任务中心: [
      { label: '运行中', value: '9' },
      { label: '成功', value: '128' },
      { label: '重试', value: '2' },
      { label: '失败', value: '1' },
    ],
    审计: [
      { label: '今日事件', value: '436' },
      { label: '高危', value: '2' },
      { label: 'API Key', value: '27' },
      { label: '导出任务', value: '1' },
    ],
  }
  return metrics[domain]
}

function domainActions(domain: ResourceDomain) {
  const actions: Record<ResourceDomain, string[]> = {
    实例: [],
    镜像: ['导入', '安全扫描', '发布', '回滚'],
    网络: ['创建 VPC', '绑定实例', '策略检查', '连通性测试'],
    存储: ['创建卷', '创建快照', '挂载预检', '恢复演练'],
    'GPU 资源池': ['发现资源', '调度预检', '隔离节点', '查看拓扑'],
    控制台会话: ['打开会话', '续期', '断开', '查看命令审计'],
    任务中心: ['查看阶段', '重试', '取消', '下载日志'],
    审计: ['筛选', '导出', '标记风险', '查看详情'],
  }
  return actions[domain]
}

function domainRows(domain: ResourceDomain): DomainRow[] {
  const rows: Record<ResourceDomain, DomainRow[]> = {
    实例: [],
    镜像: [
      { id: 'img-ubuntu', name: 'ubuntu-22.04-vm-golden', type: 'VM Boot Image', status: '正常', owner: 'platform', summary: '用于 VM 创建的基础启动盘镜像，已完成安全扫描。' },
      { id: 'img-app', name: 'registry.local/ani/demo-app:1.0.0', type: 'Container Image', status: '正常', owner: 'app-team', summary: '容器实例 Demo 镜像，支持日志、终端和执行入口。' },
      { id: 'img-gpu', name: 'registry.local/ani/demo-gpu:1.0.0', type: 'GPU Image', status: '待发布', owner: 'ai-team', summary: 'GPU 容器 Demo 镜像，声明 CUDA 和 GPU 资源需求。' },
    ],
    网络: [
      { id: 'net-vpc-a', name: 'tenant-a-vpc', type: 'Tenant VPC', status: '正常', owner: 'network', summary: '承载业务互通的租户 VPC 网络。' },
      { id: 'net-mesh', name: 'foundation-mesh', type: 'Foundation Mesh', status: '正常', owner: 'platform', summary: 'VM 与容器跨运行时互通的基础网格。' },
      { id: 'net-mgmt', name: 'management-plane', type: 'Management', status: '正常', owner: 'ops', summary: '控制台、监控、审计和运维访问使用的管理网络。' },
    ],
    存储: [
      { id: 'st-root', name: 'vm-root-standard', type: 'Root Disk', status: '正常', owner: 'storage', summary: 'VM 根盘存储类，支持快照和恢复演练。' },
      { id: 'st-pvc', name: 'container-workspace', type: 'PVC', status: '正常', owner: 'platform', summary: '容器工作目录和任务缓存卷。' },
      { id: 'st-obj', name: 'ani-model-artifacts', type: 'Object Bucket', status: '正常', owner: 'ai-team', summary: '模型、数据集和评估报告对象存储。' },
    ],
    'GPU 资源池': [
      { id: 'gpu-a100', name: 'a100-pool', type: 'NVIDIA A100', status: '正常', owner: 'scheduler', summary: 'A100 资源池，支持整卡和分区调度策略。' },
      { id: 'gpu-l40s', name: 'l40s-pool', type: 'NVIDIA L40S', status: '正常', owner: 'scheduler', summary: '图像和推理任务优先使用的 GPU 资源池。' },
      { id: 'gpu-other', name: 'heterogeneous-pool', type: 'Mixed GPU', status: '预检中', owner: 'scheduler', summary: '用于演示异构 GPU 发现和调度契约。' },
    ],
    控制台会话: [
      { id: 'sess-vnc', name: 'demo-vm-console-check/vnc', type: 'VM VNC', status: '运行中', owner: 'demo-user', summary: 'VM 控制台会话，生产环境替换为 noVNC/WebSocket 代理。' },
      { id: 'sess-serial', name: 'demo-vm-console-check/serial', type: 'Serial', status: '正常', owner: 'demo-user', summary: 'VM 串口控制台会话，用于启动诊断。' },
      { id: 'sess-container', name: 'demo-app-01/terminal', type: 'Container Terminal', status: '正常', owner: 'demo-user', summary: '容器终端会话，生产环境需要 WebSocket attach/exec。' },
    ],
    任务中心: [
      { id: 'task-create', name: '创建 VM: demo-vm-console-check', type: 'Create', status: '成功', owner: 'demo-user', summary: '规划、渲染、准入、dry-run、apply、状态回写全链路任务。' },
      { id: 'task-resize', name: '变配 VM: demo-vm-console-check', type: 'Resize', status: '运行中', owner: 'demo-user', summary: '演示生命周期变更的阶段化任务反馈。' },
      { id: 'task-index', name: '知识库索引: 产品文档库', type: 'Index', status: '重试中', owner: 'ai-team', summary: '后续 M3/M4 会接入真实索引任务。' },
    ],
    审计: [
      { id: 'audit-create', name: 'create workload instance', type: 'Instance', status: '正常', owner: 'demo-user', summary: '记录实例创建请求、资源计划和 apply 结果。' },
      { id: 'audit-console', name: 'open vm console', type: 'Console', status: '正常', owner: 'demo-user', summary: '记录控制台会话协议、TTL 和操作来源。' },
      { id: 'audit-key', name: 'api key validation', type: 'Auth', status: '正常', owner: 'platform', summary: '记录 API Key 校验和 RBAC 决策。' },
    ],
  }
  return rows[domain]
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

function kindLabel(kind: DemoKind) {
  if (kind === 'vm') return 'VM'
  if (kind === 'gpu_container') return 'GPU 容器'
  return '容器'
}

function statusTheme(status: string) {
  if (status === 'running') return 'success'
  if (status === 'deleted' || status === 'failed') return 'danger'
  if (status === 'stopped') return 'warning'
  return 'default'
}

function uniqueName(base: string) {
  return `${base}-${new Date().getMinutes().toString().padStart(2, '0')}${new Date().getSeconds().toString().padStart(2, '0')}`
}

function formatOpsResult(action: string, result: OpsResponse) {
  return [
    `action: ${action}`,
    `accepted: ${result.accepted}`,
    `protocol: ${result.protocol || '-'}`,
    `session_id: ${result.session_id || '-'}`,
    `connect_url: ${result.connect_url || '-'}`,
    `expires_at: ${result.expires_at || '-'}`,
    `reason: ${result.reason || '-'}`,
    '',
    result.output || '',
  ].join('\n')
}

function formatCreateResult(response: DemoCreateResponse) {
  return [
    `created: ${response.instance.name}`,
    `kind: ${response.instance.kind}`,
    `status: ${response.instance.status}`,
    `provider: ${response.instance.provider}`,
    `audit_id: ${response.audit_id}`,
    `resource_refs: ${response.instance.resource_refs.join(', ') || '-'}`,
    '',
    response.manifests?.[0]?.content || '',
  ].join('\n')
}

function formatLifecycleResult(action: string, instance: DemoInstance) {
  return [
    `lifecycle: ${action}`,
    `instance: ${instance.name}`,
    `kind: ${instance.kind}`,
    `status: ${instance.status}`,
    `provider: ${instance.provider}`,
    `resource_refs: ${instance.resource_refs.join(', ') || '-'}`,
  ].join('\n')
}
