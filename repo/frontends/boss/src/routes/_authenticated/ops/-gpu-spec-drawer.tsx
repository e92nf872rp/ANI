import {
  Alert,
  Button,
  Drawer,
  Form,
  Input,
  InputNumber,
  MessagePlugin,
  Radio,
  Select,
} from 'tdesign-react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect, useMemo } from 'react'
import { coreApi, newIdempotencyKey } from '@/api/coreClient'
import type { components } from '@/api/core-schema'

type GPUInventoryRecord = components['schemas']['GPUInventoryRecord']

// 规格管理 Drawer（UX §4.3 / §3.1 Flow 3a）
// 字段：spec_id / gpu_type（从节点标签派生）/ gpu_mode / shares / mb_per_share
// gpu_type 对齐节点标签校验由服务端执行（GPUTypeNotInNodes 422）
// shares / mb_per_share 在选定 gpu_type + gpu_mode 后自动填充
interface GpuSpecDrawerProps {
  visible: boolean
  // 整卡模式可选的 gpu_type 集合（来自 gpu_spec 节点标签）
  wholecardGpuTypeOptions: string[]
  // vGPU 模式可选的 gpu_type 集合（来自 gpu_sharing_spec 节点标签）
  vgpuGpuTypeOptions: string[]
  // inventory 原始列表，用于查选中 gpu_type 对应节点的显存和切分策略
  inventoryList: GPUInventoryRecord[]
  onClose: () => void
}

// 从 gpu-sharing-spec 标签值解析每份显存，如 "NVIDIA-RTX-4090-12285MiB" → 12285
function parseMiBFromSpecLabel(label: string): number | null {
  const m = label.match(/(\d+)MiB$/i)
  return m ? parseInt(m[1], 10) : null
}

// 从 gpu-sharing-policy 推断切分数：quarter=4, half=2, eighth=8, …
function sharesFromPolicy(policy: string): number | null {
  const map: Record<string, number> = {
    quarter: 4,
    half: 2,
    eighth: 8,
    sixth: 6,
    third: 3,
  }
  return map[policy] ?? null
}

export function GpuSpecDrawer({
  visible,
  wholecardGpuTypeOptions,
  vgpuGpuTypeOptions,
  inventoryList,
  onClose,
}: GpuSpecDrawerProps) {
  const [form] = Form.useForm()
  const qc = useQueryClient()

  const selectedGpuMode = Form.useWatch('gpu_mode', form) as 'wholecard' | 'vgpu' | undefined
  const selectedGpuType = Form.useWatch('gpu_type', form) as string | undefined

  // 根据当前 gpu_mode 选择对应的下拉选项
  const currentOptions = useMemo(() => {
    const opts = selectedGpuMode === 'vgpu' ? vgpuGpuTypeOptions : wholecardGpuTypeOptions
    return opts.map((t) => ({ label: t, value: t }))
  }, [selectedGpuMode, wholecardGpuTypeOptions, vgpuGpuTypeOptions])

  // 切换 gpu_mode 时清空 gpu_type，避免旧值残留导致匹配错误
  useEffect(() => {
    if (!visible) return
    // 仅在模式变化时清空 gpu_type（非初始设置）
    form.setFieldsValue({ gpu_type: '' })
  }, [selectedGpuMode, visible, form])

  // 选中 gpu_type + gpu_mode 后自动填充 shares / mb_per_share / memory_total_mb
  useEffect(() => {
    if (!selectedGpuType || !selectedGpuMode) return
    // 在 inventory 中找到匹配该 spec label 的设备记录
    const matched = inventoryList.find((item) => {
      if (selectedGpuMode === 'wholecard') return item.gpu_spec === selectedGpuType
      return item.gpu_sharing_spec === selectedGpuType
    })
    if (!matched) return

    if (selectedGpuMode === 'wholecard') {
      // 整卡：shares=1, mb_per_share=整卡显存, memory_total_mb=整卡显存
      const totalMB = matched.memory_total_mb ?? 0
      form.setFieldsValue({
        shares: 1,
        mb_per_share: totalMB,
        memory_total_mb: totalMB,
      })
    } else {
      // vGPU：从 sharing-spec 标签值解析每份显存，从 policy 推断切分数
      const perShare = parseMiBFromSpecLabel(matched.gpu_sharing_spec ?? '')
      const shares = sharesFromPolicy(matched.gpu_sharing_policy ?? '')
      form.setFieldsValue({
        shares: shares ?? 0,
        mb_per_share: perShare ?? 0,
        memory_total_mb: matched.memory_total_mb ?? 0,
      })
    }
  }, [selectedGpuType, selectedGpuMode, inventoryList, form])

  // 打开 Drawer 时重置表单
  useEffect(() => {
    if (visible) {
      form.reset()
      form.setFieldsValue({
        spec_id: '',
        gpu_type: '',
        gpu_mode: 'wholecard',
        shares: 1,
        mb_per_share: 0,
      })
    }
  }, [visible, form])

  const createSpecMutation = useMutation({
    mutationFn: async (vals: {
      spec_id: string
      gpu_type: string
      gpu_mode: 'wholecard' | 'vgpu'
      shares: number
      mb_per_share: number
      memory_total_mb?: number
    }) => {
      const idempotencyKey = newIdempotencyKey()
      const { data, error } = await coreApi.POST('/gpu-specs', {
        params: { header: { 'Idempotency-Key': idempotencyKey } },
        body: {
          spec_id: vals.spec_id,
          gpu_type: vals.gpu_type,
          gpu_mode: vals.gpu_mode,
          shares: vals.shares,
          mb_per_share: vals.mb_per_share,
          memory_total_mb: vals.memory_total_mb,
        },
      })
      if (error) throw new Error(error?.message ?? '规格创建失败')
      return data
    },
    onSuccess: () => {
      MessagePlugin.success('规格创建成功')
      qc.invalidateQueries({ queryKey: ['boss-gpu-specs'] })
      onClose()
    },
    onError: (err: Error) => {
      MessagePlugin.error(err.message || '规格创建失败')
    },
  })

  return (
    <Drawer
      visible={visible}
      header="新建规格"
      size="480px"
      onClose={onClose}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button theme="primary" loading={createSpecMutation.isPending} onClick={() => form.submit()}>
            确定
          </Button>
        </>
      }
    >
      <Form
        form={form}
        layout="vertical"
        onSubmit={(ctx) => {
          const values = ctx.fields as {
            spec_id: string
            gpu_type: string
            gpu_mode: 'wholecard' | 'vgpu'
            shares: number
            mb_per_share: number
            memory_total_mb?: number
          }
          createSpecMutation.mutate(values)
        }}
        resetType="empty"
      >
        <Form.FormItem
          label="规格 ID"
          name="spec_id"
          rules={[
            { required: true, message: '请填写规格 ID' },
            { pattern: /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/, message: '格式：小写字母数字，用连字符分隔' },
          ]}
        >
          <Input placeholder="格式 {gpu_type}-{mem}-{shares}" />
        </Form.FormItem>

        <Form.FormItem
          label="GPU 模式"
          name="gpu_mode"
          rules={[{ required: true, message: '请选择 GPU 模式' }]}
        >
          <Radio.Group
            options={[
              { label: '整卡 (wholecard)', value: 'wholecard' },
              { label: 'vGPU', value: 'vgpu' },
            ]}
          />
        </Form.FormItem>

        <Form.FormItem
          label="GPU 型号"
          name="gpu_type"
          rules={[{ required: true, message: '请选择 GPU 型号' }]}
        >
          <Select
            filterable
            clearable
            placeholder="从节点标签派生"
            options={currentOptions}
          />
        </Form.FormItem>

        {selectedGpuMode === 'vgpu' && (
          <Form.FormItem label="切分数" name="shares">
            <InputNumber min={1} step={1} readonly placeholder="自动填充" />
          </Form.FormItem>
        )}

        {selectedGpuMode === 'vgpu' && (
          <Form.FormItem label="每份显存 (MiB)" name="mb_per_share">
            <InputNumber min={1} step={1} readonly placeholder="自动填充" />
          </Form.FormItem>
        )}

        {selectedGpuMode === 'vgpu' && (
          <Alert
            theme="info"
            message="切分数和每份显存从节点标签自动派生：gpu-sharing-policy 决定切分数，gpu-sharing-spec 决定每份显存。"
            style={{ marginTop: 8 }}
          />
        )}

        {selectedGpuMode === 'wholecard' && (
          <Alert
            theme="info"
            message="整卡规格：独占整张物理 GPU，无需切分。"
            style={{ marginTop: 8 }}
          />
        )}
      </Form>
    </Drawer>
  )
}
