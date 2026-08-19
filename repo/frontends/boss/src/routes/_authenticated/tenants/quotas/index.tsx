import { useEffect, useState } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Alert,
  Button,
  Empty,
  Input,
  MessagePlugin,
  Pagination,
  Radio,
} from 'tdesign-react'
import { AddIcon, SearchIcon } from 'tdesign-icons-react'
import {
  activateTenantPlan,
  deleteTenantPlan,
  disableTenantPlan,
  listTenantPlans,
} from '@/api/tenant-plans'
import type { ApiError, TenantPlanListItem } from '@/api/tenant-plans'
import { canWritePlatform } from '@/auth/permissions'
import { PlanTable } from '@/components/tenant-plans/PlanTable'

/** 对齐产品原型-7.23：`/boss/tenants/quotas` 列表 */
export const Route = createFileRoute('/_authenticated/tenants/quotas/')({
  component: TenantQuotasPage,
})

type StatusFilter = 'all' | 'active' | 'disabled' | 'draft'

function TenantQuotasPage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const canWrite = canWritePlatform()

  const [searchInput, setSearchInput] = useState('')
  const [search, setSearch] = useState('')
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all')
  const [limit, setLimit] = useState(20)
  const [cursorStack, setCursorStack] = useState<(string | undefined)[]>([
    undefined,
  ])
  const [pageIndex, setPageIndex] = useState(0)

  const status =
    statusFilter === 'all'
      ? undefined
      : (statusFilter as 'draft' | 'active' | 'disabled')

  useEffect(() => {
    setCursorStack([undefined])
    setPageIndex(0)
  }, [search, statusFilter, limit])

  const cursor = cursorStack[pageIndex]

  const listQuery = useQuery({
    queryKey: ['tenant-plans', { search, status, limit, cursor }],
    queryFn: () =>
      listTenantPlans({
        search: search || undefined,
        status,
        limit,
        cursor,
      }),
    retry: false,
  })

  const handleSearch = () => {
    setSearch(searchInput.trim())
  }

  const handleReset = () => {
    setSearchInput('')
    setSearch('')
    setStatusFilter('all')
  }

  const goCreate = () => {
    void navigate({ to: '/tenants/quotas/new' })
  }

  const goDetail = (plan: TenantPlanListItem) => {
    void navigate({
      to: '/tenants/quotas/$planId',
      params: { planId: plan.id },
    })
  }

  const activateMutation = useMutation({
    mutationFn: (plan: TenantPlanListItem) => activateTenantPlan(plan.id),
    onSuccess: () => {
      MessagePlugin.success('套餐已发布')
      queryClient.invalidateQueries({ queryKey: ['tenant-plans'] })
    },
    onError: (err: unknown) => {
      const e = err as ApiError
      if (e.code === 'PLAN_STATE_INVALID' || e.status === 409) {
        MessagePlugin.error('套餐状态不允许发布')
        return
      }
      MessagePlugin.error(e.message ?? '网络异常，请稍后重试')
    },
  })

  const disableMutation = useMutation({
    mutationFn: (plan: TenantPlanListItem) => disableTenantPlan(plan.id),
    onSuccess: () => {
      MessagePlugin.success('套餐已停用')
      queryClient.invalidateQueries({ queryKey: ['tenant-plans'] })
    },
    onError: (err: unknown) => {
      const e = err as ApiError
      if (e.code === 'PLAN_STATE_INVALID' || e.status === 409) {
        MessagePlugin.error('草稿状态不可直接停用')
        return
      }
      MessagePlugin.error(e.message ?? '网络异常，请稍后重试')
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (plan: TenantPlanListItem) => deleteTenantPlan(plan.id),
    onSuccess: () => {
      MessagePlugin.success('套餐已删除')
      queryClient.invalidateQueries({ queryKey: ['tenant-plans'] })
    },
    onError: (err: unknown) => {
      const e = err as ApiError
      if (e.code === 'TENANT_PLAN_IN_USE' || e.status === 409) {
        MessagePlugin.error('该套餐已关联租户，不可删除')
        return
      }
      MessagePlugin.error(e.message ?? '网络异常，请稍后重试')
    },
  })

  const items = listQuery.data?.items ?? []
  const nextCursor = listQuery.data?.next_cursor ?? null
  const totalHint =
    listQuery.data?.total ??
    (nextCursor
      ? (pageIndex + 2) * limit
      : pageIndex * limit + items.length)

  if (
    listQuery.isError &&
    (listQuery.error as ApiError)?.status !== 403
  ) {
    return (
      <div>
        <Alert
          theme="error"
          message={`数据加载失败：${(listQuery.error as ApiError)?.message ?? ''}`}
          operation={
            <Button variant="outline" onClick={() => listQuery.refetch()}>
              重试
            </Button>
          }
        />
      </div>
    )
  }

  return (
    <div>
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          marginBottom: 16,
        }}
      >
        <div>
          <h2 style={{ margin: 0 }}>配额策略</h2>
          <p
            style={{
              margin: '4px 0 0 0',
              color: 'var(--td-text-color-secondary)',
              fontSize: 14,
            }}
          >
            定义套餐并绑定到租户，限额变更自动同步
          </p>
        </div>
        {canWrite && (
          <Button theme="primary" icon={<AddIcon />} onClick={goCreate}>
            新建套餐
          </Button>
        )}
      </div>

      <div
        style={{
          display: 'flex',
          gap: 12,
          marginBottom: 12,
          flexWrap: 'wrap',
          alignItems: 'center',
        }}
      >
        <Input
          style={{ width: 280 }}
          clearable
          placeholder="按名称搜索"
          prefixIcon={<SearchIcon />}
          value={searchInput}
          onChange={(v) => setSearchInput(String(v))}
          onEnter={handleSearch}
        />
        <Button theme="primary" onClick={handleSearch}>
          搜索
        </Button>
        <Button variant="outline" onClick={handleReset}>
          重置
        </Button>
      </div>

      <div style={{ marginBottom: 16 }}>
        <Radio.Group
          variant="default-filled"
          value={statusFilter}
          onChange={(v) => setStatusFilter(String(v) as StatusFilter)}
        >
          <Radio.Button value="all">全部</Radio.Button>
          <Radio.Button value="active">启用</Radio.Button>
          <Radio.Button value="disabled">停用</Radio.Button>
          <Radio.Button value="draft">草稿</Radio.Button>
        </Radio.Group>
      </div>

      {!listQuery.isLoading && items.length === 0 && !listQuery.isError ? (
        <Empty
          description="还没有配额套餐"
          action={
            canWrite ? (
              <Button theme="primary" icon={<AddIcon />} onClick={goCreate}>
                新建套餐
              </Button>
            ) : undefined
          }
        />
      ) : (
        <>
          <PlanTable
            data={items}
            loading={listQuery.isLoading}
            canWrite={canWrite}
            onDetail={goDetail}
            onActivate={(plan) => activateMutation.mutate(plan)}
            onDisable={(plan) => disableMutation.mutate(plan)}
            onDelete={(plan) => deleteMutation.mutate(plan)}
          />
          <div
            style={{
              marginTop: 16,
              display: 'flex',
              justifyContent: 'flex-end',
            }}
          >
            <Pagination
              current={pageIndex + 1}
              pageSize={limit}
              total={totalHint}
              pageSizeOptions={[10, 20, 50, 100]}
              onChange={(pageInfo) => {
                const nextPage = pageInfo.current - 1
                if (nextPage > pageIndex && nextCursor) {
                  setCursorStack((stack) => {
                    const copy = stack.slice(0, pageIndex + 1)
                    copy.push(nextCursor)
                    return copy
                  })
                  setPageIndex(nextPage)
                } else if (nextPage < pageIndex && pageIndex > 0) {
                  setPageIndex(nextPage)
                }
              }}
              onPageSizeChange={(size) => setLimit(size)}
            />
          </div>
        </>
      )}
    </div>
  )
}
