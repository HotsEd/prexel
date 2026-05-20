export const DEFAULT_LOCALE = 'pt-BR'
export const SUPPORTED_LOCALES = ['pt-BR', 'en-US'] as const
export type Locale = typeof SUPPORTED_LOCALES[number]

export const LOCALE_OPTIONS: Array<{ label: string; value: Locale }> = [
    { label: 'Português (Brasil)', value: 'pt-BR' },
    { label: 'English (US)', value: 'en-US' },
]

export function isSupportedLocale(value: string): value is Locale {
    return SUPPORTED_LOCALES.includes(value as Locale)
}

export function normalizeLocale(value?: string | null): Locale {
    if (!value) return DEFAULT_LOCALE
    if (isSupportedLocale(value)) return value
    const lower = value.toLowerCase()
    if (lower.startsWith('pt')) return 'pt-BR'
    if (lower.startsWith('en')) return 'en-US'
    return DEFAULT_LOCALE
}

export function resolveInitialLocale(): Locale {
    if (typeof window === 'undefined') return DEFAULT_LOCALE
    try {
        const raw = localStorage.getItem('prexel:prefs')
        if (raw) {
            const prefs = JSON.parse(raw) as { locale?: string }
            if (prefs.locale) return normalizeLocale(prefs.locale)
        }
    } catch { /* corrupted prefs — ignore */ }

    const preferred = navigator.languages?.[0] ?? navigator.language
    return normalizeLocale(preferred)
}
