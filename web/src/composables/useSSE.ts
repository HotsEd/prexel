/**
 * useSSE — SSE client built on fetch + ReadableStream so we can
 * attach an `Authorization: Bearer …` header. Native `EventSource`
 * can't carry custom headers, and our backend authenticates SSE
 * endpoints with JWT.
 *
 * Contract:
 *
 *   - Connect: pulls the in-memory access token from the auth store
 *     and sends it as `Authorization: Bearer <jwt>`. If a
 *     `Last-Event-ID` is known from the previous run, replays it via
 *     the standard header so the server can resume.
 *
 *   - 401 handling: when the fetch returns 401 we ask the auth store
 *     to refresh, and (on success) reopen with the new token. If the
 *     refresh fails, the stream stays closed and the next caller is
 *     expected to push the operator to /login (the axios interceptor
 *     in useApi does this automatically for REST calls).
 *
 *   - Reconnect: backoff is exponential with jitter — 1s, 2s, 4s,
 *     8s, 16s, capped at 30s. The attempt counter resets to 0 the
 *     first time we successfully read a frame from a new connection,
 *     so a stable stream that intermittently drops doesn't drift
 *     into the 30s tail.
 *
 *   - Teardown: `close()` aborts the in-flight fetch and clears any
 *     pending reconnect timer; the component-level `onBeforeUnmount`
 *     does the same. Safe to call multiple times.
 */

import { onBeforeUnmount, ref, shallowRef, type Ref } from 'vue'
import { useAuthStore } from '@/stores/auth'

export interface SSEEvent<T = unknown> {
  id?: string
  event: string
  data: T
}

export interface UseSSEOpts<T> {
  onEvent?: (ev: SSEEvent<T>) => void
  onOpen?: () => void
  onError?: (err: unknown) => void
  immediate?: boolean
  parseJson?: boolean
}

export function useSSE<T = unknown>(
  url: string | Ref<string>,
  opts: UseSSEOpts<T> = {},
) {
  const events = shallowRef<SSEEvent<T>[]>([])
  const connected = ref(false)
  const lastEventId = ref<string | null>(null)
  const error = ref<unknown>(null)

  let abort: AbortController | null = null
  let reconnectAttempts = 0
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null
  let closed = false
  // We retry the refresh at most once per (re)connect attempt. Without
  // this guard a backend that returns 401 even after a fresh token
  // would loop refresh + reconnect indefinitely.
  let refreshAttempted = false

  const parseJson = opts.parseJson !== false

  function resolveUrl() {
    return typeof url === 'string' ? url : url.value
  }

  async function connect() {
    if (closed) return
    abort = new AbortController()
    const auth = useAuthStore()

    const headers: Record<string, string> = {
      Accept: 'text/event-stream',
      'Cache-Control': 'no-cache',
    }
    if (auth.accessToken) headers.Authorization = `Bearer ${auth.accessToken}`
    if (lastEventId.value) headers['Last-Event-ID'] = lastEventId.value

    try {
      const res = await fetch(resolveUrl(), {
        method: 'GET',
        headers,
        credentials: 'include',
        signal: abort.signal,
      })

      // 401: try a single refresh. If it succeeds, reopen with the
      // new token; otherwise let the standard reconnect/backoff loop
      // surface the error so the parent UI can decide what to do.
      if (res.status === 401 && !refreshAttempted) {
        refreshAttempted = true
        const newToken = await tryRefresh()
        if (newToken) {
          // Don't count the refresh-driven reconnect against backoff.
          if (!closed) void connect()
          return
        }
      }

      if (!res.ok || !res.body) {
        throw new Error(`SSE HTTP ${res.status}`)
      }
      connected.value = true
      opts.onOpen?.()

      const reader = res.body.pipeThrough(new TextDecoderStream()).getReader()
      let buf = ''
      let curId: string | undefined
      let curEvent = 'message'
      const dataParts: string[] = []
      let firstFrameSeen = false

      const flush = () => {
        if (dataParts.length === 0 && !curId) {
          curEvent = 'message'
          return
        }
        const raw = dataParts.join('\n')
        let data: T
        if (parseJson && raw.length > 0) {
          try { data = JSON.parse(raw) as T } catch { data = raw as unknown as T }
        } else {
          data = raw as unknown as T
        }
        if (curId) lastEventId.value = curId
        const ev: SSEEvent<T> = { id: curId, event: curEvent, data }
        events.value = [...events.value, ev]
        opts.onEvent?.(ev)
        if (!firstFrameSeen) {
          // Healthy stream: reset both the backoff counter and the
          // one-shot refresh guard so future 401s after a token
          // rotation will trigger a fresh refresh.
          firstFrameSeen = true
          reconnectAttempts = 0
          refreshAttempted = false
        }
        curId = undefined
        curEvent = 'message'
        dataParts.length = 0
      }

      while (true) {
        const { value, done } = await reader.read()
        if (done) break
        buf += value
        let idx: number
        while ((idx = buf.indexOf('\n')) >= 0) {
          let line = buf.slice(0, idx)
          buf = buf.slice(idx + 1)
          if (line.endsWith('\r')) line = line.slice(0, -1)
          if (line === '') { flush(); continue }
          if (line.startsWith(':')) continue
          const colon = line.indexOf(':')
          let field: string, val: string
          if (colon === -1) { field = line; val = '' }
          else {
            field = line.slice(0, colon)
            val = line.slice(colon + 1)
            if (val.startsWith(' ')) val = val.slice(1)
          }
          if (field === 'id') curId = val
          else if (field === 'event') curEvent = val
          else if (field === 'data') dataParts.push(val)
        }
      }
      connected.value = false
      if (!closed) scheduleReconnect()
    } catch (err: unknown) {
      connected.value = false
      if ((err as { name?: string } | undefined)?.name === 'AbortError') return
      error.value = err
      opts.onError?.(err)
      if (!closed) scheduleReconnect()
    }
  }

  /**
   * Ask the auth store to refresh its access token. Returns the new
   * token on success, null when the refresh failed. Auth store owns
   * the actual `/auth/refresh` call — we just trigger it and read
   * `accessToken` afterwards.
   */
  async function tryRefresh(): Promise<string | null> {
    try {
      const auth = useAuthStore()
      // Dynamic import to avoid a circular dep with useApi (which
      // also drives refresh from REST 401s).
      const { authService } = await import('@/services/auth')
      const data = await authService.refresh()
      auth.setToken(data.access_token, data.expires_in)
      return auth.accessToken
    } catch {
      return null
    }
  }

  /**
   * Exponential backoff: 1s, 2s, 4s, 8s, 16s, capped at 30s. Reset
   * inside flush() the first time we read a frame on a new stream.
   */
  function scheduleReconnect() {
    reconnectAttempts += 1
    const base = Math.min(30_000, 1_000 * 2 ** (reconnectAttempts - 1))
    // Light jitter (±15%) so a herd of clients doesn't reconnect in lockstep.
    const jitter = base * (0.85 + Math.random() * 0.3)
    const delay = Math.round(jitter)
    if (reconnectTimer) clearTimeout(reconnectTimer)
    reconnectTimer = setTimeout(() => {
      reconnectTimer = null
      if (!closed) void connect()
    }, delay)
  }

  function close() {
    closed = true
    if (reconnectTimer) {
      clearTimeout(reconnectTimer)
      reconnectTimer = null
    }
    abort?.abort()
    abort = null
    connected.value = false
  }

  function clear() { events.value = [] }
  function open() {
    closed = false
    reconnectAttempts = 0
    refreshAttempted = false
    void connect()
  }

  if (opts.immediate !== false) {
    void connect()
  }

  onBeforeUnmount(() => { close() })

  return { events, connected, lastEventId, error, open, close, clear }
}
