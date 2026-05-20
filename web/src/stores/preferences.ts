import { defineStore } from 'pinia'
import { ref, watch } from 'vue'
import { normalizeLocale, resolveInitialLocale, setI18nLocale, type Locale } from '@/i18n'

const STORAGE_KEY = 'prexel:prefs'
type Theme = 'light' | 'dark'

interface PersistedPrefs {
    theme: Theme
    locale: Locale
}

function load(): PersistedPrefs {
    if (typeof window === 'undefined') return { theme: 'dark', locale: 'pt-BR' }
    try {
        const raw = localStorage.getItem(STORAGE_KEY)
        if (raw) {
            const parsed = JSON.parse(raw) as Partial<PersistedPrefs>
            return {
                theme: parsed.theme === 'light' ? 'light' : 'dark',
                locale: normalizeLocale(parsed.locale),
            }
        }
    } catch { /* corrupted prefs — fall through */ }
    return { theme: 'dark', locale: resolveInitialLocale() }
}

export const usePreferencesStore = defineStore('preferences', () => {
    const initial = load()
    const theme = ref<Theme>(initial.theme)
    const locale = ref<Locale>(initial.locale)

    function applyTheme(t: Theme) {
        if (typeof document === 'undefined') return
        document.documentElement.classList.toggle('dark', t === 'dark')
    }

    function applyLocale(l: Locale) {
        setI18nLocale(l)
    }

    function setTheme(t: Theme) { theme.value = t }
    function toggleTheme() { theme.value = theme.value === 'dark' ? 'light' : 'dark' }
    function setLocale(l: Locale | string) { locale.value = normalizeLocale(l) }

    function applyAll() {
        applyTheme(theme.value)
        applyLocale(locale.value)
    }

    watch(theme, (t) => {
        applyTheme(t)
    })

    watch(locale, (l) => {
        applyLocale(l)
    })

    watch([theme, locale], ([t, l]) => {
        if (typeof window !== 'undefined') {
            localStorage.setItem(STORAGE_KEY, JSON.stringify({ theme: t, locale: l }))
        }
    })

    return { theme, locale, setTheme, toggleTheme, setLocale, applyAll }
})
