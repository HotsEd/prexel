// Axios singleton with proactive refresh + 401 retry + request queue.
//
// Refresh strategy
// ----------------
// Refresh token lives in an HttpOnly cookie (set by the backend at
// Path=/auth/refresh). The Pinia auth store stores the access token in
// memory together with its absolute expiry timestamp. Before every request
// we check if the access token has < 60s of life left — if so, we trigger
// a refresh and queue the request behind it.
//
// On a 401 we attempt exactly one refresh-and-retry. If that fails we clear
// the auth state and let the router redirect to /login.

import axios, {
  type AxiosError,
  type AxiosInstance,
  type InternalAxiosRequestConfig,
} from 'axios'

let instance: AxiosInstance | null = null
let refreshPromise: Promise<string | null> | null = null

async function getAuthStore() {
  const { useAuthStore } = await import('@/stores/auth')
  return useAuthStore()
}

async function getRouter() {
  const mod = await import('@/router')
  return mod.default
}

async function doRefresh(opts: { clearOnFailure?: boolean } = {}): Promise<string | null> {
  if (refreshPromise) return refreshPromise
  refreshPromise = (async () => {
    try {
      const res = await axios.post('/api/v1/auth/refresh', null, {
        withCredentials: true,
      })
      const auth = await getAuthStore()
      auth.setToken(res.data.access_token, res.data.expires_in)
      return res.data.access_token as string
    } catch {
      const auth = await getAuthStore()
      if (opts.clearOnFailure !== false) {
        auth.clear()
      }
      return null
    } finally {
      refreshPromise = null
    }
  })()
  return refreshPromise
}

interface RetryableConfig extends InternalAxiosRequestConfig {
  _retry?: boolean
}

export function useApi(): AxiosInstance {
  if (instance) return instance

  instance = axios.create({
    baseURL: '/api/v1',
    withCredentials: true,
    timeout: 30_000,
    headers: { 'Content-Type': 'application/json' },
  })

  instance.interceptors.request.use(async (cfg) => {
    const auth = await getAuthStore()
    const now = Date.now()
    const expiresAt = auth.tokenExpiresAt
    const url = cfg.url || ''
    const isRefreshCall = url.includes('/auth/refresh')
    const isLoginCall = url.includes('/auth/login')
    const isChallengeCall = url.includes('/auth/2fa/challenge')

    if (!isRefreshCall && !isLoginCall && !isChallengeCall) {
      if (auth.accessToken && expiresAt && expiresAt - now < 60_000) {
        await doRefresh({ clearOnFailure: false })
      }
      if (auth.accessToken) {
        cfg.headers.set('Authorization', `Bearer ${auth.accessToken}`)
      }
    }
    return cfg
  })

  instance.interceptors.response.use(
    (r) => r,
    async (error: AxiosError) => {
      const original = error.config as RetryableConfig | undefined
      const url = original?.url || ''
      if (
        error.response?.status === 401 &&
        original &&
        !original._retry &&
        !url.includes('/auth/refresh') &&
        !url.includes('/auth/login') &&
        !url.includes('/auth/2fa/challenge')
      ) {
        original._retry = true
        const newToken = await doRefresh()
        if (newToken) {
          original.headers = original.headers ?? {}
          original.headers.Authorization = `Bearer ${newToken}`
          return instance!.request(original)
        }
        const router = await getRouter()
        if (router.currentRoute.value.path !== '/login') {
          router.push({ path: '/login' })
        }
      }
      return Promise.reject(error)
    },
  )

  return instance
}

/** Extracts a user-friendly message from an axios error. */
export function apiErrorMessage(err: unknown): string {
  if (axios.isAxiosError(err)) {
    const body = err.response?.data as { error?: string; message?: string } | undefined
    if (body?.message) return body.message
    if (body?.error) return body.error.replace(/_/g, ' ')
    if (err.response?.status) return `HTTP ${err.response.status}`
    return err.message
  }
  return String(err)
}
