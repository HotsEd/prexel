/**
 * Personal API tokens (PATs) service — wraps /me/tokens.
 *
 * Mental model: the operator creates a long-lived credential that
 * authenticates AS them when calling /api/v1/* with
 * `Authorization: Bearer prx_pat_…`. The token inherits the user's
 * RBAC permissions; there is no separate scope matrix.
 *
 * SECURITY: the raw token string is only ever present in the response
 * to `create()`. Subsequent `list()` calls deliberately omit it. The UI
 * must show the raw value exactly once at creation time and then drop
 * it; there's no recovery if the operator loses it.
 */
import { useApi } from '@/composables/useApi'

export interface ApiToken {
    id: string
    user_id: string
    name: string
    /** Last 4 chars of the raw token — enough to identify which token is which. */
    last_four: string
    /** ISO-8601 string. Null when the operator chose "never expires". */
    expires_at?: string | null
    /** ISO-8601 string. Null when the token has never authenticated yet. */
    last_used_at?: string | null
    /** Always null in list() results — revoked tokens are filtered server-side. */
    revoked_at?: string | null
    created_at: string
    updated_at: string
}

export interface CreateApiTokenInput {
    name: string
    /** ISO-8601 timestamp in the future, or null/omitted for "never expires". */
    expires_at?: string | null
}

export interface CreateApiTokenResponse {
    token: ApiToken
    /** The full `prx_pat_…` secret. Visible exactly once — handle with care. */
    raw: string
}

export const apiTokensService = {
    async list(): Promise<ApiToken[]> {
        const res = await useApi().get<{ tokens: ApiToken[] }>('/me/tokens')
        return res.data?.tokens ?? []
    },

    async create(input: CreateApiTokenInput): Promise<CreateApiTokenResponse> {
        const res = await useApi().post<CreateApiTokenResponse>('/me/tokens', input)
        return res.data
    },

    async revoke(id: string): Promise<void> {
        await useApi().delete(`/me/tokens/${id}`)
    },
}
