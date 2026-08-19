/**
 * 配额维度展示顺序：GPU → CPU → 内存 → 存储 → Token → KB查询 → 推理服务上限 → 成员上限
 * 未在列表中的维度排在末尾（按 resource_type 字典序）。
 */
const QUOTA_RESOURCE_ORDER: Record<string, number> = {
  gpu_count: 10,
  cpu_core: 20,
  memory_gb: 30,
  storage_gb: 40,
  token_count: 50,
  kb_query_count: 60,
  inference_service_count: 70,
  member_count: 80,
}

export function compareQuotaResourceType(a: string, b: string): number {
  const ra = QUOTA_RESOURCE_ORDER[a] ?? 1000
  const rb = QUOTA_RESOURCE_ORDER[b] ?? 1000
  if (ra !== rb) return ra - rb
  return a.localeCompare(b)
}

export function sortQuotaItemsByResourceType<T extends { resource_type: string }>(
  items: T[],
): T[] {
  return [...items].sort((x, y) =>
    compareQuotaResourceType(x.resource_type, y.resource_type),
  )
}
