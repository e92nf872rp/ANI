/**
 * ApiNotReadyAlert — 平台 API 未就绪全页 Alert 组件。
 *
 * 当平台计量 API 返回 404/501 时，展示全页 Alert，提示平台计量接口尚未上线。
 * 排行/图表区由父组件根据 visible 状态禁用或隐藏。
 *
 * 文案固定（UX §6.2 / §7.2）：
 * 「平台计量接口尚未上线，暂无法展示跨租户排行」
 */
import { Alert } from 'tdesign-react'

/** API 未就绪固定文案（UX §6.2 / §7.2） */
const API_NOT_READY_MESSAGE =
  '平台计量接口尚未上线，暂无法展示跨租户排行'

export interface ApiNotReadyAlertProps {
  /** 是否显示；父组件根据 API 404/501 判断 */
  visible: boolean
}

/**
 * 平台 API 未就绪全页 Alert。
 *
 * visible=true 时渲染 Warning Alert；visible=false 时不渲染。
 */
export function ApiNotReadyAlert({ visible }: ApiNotReadyAlertProps) {
  if (!visible) return null
  return (
    <Alert theme="warning" message={API_NOT_READY_MESSAGE} close={false} />
  )
}
