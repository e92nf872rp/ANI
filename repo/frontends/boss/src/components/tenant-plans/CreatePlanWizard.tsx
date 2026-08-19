import { useEffect, useMemo, useState } from 'react'
import {
  Alert,
  Button,
  Form,
  Input,
  InputNumber,
  Skeleton,
  Steps,
  Table,
  Textarea,
} from 'tdesign-react'
import type { PrimaryTableCol } from 'tdesign-react'
import { useQuery } from '@tanstack/react-query'
import { listQuotaMeta } from '@/api/tenant-plans'
import type { PlanQuotaLimitInput, QuotaMetaItem } from '@/api/tenant-plans'
import { sortQuotaItemsByResourceType } from './quotaResourceOrder'

export interface CreatePlanFormValues {
  code: string
  name: string
  description?: string
  /** 始终携带全部配额元数据维度；未填 total 传 null（后端用 default_quota） */
  quota_limits: PlanQuotaLimitInput[]
}

interface CreatePlanWizardProps {
  submitting: boolean
  onSubmit: (values: CreatePlanFormValues) => void
  onCancel: () => void
}

const CODE_PATTERN = /^[a-z0-9-]{3,40}$/

/** 对齐产品原型-7.23：名称与编码 → 限额配置 → 确认发布（独立页） */
export function CreatePlanWizard({
  submitting,
  onSubmit,
  onCancel,
}: CreatePlanWizardProps) {
  const [form] = Form.useForm()
  const [step, setStep] = useState(0)
  /** resource_type → 用户填写的 total；未填为 null */
  const [totals, setTotals] = useState<Record<string, number | null>>({})
  const [draft, setDraft] = useState<{
    code: string
    name: string
    description?: string
  }>({ code: '', name: '', description: '' })

  const metaQuery = useQuery({
    queryKey: ['quota-meta'],
    queryFn: listQuotaMeta,
    retry: false,
  })

  const metaItems = useMemo(
    () => sortQuotaItemsByResourceType(metaQuery.data?.items ?? []),
    [metaQuery.data?.items],
  )

  useEffect(() => {
    if (metaItems.length === 0) return
    setTotals((prev) => {
      const next: Record<string, number | null> = {}
      for (const m of metaItems) {
        next[m.resource_type] =
          m.resource_type in prev ? prev[m.resource_type] : null
      }
      return next
    })
  }, [metaItems])

  const buildQuotaLimits = (): PlanQuotaLimitInput[] =>
    metaItems.map((m) => ({
      resource_type: m.resource_type,
      total:
        totals[m.resource_type] === undefined || totals[m.resource_type] === null
          ? null
          : totals[m.resource_type],
    }))

  const goNextFromBase = async () => {
    const valid = await form.validate()
    if (valid !== true) return
    const values = form.getFieldsValue(true) as {
      code: string
      name: string
      description?: string
    }
    setDraft({
      code: values.code,
      name: values.name,
      description: values.description?.trim() || undefined,
    })
    setStep(1)
  }

  const goNextFromQuota = () => {
    if (metaQuery.isLoading) return
    if (metaQuery.isError) return
    if (metaItems.length === 0) return
    setStep(2)
  }

  const handleConfirm = () => {
    onSubmit({
      ...draft,
      quota_limits: buildQuotaLimits(),
    })
  }

  return (
    <div style={{ maxWidth: 720 }}>
      <Steps
        current={step}
        options={[
          { title: '名称与编码' },
          { title: '限额配置' },
          { title: '确认发布' },
        ]}
        style={{ marginBottom: 28 }}
      />

      {step === 0 && (
        <Form form={form} layout="vertical" resetType="empty" disabled={submitting}>
          <Form.FormItem
            label="套餐名称"
            name="name"
            initialData={draft.name}
            rules={[
              { required: true, message: '请输入名称' },
              { max: 64, message: '最多 64 个字符' },
            ]}
          >
            <Input placeholder="如：标准套餐" maxlength={64} />
          </Form.FormItem>
          <Form.FormItem
            label="套餐编码"
            name="code"
            initialData={draft.code}
            rules={[
              { required: true, message: '请输入套餐编码' },
              {
                pattern: CODE_PATTERN,
                message: '仅允许小写字母、数字、连字符，长度 3–40',
              },
            ]}
          >
            <Input placeholder="例如 standard-plan" maxlength={40} />
          </Form.FormItem>
          <Form.FormItem label="说明" name="description" initialData={draft.description}>
            <Textarea
              placeholder="可选说明"
              maxlength={512}
              autosize={{ minRows: 2, maxRows: 4 }}
            />
          </Form.FormItem>
        </Form>
      )}

      {step === 1 && (
        <QuotaLimitsStep
          loading={metaQuery.isLoading}
          error={metaQuery.isError}
          errorMessage={(metaQuery.error as { message?: string } | null)?.message}
          items={metaItems}
          totals={totals}
          onRetry={() => void metaQuery.refetch()}
          onChangeTotal={(resourceType, total) => {
            setTotals((prev) => ({ ...prev, [resourceType]: total }))
          }}
        />
      )}

      {step === 2 && (
        <div style={{ fontSize: 14, lineHeight: 1.8 }}>
          <div>
            <b>{draft.name}</b>（{draft.code}）
          </div>
          <div style={{ color: 'var(--td-text-color-secondary)' }}>
            {draft.description || '无说明'}
          </div>
          <div style={{ marginTop: 12 }}>
            <div style={{ marginBottom: 4 }}>限额维度：</div>
            {metaItems.map((m) => {
              const t = totals[m.resource_type]
              return (
                <div key={m.resource_type}>
                  {m.display_name}（{m.resource_type}）：
                  {t == null ? `默认 ${m.default_quota}` : t}
                  {m.unit ? ` ${m.unit}` : ''}
                </div>
              )
            })}
          </div>
          <div style={{ marginTop: 8, color: 'var(--td-text-color-secondary)' }}>
            未填写的维度会以 total=null 提交，后端按 default_quota 落库。提交后状态为草稿，需「发布」后才可分配给租户。
          </div>
        </div>
      )}

      <div
        style={{
          marginTop: 32,
          display: 'flex',
          gap: 8,
          justifyContent: 'flex-end',
        }}
      >
        {step === 0 ? (
          <>
            <Button variant="outline" onClick={onCancel} disabled={submitting}>
              取消
            </Button>
            <Button theme="primary" onClick={() => void goNextFromBase()}>
              下一步
            </Button>
          </>
        ) : step === 1 ? (
          <>
            <Button variant="outline" onClick={() => setStep(0)} disabled={submitting}>
              上一步
            </Button>
            <Button
              theme="primary"
              disabled={metaQuery.isLoading || metaQuery.isError || metaItems.length === 0}
              onClick={goNextFromQuota}
            >
              下一步
            </Button>
          </>
        ) : (
          <>
            <Button variant="outline" onClick={() => setStep(1)} disabled={submitting}>
              上一步
            </Button>
            <Button theme="primary" loading={submitting} onClick={handleConfirm}>
              确认创建
            </Button>
          </>
        )}
      </div>
    </div>
  )
}

function QuotaLimitsStep({
  loading,
  error,
  errorMessage,
  items,
  totals,
  onRetry,
  onChangeTotal,
}: {
  loading: boolean
  error: boolean
  errorMessage?: string
  items: QuotaMetaItem[]
  totals: Record<string, number | null>
  onRetry: () => void
  onChangeTotal: (resourceType: string, total: number | null) => void
}) {
  const columns: PrimaryTableCol<QuotaMetaItem>[] = useMemo(
    () => [
      { colKey: 'resource_type', title: '维度', minWidth: 140 },
      { colKey: 'display_name', title: '展示名', minWidth: 120 },
      {
        colKey: 'total',
        title: '限额',
        width: 240,
        cell: ({ row }) => (
          <InputNumber
            theme="normal"
            style={{ width: 200 }}
            min={0}
            decimalPlaces={row.is_discrete ? 0 : undefined}
            placeholder={String(row.default_quota)}
            value={totals[row.resource_type] ?? undefined}
            onChange={(v) => {
              onChangeTotal(
                row.resource_type,
                v === undefined || v === null || v === '' ? null : Number(v),
              )
            }}
          />
        ),
      },
      { colKey: 'unit', title: '单位', width: 80 },
    ],
    [totals, onChangeTotal],
  )

  if (loading) {
    return <Skeleton animation="gradient" style={{ height: 160 }} />
  }

  if (error) {
    return (
      <Alert
        theme="error"
        message={`配额元数据加载失败：${errorMessage ?? ''}`}
        operation={
          <Button variant="outline" onClick={onRetry}>
            重试
          </Button>
        }
      />
    )
  }

  if (items.length === 0) {
    return <Alert theme="warning" message="暂无可用配额维度，请先在 Core 启用配额元数据" />
  }

  return (
    <div>
      <div
        style={{
          color: 'var(--td-text-color-secondary)',
          fontSize: 13,
          marginBottom: 12,
        }}
      >
        以下为全部启用的配额维度，均会提交给后端；输入框为空时按占位默认值（传 null）落库。
      </div>
      <Table
        data={items}
        columns={columns}
        rowKey="resource_type"
        bordered
        size="small"
      />
    </div>
  )
}
