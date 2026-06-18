import { api } from './client'
import { coreApi } from './coreClient'

let bearerToken: string | null = null
let middlewareAttached = false

const authMiddleware = {
  onRequest({ request }: { request: Request }) {
    if (bearerToken) {
      request.headers.set('Authorization', `Bearer ${bearerToken}`)
    }
    return request
  },
}

function ensureAuthMiddleware() {
  if (middlewareAttached) return
  api.use(authMiddleware)
  coreApi.use(authMiddleware)
  middlewareAttached = true
}

/** Attach a JWT token to Services and Core API requests. Call after login. */
export function setAuthToken(token: string) {
  bearerToken = token
  ensureAuthMiddleware()
}
