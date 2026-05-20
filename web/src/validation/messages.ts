/**
 * Validation message accessors backed by vue-i18n. Schemas use these so the
 * keys live in one place and update when locale changes.
 */
import { i18n } from '@/i18n'

const t = i18n.global.t

export const MSG = {
    get required() { return t('validation.required') },
    get email() { return t('validation.email') },
    minLength: (n: number) => t('validation.minLength', { n }),
    maxLength: (n: number) => t('validation.maxLength', { n }),
} as const
