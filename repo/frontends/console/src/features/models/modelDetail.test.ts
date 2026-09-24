import { describe, expect, it } from 'vitest'
import { formatModelSize } from './modelDetail'

describe('model detail presentation', () => {
  it('formats version sizes for a human-readable table', () => {
    expect(formatModelSize(0)).toBe('0 B')
    expect(formatModelSize(1024)).toBe('1 KiB')
    expect(formatModelSize(1024 * 1024 * 2.5)).toBe('2.5 MiB')
  })

  it('uses an em dash for missing or invalid sizes', () => {
    expect(formatModelSize(undefined)).toBe('—')
    expect(formatModelSize(-1)).toBe('—')
    expect(formatModelSize(Number.NaN)).toBe('—')
  })
})
