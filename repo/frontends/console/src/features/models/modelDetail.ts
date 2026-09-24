export function formatModelSize(bytes?: number | null): string {
  if (bytes === undefined || bytes === null || !Number.isFinite(bytes) || bytes < 0) {
    return '—'
  }
  if (bytes < 1024) return `${bytes} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB']
  let value = bytes
  let unit = units[0]
  for (let index = 0; index < units.length && value >= 1024; index += 1) {
    value /= 1024
    unit = units[index] ?? unit
  }
  const rounded = Number.isInteger(value) ? value.toString() : value.toFixed(1).replace(/\.0$/, '')
  return `${rounded} ${unit}`
}

export function formatModelDate(value?: string | null): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return new Intl.DateTimeFormat('zh-CN', {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(date)
}

export function formatModelChecksum(value?: string | null): string {
  if (!value) return '—'
  if (value.length <= 20) return value
  return `${value.slice(0, 12)}…${value.slice(-8)}`
}
