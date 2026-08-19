/**
 * TenantDrilldownDrawer 单元测试。
 *
 * 覆盖 ACCEPTANCE CRITERIA（Issue #18）：
 * - 行操作「查看明细」→ 打开 Drawer
 * - FR-16: 钻取必须调用 GET /metering/usage/platform?tenant_id=...
 * - Drawer 内支持 loading / empty / error / forbidden 四态
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TenantDrilldownDrawer } from './TenantDrilldownDrawer'
import type { PlatformUsageFilter, PlatformUsageRow } from './types'
import * as coreClient from '@/api/coreClient'

// Mock coreApi.GET
vi.mock('@/api/coreClient', () => ({
  coreApi: {
    GET: vi.fn(),
  },
  setAuthToken: vi.fn(),
}))

const baseFilter: PlatformUsageFilter = {
  start_time: '2026-07-01T00:00:00.000Z',
  end_time: '2026-07-31T00:00:00.000Z',
  resource_type: 'token_input',
}

const mockRow: PlatformUsageRow = {
  tenant_id: 'tenant-123',
  resource_type: 'token_input',
  total_quantity: 500,
  unit: 'tokens',
  period: '2026-07-01',
}

/** 构造成功响应 mock（含 openapi-fetch 需要的 response 字段） */
function mockSuccessResponse(items: PlatformUsageRow[], total = items.length) {
  return {
    data: { items, total, dev_profile: { mode: 'local' as const, provider: '', real_provider: true } },
    error: undefined,
    response: new Response(),
  } as never
}

/** 构造错误响应 mock（含 status）
 *
 * openapi-fetch 的 error 只是响应体 JSON，不含 HTTP status；
 * status 在 response 对象上。hook 会从 response.status 读取并挂载到 error 上。
 */
function mockErrorResponse(status: number) {
  return {
    data: undefined,
    error: { message: 'error' },
    response: new Response(null, { status }),
  } as never
}

function renderWithQueryClient(ui: React.ReactElement) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: {
        // hook 内部定义了 retry 函数（403/404/501 不重试，5xx 重试 3 次），
        // 此处 retry: false 会被 hook 级 retry 覆盖；设置极短 retryDelay 加速测试
        retry: false,
        retryDelay: 0,
      },
    },
  })
  return render(
    <QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>,
  )
}

describe('TenantDrilldownDrawer', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('visible=true 且有 row 时应显示 Drawer 标题', () => {
    vi.mocked(coreClient.coreApi.GET).mockResolvedValue(mockSuccessResponse([]))
    renderWithQueryClient(
      <TenantDrilldownDrawer
        visible={true}
        onClose={vi.fn()}
        row={mockRow}
        baseFilter={baseFilter}
      />,
    )
    expect(screen.getByText('租户用量明细 · tenant-123')).toBeInTheDocument()
  })

  it('visible=false 时不应显示 Drawer 标题', () => {
    renderWithQueryClient(
      <TenantDrilldownDrawer
        visible={false}
        onClose={vi.fn()}
        row={mockRow}
        baseFilter={baseFilter}
      />,
    )
    expect(screen.queryByText('租户用量明细 · tenant-123')).not.toBeInTheDocument()
  })

  it('FR-16: 钻取应调用 GET /metering/usage/platform 且带 tenant_id', async () => {
    vi.mocked(coreClient.coreApi.GET).mockResolvedValue(mockSuccessResponse([]))
    renderWithQueryClient(
      <TenantDrilldownDrawer
        visible={true}
        onClose={vi.fn()}
        row={mockRow}
        baseFilter={baseFilter}
      />,
    )
    await waitFor(() => {
      expect(coreClient.coreApi.GET).toHaveBeenCalledWith(
        '/metering/usage/platform',
        expect.objectContaining({
          params: expect.objectContaining({
            query: expect.objectContaining({
              tenant_id: 'tenant-123',
              start_time: baseFilter.start_time,
              end_time: baseFilter.end_time,
              resource_type: 'token_input',
              group_by: 'day',
            }),
          }),
        }),
      )
    })
  })

  it('钻取应继承主查询的 resource_type', async () => {
    vi.mocked(coreClient.coreApi.GET).mockResolvedValue(mockSuccessResponse([]))
    renderWithQueryClient(
      <TenantDrilldownDrawer
        visible={true}
        onClose={vi.fn()}
        row={mockRow}
        baseFilter={baseFilter}
      />,
    )
    await waitFor(() => {
      expect(coreClient.coreApi.GET).toHaveBeenCalled()
      const call = vi.mocked(coreClient.coreApi.GET).mock.calls[0]
      const query = (call[1] as { params: { query: Record<string, unknown> } }).params.query
      expect(query.resource_type).toBe('token_input')
    })
  })

  it('钻取 group_by 应默认 day（FR-16）', async () => {
    vi.mocked(coreClient.coreApi.GET).mockResolvedValue(mockSuccessResponse([]))
    renderWithQueryClient(
      <TenantDrilldownDrawer
        visible={true}
        onClose={vi.fn()}
        row={mockRow}
        baseFilter={baseFilter}
      />,
    )
    await waitFor(() => {
      const call = vi.mocked(coreClient.coreApi.GET).mock.calls[0]
      const query = (call[1] as { params: { query: Record<string, unknown> } }).params.query
      expect(query.group_by).toBe('day')
    })
  })

  it('查询成功且有数据时应渲染明细内容', async () => {
    const items: PlatformUsageRow[] = [
      { tenant_id: 'tenant-123', resource_type: 'token_input', total_quantity: 100, unit: 'tokens', period: '2026-07-01' },
    ]
    vi.mocked(coreClient.coreApi.GET).mockResolvedValue(mockSuccessResponse(items, 1))
    renderWithQueryClient(
      <TenantDrilldownDrawer
        visible={true}
        onClose={vi.fn()}
        row={mockRow}
        baseFilter={baseFilter}
      />,
    )
    await waitFor(() => {
      expect(screen.getByText('100')).toBeInTheDocument()
    })
    // Drawer 内明细表不应有「查看明细」操作列（TESTING.md Issue #19 第5点）
    expect(screen.queryByText('查看明细')).not.toBeInTheDocument()
  })

  it('查询成功但 items 为空时应显示 Empty', async () => {
    vi.mocked(coreClient.coreApi.GET).mockResolvedValue(mockSuccessResponse([]))
    renderWithQueryClient(
      <TenantDrilldownDrawer
        visible={true}
        onClose={vi.fn()}
        row={mockRow}
        baseFilter={baseFilter}
      />,
    )
    await waitFor(() => {
      expect(screen.getByText('当前条件下暂无该租户用量数据')).toBeInTheDocument()
    })
  })

  it('查询 403 时应显示 forbidden Alert', async () => {
    vi.mocked(coreClient.coreApi.GET).mockResolvedValue(mockErrorResponse(403))
    renderWithQueryClient(
      <TenantDrilldownDrawer
        visible={true}
        onClose={vi.fn()}
        row={mockRow}
        baseFilter={baseFilter}
      />,
    )
    await waitFor(() => {
      expect(screen.getByText('无权限查看该租户用量')).toBeInTheDocument()
    })
  })

  it('查询 404/501 时应显示 api-not-ready Alert', async () => {
    vi.mocked(coreClient.coreApi.GET).mockResolvedValue(mockErrorResponse(404))
    renderWithQueryClient(
      <TenantDrilldownDrawer
        visible={true}
        onClose={vi.fn()}
        row={mockRow}
        baseFilter={baseFilter}
      />,
    )
    await waitFor(() => {
      expect(screen.getByText('平台计量接口尚未上线，暂无法展示租户明细')).toBeInTheDocument()
    })
  })

  it('查询 500 时应显示 error Alert + 重试按钮', async () => {
    vi.mocked(coreClient.coreApi.GET).mockResolvedValue(mockErrorResponse(500))
    renderWithQueryClient(
      <TenantDrilldownDrawer
        visible={true}
        onClose={vi.fn()}
        row={mockRow}
        baseFilter={baseFilter}
      />,
    )
    // hook 对 500 会重试 3 次（retryDelay=0），waitFor 需要足够 timeout
    await waitFor(
      () => {
        expect(screen.getByText('用量数据加载失败，请稍后重试')).toBeInTheDocument()
        expect(screen.getByText('重试')).toBeInTheDocument()
      },
      { timeout: 5000 },
    )
  })

  it('Drawer 内应渲染分组维度切换控件', async () => {
    vi.mocked(coreClient.coreApi.GET).mockResolvedValue(mockSuccessResponse([]))
    renderWithQueryClient(
      <TenantDrilldownDrawer
        visible={true}
        onClose={vi.fn()}
        row={mockRow}
        baseFilter={baseFilter}
      />,
    )
    await waitFor(() => {
      expect(screen.getByText('分组维度')).toBeInTheDocument()
    })
  })
})
