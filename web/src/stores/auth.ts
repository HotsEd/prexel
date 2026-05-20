import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { authService } from '@/services/auth'
import type { TwoFactorMethod, User } from '@/types/api'

const sessionTokenKey = 'prexel.access_token'
const sessionTokenExpiresKey = 'prexel.access_token_expires_at'

/**
 * Auth store — owns the in-memory access token. Refresh token lives in an
 * HttpOnly cookie. On app boot we call /auth/refresh once and only then
 * commit `initialized=true` so the router guard can decide where to land.
 */
export const useAuthStore = defineStore('auth', () => {
  const accessToken = ref<string | null>(null)
  const tokenExpiresAt = ref<number | null>(null)
  const user = ref<User | null>(null)
  const initialized = ref(false)
  const initializing = ref(false)

  const isAuthenticated = computed(() => !!accessToken.value)

  function setToken(token: string, expiresInSeconds: number) {
    accessToken.value = token
    tokenExpiresAt.value = Date.now() + expiresInSeconds * 1000
    sessionStorage.setItem(sessionTokenKey, token)
    sessionStorage.setItem(sessionTokenExpiresKey, String(tokenExpiresAt.value))
  }

  function setUser(u: User | null) {
    user.value = u
  }

  function clear() {
    accessToken.value = null
    tokenExpiresAt.value = null
    user.value = null
    sessionStorage.removeItem(sessionTokenKey)
    sessionStorage.removeItem(sessionTokenExpiresKey)
  }

  function markInitialized() {
    initialized.value = true
  }

  function restoreSessionToken(): boolean {
    const token = sessionStorage.getItem(sessionTokenKey)
    const expiresAt = Number(sessionStorage.getItem(sessionTokenExpiresKey) ?? 0)
    if (!token || !expiresAt || expiresAt <= Date.now() + 5_000) {
      sessionStorage.removeItem(sessionTokenKey)
      sessionStorage.removeItem(sessionTokenExpiresKey)
      return false
    }
    accessToken.value = token
    tokenExpiresAt.value = expiresAt
    return true
  }

  /** Called by router.beforeEach on first nav. Safe to call multiple times. */
  async function initialize() {
    if (initialized.value || initializing.value) return
    initializing.value = true
    try {
      if (restoreSessionToken()) {
        const me = await authService.me()
        setUser(me)
        return
      }

      const data = await authService.refresh()
      setToken(data.access_token, data.expires_in)
      // best-effort user hydration; ignore failures
      try {
        const me = await authService.me()
        setUser(me)
      } catch { /* leave user null */ }
    } catch {
      clear()
    } finally {
      initializing.value = false
      initialized.value = true
    }
  }

  /**
   * Returns:
   *   - { kind: 'ok' }                 → access_token saved, ready to navigate
   *   - { kind: 'two-factor-required', challengeId, methods } → caller must route to /two-factor-challenge
   */
  async function login(email: string, password: string): Promise<
    | { kind: 'ok' }
    | { kind: 'two-factor-required'; challengeId: string; methods: TwoFactorMethod[] }
  > {
    const result = await authService.login(email, password)
    if (result.kind === 'two-factor-required') {
      return { kind: 'two-factor-required', challengeId: result.challenge_id, methods: result.methods }
    }
    setToken(result.access_token, result.expires_in)
    // hydrate user
    try {
      const me = await authService.me()
      setUser(me)
    } catch {
      setUser({ id: '', email })
    }
    markInitialized()
    return { kind: 'ok' }
  }

  /** Called from TwoFactorChallenge after a successful verify. */
  async function applyChallengeSuccess(data: { access_token: string; expires_in: number }, fallbackEmail: string) {
    setToken(data.access_token, data.expires_in)
    try {
      const me = await authService.me()
      setUser(me)
    } catch {
      setUser({ id: '', email: fallbackEmail })
    }
    markInitialized()
  }

  async function logout() {
    try { await authService.logout() } catch { /* ignore */ }
    clear()
    markInitialized()
  }

  return {
    accessToken,
    tokenExpiresAt,
    user,
    initialized,
    isAuthenticated,
    setToken,
    setUser,
    clear,
    markInitialized,
    initialize,
    login,
    applyChallengeSuccess,
    logout,
  }
})
