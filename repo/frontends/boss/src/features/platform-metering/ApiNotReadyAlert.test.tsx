/**
 * ApiNotReadyAlert 单元测试。
 *
 * 覆盖 ACCEPTANCE CRITERIA：
 * - visible=true 时显示全页 Alert
 * - visible=false 时不显示
 * - 文案固定为「平台计量接口尚未上线，暂无法展示跨租户排行」
 */
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ApiNotReadyAlert } from './ApiNotReadyAlert'

const API_NOT_READY_MESSAGE =
  '平台计量接口尚未上线，暂无法展示跨租户排行'

describe('ApiNotReadyAlert', () => {
  it('visible=true 时应显示 Alert', () => {
    render(<ApiNotReadyAlert visible={true} />)
    expect(screen.getByText(API_NOT_READY_MESSAGE)).toBeInTheDocument()
  })

  it('visible=false 时不应显示 Alert', () => {
    const { container } = render(<ApiNotReadyAlert visible={false} />)
    expect(container.firstChild).toBeNull()
  })

  it('文案应固定为 UX §6.2 / §7.2 定义的内容', () => {
    render(<ApiNotReadyAlert visible={true} />)
    expect(screen.getByText(API_NOT_READY_MESSAGE)).toBeInTheDocument()
  })
})
