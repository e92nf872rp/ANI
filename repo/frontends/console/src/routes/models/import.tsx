import { createFileRoute } from '@tanstack/react-router'

export const Route = createFileRoute('/models/import')({
  component: ModelImport,
})

function ModelImport() {
  // TODO: implement multi-step import wizard (upload / HuggingFace / ModelScope)
  return (
    <div>
      <h2>导入模型</h2>
      <p style={{ color: 'var(--td-text-color-secondary)' }}>
        支持本地上传、HuggingFace 和 ModelScope 三种方式
      </p>
      {/* Step wizard implementation pending */}
      <div style={{ padding: 40, textAlign: 'center', color: '#aaa' }}>
        模型导入向导 — 开发中
      </div>
    </div>
  )
}
