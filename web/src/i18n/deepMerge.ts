/**
 * Recursive deep-merge of translation message trees.
 *
 * Translation files are nested plain-object trees of strings (leaves). When
 * the i18n base messages get extended via fragment files (see
 * `locales/sections/`), we need to fold those fragments into the main object
 * without losing any sibling keys at any depth. `Object.assign` and the
 * spread operator both shallow-overwrite at the top level, so a fragment
 * that added `{ apps: { row: {...} } }` would WIPE the existing
 * `apps.{title,detail,…}` namespace if we used those — every key inside the
 * sources must survive into the target.
 *
 * Behaviour:
 *   - Plain objects on both sides → recurse.
 *   - Anything else (string, number, array, null, RegExp, …) → later source
 *     wins. Strings are leaves; arrays are treated as opaque values rather
 *     than concatenated, which matches vue-i18n's expectation that a
 *     translation slot is either a string or a structured value chosen by
 *     the latest definition.
 *
 * Sources are applied left-to-right, so the rightmost fragment can override
 * a key set by an earlier one — useful when a per-feature fragment needs to
 * specialize a string defined in the base file.
 */

type Plain = Record<string, unknown>

function isPlainObject(value: unknown): value is Plain {
    if (value === null || typeof value !== 'object') return false
    const proto = Object.getPrototypeOf(value)
    return proto === Object.prototype || proto === null
}

export function deepMerge<T extends Plain>(...sources: ReadonlyArray<Plain>): T {
    const out: Plain = {}
    for (const src of sources) {
        if (!isPlainObject(src)) continue
        for (const key of Object.keys(src)) {
            const next = src[key]
            const prev = out[key]
            if (isPlainObject(prev) && isPlainObject(next)) {
                out[key] = deepMerge(prev, next)
            } else {
                out[key] = next
            }
        }
    }
    return out as T
}
