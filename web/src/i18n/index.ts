import { createI18n } from 'vue-i18n'
import { DEFAULT_LOCALE, resolveInitialLocale, type Locale } from './locales'
import ptBR from './locales/pt-BR'
import enUS from './locales/en-US'
import ptBRAgentA from './locales/sections/agentA.pt-BR'
import enUSAgentA from './locales/sections/agentA.en-US'
import ptBRAgentB from './locales/sections/agentB.pt-BR'
import enUSAgentB from './locales/sections/agentB.en-US'
import ptBRAgentC from './locales/sections/agentC.pt-BR'
import enUSAgentC from './locales/sections/agentC.en-US'
import { deepMerge } from './deepMerge'

// Fragments under `locales/sections/` are folded into the base trees with a
// recursive deep-merge so that per-feature additions never wipe sibling
// namespaces (e.g. an `apps.row` fragment must coexist with `apps.title`).
// Order matters: each fragment is layered on top of the previous one, so
// later fragments can intentionally override earlier strings if needed.
const MESSAGES = {
    'pt-BR': deepMerge(ptBR, ptBRAgentA, ptBRAgentB, ptBRAgentC),
    'en-US': deepMerge(enUS, enUSAgentA, enUSAgentB, enUSAgentC),
} as const

export { DEFAULT_LOCALE, LOCALE_OPTIONS, SUPPORTED_LOCALES, isSupportedLocale, normalizeLocale, resolveInitialLocale } from './locales'
export type { Locale } from './locales'

export const i18n = createI18n({
    legacy: false,
    locale: resolveInitialLocale(),
    fallbackLocale: DEFAULT_LOCALE,
    messages: MESSAGES,
    silentTranslationWarn: import.meta.env.PROD,
    silentFallbackWarn: import.meta.env.PROD,
    missingWarn: !import.meta.env.PROD,
    fallbackWarn: !import.meta.env.PROD,
})

export function setI18nLocale(locale: Locale): void {
    i18n.global.locale.value = locale
    if (typeof document !== 'undefined') {
        document.documentElement.lang = locale
    }
}
