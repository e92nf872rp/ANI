/**
 * DevProfileAlert 单元测试。
 *
 * 覆盖 ACCEPTANCE CRITERIA：
 * - real_provider=false 时显示 Warning 横幅
 * - real_provider=true 时不显示
 * - 文案固定为「当前为联调/开发环境数据，非生产真实计量；生产可用性待 live 验证。」
 */
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { DevProfileAlert } from './DevProfileAlert'
import type { components } from '@/api/core-schema'

type CoreDevProfileInfo = components['schemas']['CoreDevProfileInfo']

const DEV_PROFILE_MESSAGE =
  '当前为联调/开发环境数据，非生产真实计量；生产可用性待 live 验证。'

describe('DevProfileAlert', () => {
  it('real_provider=false 时应显示横幅', () => {
    const devProfile: CoreDevProfileInfo = {
      mode: 'local',
      provider: 'local_metering',
      real_provider: false,
    }
    render(<DevProfileAlert dev_profile={devProfile} />)
    expect(screen.getByText(DEV_PROFILE_MESSAGE)).toBeInTheDocument()
  })

  it('real_provider=true 时不应显示横幅', () => {
    const devProfile: CoreDevProfileInfo = {
      mode: 'real',
      provider: 'kubernetes_rest',
      real_provider: true,
    }
    const { container } = render(<DevProfileAlert dev_profile={devProfile} />)
    expect(container.firstChild).toBeNull()
  })

  it('文案应固定为 UX §6.1 / §7.2 定义的内容', () => {
    const devProfile: CoreDevProfileInfo = {
      mode: 'local',
      provider: 'local',
      real_provider: false,
    }
    render(<DevProfileAlert dev_profile={devProfile} />)
    expect(screen.getByText(DEV_PROFILE_MESSAGE)).toBeInTheDocument()
  })
})
