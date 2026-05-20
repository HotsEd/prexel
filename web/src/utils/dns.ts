/**
 * Tiny DNS helpers used by the domain wizard. The authoritative apex
 * extraction lives in the Go backend (PSL-aware); this is a best-effort
 * client-side preview so the user sees the inferred apex while typing.
 *
 * Heuristic: drop the leftmost label until two labels remain (e.g.
 *   api.staging.foo.com → staging.foo.com → foo.com).
 * Treats well-known multi-label public suffixes ("co.uk", "com.br", …)
 * as a single TLD so `app.minha.com.br` collapses to `minha.com.br`.
 */
const MULTI_LABEL_TLDS = new Set([
    'co.uk', 'co.jp', 'co.kr', 'co.in', 'co.nz', 'co.za',
    'com.br', 'com.ar', 'com.mx', 'com.au', 'com.tr',
    'org.br', 'gov.br', 'edu.br', 'net.br',
])

/** Strips scheme, leading `www.`, trailing slash, path, and port. */
export function normalizeHostname(input: string): string {
    let v = (input || '').trim().toLowerCase()
    if (!v) return ''
    v = v.replace(/^https?:\/\//, '')
    v = v.replace(/^\/+/, '')
    // Drop everything after the first slash or query.
    v = v.split(/[/?#]/)[0] ?? ''
    // Strip port.
    v = v.replace(/:\d+$/, '')
    // Drop trailing dot (FQDN form).
    v = v.replace(/\.$/, '')
    return v
}

/** Returns the apex (registrable) domain for an FQDN, or '' on bad input. */
export function extractApex(hostname: string): string {
    const h = normalizeHostname(hostname)
    if (!h) return ''
    const labels = h.split('.')
    if (labels.length < 2) return ''
    const lastTwo = labels.slice(-2).join('.')
    if (MULTI_LABEL_TLDS.has(lastTwo) && labels.length >= 3) {
        return labels.slice(-3).join('.')
    }
    return lastTwo
}

/** True if `hostname` looks like a proper FQDN (no path, no port, has dot). */
export function isLikelyFqdn(hostname: string): boolean {
    const h = normalizeHostname(hostname)
    if (!h) return false
    if (!h.includes('.')) return false
    // Reject IP literals.
    if (/^\d+\.\d+\.\d+\.\d+$/.test(h)) return false
    // Each label must be 1-63 chars, alnum/hyphen, not starting/ending hyphen.
    const labelRe = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/
    return h.split('.').every((l) => labelRe.test(l))
}

/** True if the hostname is the apex itself (zero subdomain labels). */
export function isApex(hostname: string): boolean {
    const h = normalizeHostname(hostname)
    return !!h && h === extractApex(h)
}

/** Returns the subdomain portion (`api.foo.com` → `api`) or '' if apex. */
export function subdomainPart(hostname: string): string {
    const h = normalizeHostname(hostname)
    const apex = extractApex(h)
    if (!h || !apex || h === apex) return ''
    if (!h.endsWith('.' + apex)) return ''
    return h.slice(0, -apex.length - 1)
}
