import { AxiosError } from 'axios'
import { i18n } from '@/i18n'

export interface NormalizedError {
    message: string
    status?: number
    cause: unknown
}

const STATUS_KEYS: Record<number, string> = {
    400: 'errors.validation',
    401: 'errors.unauthorized',
    403: 'errors.forbidden',
    404: 'errors.notFound',
    409: 'errors.conflict',
    422: 'errors.validation',
    429: 'errors.rateLimited',
    500: 'errors.server',
    502: 'errors.unavailable',
    503: 'errors.unavailable',
    504: 'errors.timeout',
}

export function normalizeError(err: unknown): NormalizedError {
    const t = i18n.global.t
    const FALLBACK = t('common.somethingWrong')

    if (err instanceof AxiosError) {
        const status = err.response?.status
        const data = err.response?.data as { message?: string; error?: string } | undefined
        const statusKey = status ? STATUS_KEYS[status] : undefined
        const message =
            data?.message
            ?? data?.error
            ?? (statusKey ? t(statusKey) : undefined)
            ?? (err.code === 'ERR_NETWORK' ? t('errors.network') : err.message)
            ?? FALLBACK
        return { message, status, cause: err }
    }
    if (err instanceof Error) {
        return { message: err.message || FALLBACK, cause: err }
    }
    if (typeof err === 'string') {
        return { message: err, cause: err }
    }
    return { message: FALLBACK, cause: err }
}
