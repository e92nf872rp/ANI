import { api } from '@/api/client'

type APIErrorPayload = {
  code?: string
  message?: string
  request_id?: string
  details?: Record<string, unknown>
}

/** A small error shape shared by the model detail page and its retry UI. */
export type ModelQueryError = Error & APIErrorPayload & { status?: number }

function queryError(error: unknown, response: Response, fallback: string): ModelQueryError {
  const payload = (error && typeof error === 'object' ? error : {}) as APIErrorPayload
  const result = new Error(payload.message || fallback) as ModelQueryError
  Object.assign(result, payload)
  result.status = response.status
  return result
}

export async function fetchModel(modelId: string) {
  const { data, error, response } = await api.GET('/models/{model_id}', {
    params: { path: { model_id: modelId } },
  })
  if (error || !data) {
    throw queryError(error, response, '模型详情加载失败')
  }
  return data
}

export async function fetchModelVersions(modelId: string, cursor?: string) {
  const query: { limit: number; cursor?: string } = { limit: 100 }
  if (cursor) query.cursor = cursor
  const { data, error, response } = await api.GET('/models/{model_id}/versions', {
    params: { path: { model_id: modelId }, query },
  })
  if (error || !data) {
    throw queryError(error, response, '模型版本加载失败')
  }
  return data
}
