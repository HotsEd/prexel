/**
 * Auth service — talks to the Prexel Go backend.
 *
 * Login flow:
 *   POST /auth/login → { access_token, expires_in } | { requires_2fa, challenge_id, methods }
 *
 *   If `requires_2fa`, the caller must complete the 2FA challenge:
 *     POST /auth/2fa/challenge   { challenge_id, code, method }
 *
 * After login (or successful challenge) the server sets an HttpOnly refresh
 * cookie at /auth/refresh and returns the access token in the body.
 */

import axios from 'axios'
import { useApi } from '@/composables/useApi'
import type {
    TwoFactorMethod,
    TwoFactorRequiredResponse,
    TwoFactorSetupInitiateResponse,
    TwoFactorSetupConfirmResponse,
    TwoFactorStatusResponse,
    User,
} from '@/types/api'

export type LoginResult =
    | { kind: 'ok'; access_token: string; expires_in: number }
    | { kind: 'two-factor-required'; challenge_id: string; methods: TwoFactorMethod[] }

/**
 * Bare axios for endpoints that must never go through the auth interceptor
 * (login + challenge run before the user has a token).
 */
const bare = axios.create({
    baseURL: '/api/v1',
    withCredentials: true,
    headers: { 'Content-Type': 'application/json' },
})

function isTwoFactorRequired(data: unknown): data is TwoFactorRequiredResponse {
    return !!data && typeof data === 'object' && (data as { requires_2fa?: boolean }).requires_2fa === true
}

export const authService = {
    async login(email: string, password: string): Promise<LoginResult> {
        const res = await bare.post('/auth/login', { email, password })
        if (isTwoFactorRequired(res.data)) {
            return {
                kind: 'two-factor-required',
                challenge_id: res.data.challenge_id,
                methods: res.data.methods ?? ['app'],
            }
        }
        return {
            kind: 'ok',
            access_token: res.data.access_token,
            expires_in: res.data.expires_in,
        }
    },

    async refresh(): Promise<{ access_token: string; expires_in: number }> {
        const res = await bare.post('/auth/refresh', null)
        return res.data
    },

    async logout(): Promise<void> {
        const api = useApi()
        await api.post('/auth/logout')
    },

    async me(): Promise<User> {
        const api = useApi()
        const res = await api.get<User>('/auth/me')
        return res.data
    },

    // ─── 2FA challenge (login-time) ───
    async twoFactorChallengeMethods(challengeId: string): Promise<{ methods: TwoFactorMethod[]; email_hint: string | null }> {
        void challengeId
        return { methods: ['app', 'recovery'], email_hint: null }
    },

    async twoFactorChallengeVerify(opts: {
        challenge_id: string
        code: string
        method: TwoFactorMethod
    }): Promise<{ access_token: string; expires_in: number }> {
        const res = await bare.post('/auth/2fa/challenge', {
            challenge_id: opts.challenge_id,
            code: opts.code,
            method: opts.method,
        })
        return res.data
    },

    /** v0.1 doesn't support email-based 2FA challenge; kept as a hook. */
    async twoFactorChallengeSendEmail(): Promise<{ email_hint: string | null }> {
        throw new Error('Email 2FA não disponível na v0.1.')
    },

    // ─── 2FA management (authenticated) ───
    async twoFactorStatus(): Promise<TwoFactorStatusResponse> {
        const api = useApi()
        const res = await api.get<TwoFactorStatusResponse>('/auth/2fa/status')
        return res.data
    },

    async changePassword(opts: {
        current_password: string
        new_password: string
        two_factor_code?: string
        two_factor_method?: TwoFactorMethod
    }): Promise<void> {
        const api = useApi()
        await api.post('/auth/password', opts)
    },

    async updateProfile(opts: { name: string }): Promise<User> {
        const api = useApi()
        const res = await api.patch<User>('/auth/profile', opts)
        return res.data
    },

    async changeEmail(opts: {
        email: string
        current_password: string
        two_factor_code?: string
        two_factor_method?: TwoFactorMethod
    }): Promise<User> {
        const api = useApi()
        const res = await api.post<User>('/auth/email', opts)
        return res.data
    },

    async uploadAvatar(file: File): Promise<User> {
        const api = useApi()
        const form = new FormData()
        form.append('avatar', file)
        const res = await api.post<User>('/auth/avatar', form, {
            headers: { 'Content-Type': 'multipart/form-data' },
        })
        return res.data
    },

    async removeAvatar(): Promise<void> {
        const api = useApi()
        await api.delete('/auth/avatar')
    },

    async twoFactorSetupInitiate(opts: { password: string }): Promise<TwoFactorSetupInitiateResponse> {
        const api = useApi()
        const res = await api.post<TwoFactorSetupInitiateResponse>('/auth/2fa/setup-initiate', opts)
        return res.data
    },

    async twoFactorSetupConfirm(code: string): Promise<TwoFactorSetupConfirmResponse> {
        const api = useApi()
        const res = await api.post<TwoFactorSetupConfirmResponse>('/auth/2fa/setup-confirm', { code })
        return res.data
    },

    async twoFactorDisable(opts: { password: string; code: string }): Promise<void> {
        const api = useApi()
        await api.post('/auth/2fa/disable', opts)
    },

    async twoFactorRecoveryCodes(opts: { password: string }): Promise<{ recovery_codes: string[] }> {
        const api = useApi()
        const res = await api.post<{ recovery_codes: string[] }>('/auth/2fa/recovery-codes', opts)
        return res.data
    },
}
