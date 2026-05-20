// Helpers for components/apps/SettingsTab.vue, split out so the
// component itself stays under the 600-line ceiling.
//
// Everything here is pure: regex validators that mirror the backend's
// parser (internal/app/service.go validateLimits) plus the docker-
// labels textarea encoder/decoder. No Vue state — the component imports
// these and wires them into computeds.

// ─── Validation regexes (mirror backend) ──────────────────────
//
// The backend has the final say; client-side validation is fail-fast
// UX only. Keep these in lock-step with internal/app/service.go.
const MEM_RE = /^\s*\d+(\.\d+)?\s*[bBkKmMgG]?\s*$/
const MEM_SWAP_RE = /^\s*(-1|\d+(\.\d+)?\s*[bBkKmMgG]?)\s*$/
const CPUS_RE = /^\s*\d+(\.\d+)?\s*$/
const CPUSET_RE = /^\s*\d+([,-]\d+)*\s*$/
const DOCKER_LABEL_KEY_RE = /^[a-zA-Z0-9][a-zA-Z0-9._-]*$/

/** Returns an error message or null when the value is empty / valid. */
export function validateMemory(v: string): string | null {
    const t = v.trim()
    if (t === '') return null
    return MEM_RE.test(t) ? null : 'Formato inválido. Ex.: 512m, 1g, 2g.'
}

export function validateMemorySwap(v: string): string | null {
    const t = v.trim()
    if (t === '') return null
    return MEM_SWAP_RE.test(t) ? null : 'Formato inválido. Ex.: 1g, -1 (ilimitado).'
}

export function validateMemoryReservation(v: string): string | null {
    const t = v.trim()
    if (t === '') return null
    return MEM_RE.test(t) ? null : 'Formato inválido. Ex.: 256m, 512m.'
}

export function validateCpus(v: string): string | null {
    const t = v.trim()
    if (t === '') return null
    return CPUS_RE.test(t) ? null : 'Esperado um número, ex.: 0.5, 1, 2.'
}

export function validateCpuset(v: string): string | null {
    const t = v.trim()
    if (t === '') return null
    return CPUSET_RE.test(t) ? null : 'Formato: 0,2-4,7.'
}

export function validatePort(raw: string): string | null {
    const v = raw.trim()
    if (v === '') return 'Porta obrigatória para apps single-container.'
    const n = Number(v)
    if (!Number.isInteger(n) || n < 1 || n > 65535) return 'Porta inválida (1–65535).'
    return null
}

// ─── Docker labels textarea ───────────────────────────────────
//
// We expose docker_labels as a `key=value` textarea (one per line)
// rather than a table — operators touch this rarely and the textarea
// matches the EnvEditor mental model. Errors render inline, save is
// blocked while errors exist.

export interface DockerLabelParse {
    labels: Record<string, string>
    error: string | null
}

export function parseDockerLabels(text: string): DockerLabelParse {
    const out: Record<string, string> = {}
    const lines = text.split(/\r?\n/)
    for (let i = 0; i < lines.length; i++) {
        const raw = lines[i] ?? ''
        const trimmed = raw.trim()
        if (trimmed === '' || trimmed.startsWith('#')) continue
        const eq = trimmed.indexOf('=')
        if (eq <= 0) {
            return { labels: {}, error: `Linha ${i + 1}: esperado key=value.` }
        }
        const key = trimmed.slice(0, eq).trim()
        const value = trimmed.slice(eq + 1)
        if (!DOCKER_LABEL_KEY_RE.test(key)) {
            return {
                labels: {},
                error: `Linha ${i + 1}: chave inválida "${key}". Use letras, números, ponto, hífen, underscore.`,
            }
        }
        out[key] = value
    }
    return { labels: out, error: null }
}

/**
 * Serialize a labels map back to textarea content. Sorted alpha so
 * the textarea doesn't shuffle on every reload — same trick EnvEditor
 * pulls for env vars.
 */
export function serializeDockerLabels(m: Record<string, string>): string {
    return Object.entries(m)
        .sort((a, b) => a[0].localeCompare(b[0]))
        .map(([k, v]) => `${k}=${v}`)
        .join('\n')
}

/**
 * Mirror the backend's tag normaliser (lowercase + trim). Used by the
 * chip editor so the chip text matches what gets persisted — otherwise
 * the user sees "Prod" and the server stores "prod", which makes the
 * next reload look like a phantom diff.
 */
export function normaliseTag(raw: string): string {
    return raw.trim().toLowerCase()
}

/**
 * Empty string → null for nullable string PATCH fields. The backend
 * treats `""` as "store empty string" — operators expect "clear the
 * field" instead, which is `null`.
 */
export function nullableTrim(s: string): string | null {
    const t = s.trim()
    return t === '' ? null : t
}

/**
 * Order-insensitive set equality for tag arrays. Used to decide
 * whether to fire the PUT /apps/:id/tags endpoint on save.
 */
export function tagsEqual(a: readonly string[], b: readonly string[]): boolean {
    if (a.length !== b.length) return false
    const sortedA = [...a].sort()
    const sortedB = [...b].sort()
    return sortedA.every((v, i) => v === sortedB[i])
}
