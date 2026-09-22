import { describe, expect, it, vi } from 'vitest'
import { api } from '@/api/client'
import { fetchModel, fetchModelVersions } from './modelQueries'

describe('model queries', () => {
  it('fetches model details with the path model id', async () => {
    const get = vi.spyOn(api, 'GET').mockResolvedValueOnce({
      data: { id: 'model-1', name: 'demo', source: 'upload', status: 'ready', created_at: '2026-01-01T00:00:00Z' },
      response: new Response(null, { status: 200 }),
    } as never)

    await expect(fetchModel('model-1')).resolves.toMatchObject({ id: 'model-1' })
    expect(get).toHaveBeenCalledWith('/models/{model_id}', {
      params: { path: { model_id: 'model-1' } },
    })
  })

  it('fetches versions with a bounded page and forwards the cursor', async () => {
    const get = vi.spyOn(api, 'GET').mockResolvedValueOnce({
      data: { items: [], next_cursor: null },
      response: new Response(null, { status: 200 }),
    } as never)

    await expect(fetchModelVersions('model-1', 'cursor-1')).resolves.toMatchObject({ items: [] })
    expect(get).toHaveBeenCalledWith('/models/{model_id}/versions', {
      params: { path: { model_id: 'model-1' }, query: { limit: 100, cursor: 'cursor-1' } },
    })
  })

  it('surfaces API errors instead of treating an empty response as success', async () => {
    vi.spyOn(api, 'GET').mockResolvedValueOnce({
      error: { code: 'NOT_FOUND', message: 'model not found' },
      response: new Response(null, { status: 404 }),
    } as never)

    await expect(fetchModel('missing')).rejects.toMatchObject({ code: 'NOT_FOUND' })
  })
})
