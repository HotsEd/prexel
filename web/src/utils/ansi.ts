/*
    Tiny ANSI SGR parser. Converts a string with ANSI escape codes
    (the `\x1b[...m` "Select Graphic Rendition" subset) into a flat
    list of `{text, classes}` segments that the deploy terminal
    renders as `<span class="...">` elements.

    Why hand-rolled? Docker build output uses a small handful of
    codes — basic 8-color FG/BG, 256-color FG, bold, dim, reset.
    A full library (`ansi-to-html`, `anser`) drags in a parser for
    cursor moves, scroll regions, modes, etc. that we never see
    in build logs. ~80 lines here saves a runtime dep.

    Unknown / unsupported codes are silently dropped — same
    behaviour as `docker build` piped to a pager.
*/

export interface AnsiSegment {
    text: string
    /** CSS class list. Empty for plain text. */
    classes: string[]
}

// Standard 8-color SGR FG and BG palettes. Bright variants (90-97 /
// 100-107) map to *-bright classes so the terminal CSS can tone them.
const FG: Record<number, string> = {
    30: 'ansi-fg-black',
    31: 'ansi-fg-red',
    32: 'ansi-fg-green',
    33: 'ansi-fg-yellow',
    34: 'ansi-fg-blue',
    35: 'ansi-fg-magenta',
    36: 'ansi-fg-cyan',
    37: 'ansi-fg-white',
    90: 'ansi-fg-black-bright',
    91: 'ansi-fg-red-bright',
    92: 'ansi-fg-green-bright',
    93: 'ansi-fg-yellow-bright',
    94: 'ansi-fg-blue-bright',
    95: 'ansi-fg-magenta-bright',
    96: 'ansi-fg-cyan-bright',
    97: 'ansi-fg-white-bright',
}
const BG: Record<number, string> = {
    40: 'ansi-bg-black',
    41: 'ansi-bg-red',
    42: 'ansi-bg-green',
    43: 'ansi-bg-yellow',
    44: 'ansi-bg-blue',
    45: 'ansi-bg-magenta',
    46: 'ansi-bg-cyan',
    47: 'ansi-bg-white',
    100: 'ansi-bg-black-bright',
    101: 'ansi-bg-red-bright',
    102: 'ansi-bg-green-bright',
    103: 'ansi-bg-yellow-bright',
    104: 'ansi-bg-blue-bright',
    105: 'ansi-bg-magenta-bright',
    106: 'ansi-bg-cyan-bright',
    107: 'ansi-bg-white-bright',
}

/*
   parseAnsi splits `s` into segments at every SGR escape. State
   (current fg/bg, bold, dim, italic, underline) carries across
   segments until reset (`\x1b[0m`) or per-attribute reset codes.

   The regex matches the SGR subset: `\x1b[<digits>(;<digits>)*m`.
   Anything else (cursor moves, OSC sequences) is treated as literal
   text — docker build only emits SGR in practice.
*/
const SGR_RE = /\x1b\[([0-9;]*)m/g

interface State {
    fg: string | null
    bg: string | null
    bold: boolean
    dim: boolean
    italic: boolean
    underline: boolean
}

function emptyState(): State {
    return { fg: null, bg: null, bold: false, dim: false, italic: false, underline: false }
}

function classesFor(s: State): string[] {
    const out: string[] = []
    if (s.fg) out.push(s.fg)
    if (s.bg) out.push(s.bg)
    if (s.bold) out.push('ansi-bold')
    if (s.dim) out.push('ansi-dim')
    if (s.italic) out.push('ansi-italic')
    if (s.underline) out.push('ansi-underline')
    return out
}

// Apply a SGR parameter list to the running state. Sequences with
// embedded sub-params (38;5;n / 48;5;n / 38;2;r;g;b) walk the list
// in one pass.
function applySGR(s: State, params: number[]): State {
    const out = { ...s }
    if (params.length === 0) {
        // Empty `\x1b[m` == reset.
        return emptyState()
    }
    for (let i = 0; i < params.length; i++) {
        const n = params[i]!
        switch (n) {
            case 0:
                Object.assign(out, emptyState())
                break
            case 1:
                out.bold = true; break
            case 2:
                out.dim = true; break
            case 3:
                out.italic = true; break
            case 4:
                out.underline = true; break
            case 22:
                out.bold = false; out.dim = false; break
            case 23:
                out.italic = false; break
            case 24:
                out.underline = false; break
            case 39:
                out.fg = null; break
            case 49:
                out.bg = null; break
            case 38:
            case 48:
                // 256-color: 38;5;n  /  48;5;n
                // 24-bit:    38;2;r;g;b / 48;2;r;g;b
                if (params[i + 1] === 5 && typeof params[i + 2] === 'number') {
                    // 256-color → fold into nearest 8-color class so we
                    // don't ship a 256-rule stylesheet. n in 0-15 maps
                    // to the standard FG/BG; 16-231 are the 6x6x6 cube
                    // (approximated by red/green/blue/yellow/cyan/magenta);
                    // 232-255 grayscale → white-bright/black-bright.
                    const idx = params[i + 2]!
                    const cls = approx256(idx, n === 38)
                    if (n === 38) out.fg = cls
                    else out.bg = cls
                    i += 2
                } else if (params[i + 1] === 2) {
                    // 24-bit RGB — same approximation as 256-color.
                    const r = params[i + 2] ?? 0
                    const g = params[i + 3] ?? 0
                    const b = params[i + 4] ?? 0
                    const cls = approxRGB(r, g, b, n === 38)
                    if (n === 38) out.fg = cls
                    else out.bg = cls
                    i += 4
                }
                break
            default:
                if (FG[n]) out.fg = FG[n] ?? null
                else if (BG[n]) out.bg = BG[n] ?? null
                // Unknown SGR — ignore.
                break
        }
    }
    return out
}

function approx256(idx: number, isFG: boolean): string {
    const table = isFG ? FG : BG
    const baseFG = isFG ? 30 : 40
    const baseBright = isFG ? 90 : 100
    if (idx < 8) return table[baseFG + idx]!
    if (idx < 16) return table[baseBright + (idx - 8)]!
    if (idx >= 232) {
        // Grayscale ramp — pick black-bright (dark grays) or
        // white-bright (light grays).
        return idx < 244 ? table[baseBright + 0]! : table[baseBright + 7]!
    }
    // 16-231: 6x6x6 RGB cube. Pull out r/g/b and approximate.
    const n = idx - 16
    const r = Math.floor(n / 36)
    const g = Math.floor((n % 36) / 6)
    const b = n % 6
    return approxRGB((r * 255) / 5, (g * 255) / 5, (b * 255) / 5, isFG)
}

function approxRGB(r: number, g: number, b: number, isFG: boolean): string {
    const table = isFG ? FG : BG
    const baseFG = isFG ? 30 : 40
    const baseBright = isFG ? 90 : 100
    // Pick the dominant 6-channel class. Trivial heuristic — fine
    // for log output where exact RGB matching doesn't matter.
    const max = Math.max(r, g, b)
    const min = Math.min(r, g, b)
    if (max < 64) return table[baseFG + 0]! // black
    if (min > 192) return table[baseBright + 7]! // white-bright
    const bright = max > 160
    const off = bright ? baseBright : baseFG
    if (r >= g && r >= b) {
        if (g >= b * 1.5) return table[off + 3]! // yellow
        if (b >= g * 1.5) return table[off + 5]! // magenta
        return table[off + 1]! // red
    }
    if (g >= r && g >= b) {
        if (r >= b * 1.5) return table[off + 3]! // yellow
        if (b >= r * 1.5) return table[off + 6]! // cyan
        return table[off + 2]! // green
    }
    if (r >= g * 1.5) return table[off + 5]! // magenta
    if (g >= r * 1.5) return table[off + 6]! // cyan
    return table[off + 4]! // blue
}

/*
   parseAnsi tokenizes `s` into segments. Stateful: bold/dim/colours
   carry forward until reset. Lines that contain no escapes return
   a single plain segment — cheapest path.
*/
export function parseAnsi(s: string): AnsiSegment[] {
    if (!s || !s.includes('\x1b')) {
        return [{ text: s, classes: [] }]
    }
    const out: AnsiSegment[] = []
    let state = emptyState()
    let cursor = 0
    SGR_RE.lastIndex = 0
    let m: RegExpExecArray | null
    while ((m = SGR_RE.exec(s)) !== null) {
        if (m.index > cursor) {
            const text = s.slice(cursor, m.index)
            out.push({ text, classes: classesFor(state) })
        }
        const raw = m[1] ?? ''
        const params = raw === '' ? [] : raw.split(';').map((p) => parseInt(p, 10) || 0)
        state = applySGR(state, params)
        cursor = m.index + m[0].length
    }
    if (cursor < s.length) {
        out.push({ text: s.slice(cursor), classes: classesFor(state) })
    }
    // Drop empty segments (back-to-back escapes with nothing between).
    return out.filter((seg) => seg.text.length > 0)
}
