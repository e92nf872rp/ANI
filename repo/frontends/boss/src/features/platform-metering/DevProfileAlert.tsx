/**
 * DevProfileAlert — dev_profile 横幅 Alert 组件。
 *
 * 当 API 响应含 dev_profile 且 real_provider=false 时，展示 Warning 横幅，
 * 提示当前为联调/开发环境数据，非生产真实计量。
 *
 * 文案固定（UX §6.1 / §7.2）：
 * 「当前为联调/开发环境数据，非生产真实计量；生产可用性待 live 验证。」
 */
import { Alert } from 'tdesign-react'
import type { components } from '@/api/core-schema'

/** CoreDevProfileInfo 类型（从 core-schema components.schemas 提取） */
type CoreDevProfileInfo = components['schemas']['CoreDevProfileInfo']

export interface DevProfileAlertProps {
  /** API 响应中的 dev_profile 信息；real_provider=false 时显示横幅 */
  dev_profile: CoreDevProfileInfo
}

/** dev_profile 横幅固定文案（UX §6.1 / §7.2） */
const DEV_PROFILE_MESSAGE =
  '当前为联调/开发环境数据，非生产真实计量；生产可用性待 live 验证。'

/**
 * dev_profile 横幅 Alert。
 *
 * 当 dev_profile.real_provider=false 时渲染 Warning Alert；
 * real_provider=true 时不渲染（生产环境无需提示）。
 */
export function DevProfileAlert({ dev_profile }: DevProfileAlertProps) {
  if (dev_profile?.real_provider) return null
  return (
    <Alert theme="warning" message={DEV_PROFILE_MESSAGE} close={false} />
  )
}
