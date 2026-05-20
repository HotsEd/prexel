<!--
    ContainerTerminal — interactive xterm.js terminal hooked up to the
    backend's WebSocket exec endpoint.

    Wire protocol (must stay in sync with internal/api/handler/container_exec.go):

      - Binary frames: raw TTY bytes in both directions.
        * server → client: stdout/stderr (merged because the exec is in
          TTY mode)
        * client → server: keystrokes verbatim
      - Text frames (client → server only today): JSON control messages.
        The only supported `type` is "resize" with `cols` and `rows`.

    Authentication

      Browsers cannot set the Authorization header on the WebSocket
      constructor (RFC 6455), so we ship the JWT in two ways:

        1. Preferred: `Sec-WebSocket-Protocol: bearer.<jwt>` (same
           convention kubectl/`kubernetes-client-go` uses for `exec`).
           The browser sets this header from the second arg of the
           `WebSocket` constructor — see buildEndpoint/openSocket below.

        2. Fallback: `?token=<jwt>` query string. TODO: drop once the
           backend (internal/api/handler/container_exec.go) is known
           to honour the subprotocol on every supported deploy.

      BACKEND COORDINATION REQUIRED: the exec handler must accept
      `Sec-WebSocket-Protocol: bearer.<jwt>` (extract the JWT from
      the protocol value, then echo back the same protocol in the
      response so the upgrade succeeds). Until that lands, the WS
      query fallback keeps existing deploys working.

      We do NOT do refresh-on-401 here: if the token is stale, the
      connection fails to upgrade and the parent dialog reopens.

    Lifecycle

      onMounted:  build xterm, fit, open the WS, wire all 4 channels
                  (ws→term, term→ws, fit/resize→ws, resizeObserver)
      onUnmounted: close the WS, dispose xterm, drop observers

      Parents that re-mount this component (e.g. when reopening a
      dialog) get a fresh session each time. Re-using an existing
      session across mount/unmount is intentionally NOT supported —
      shells should always be opened by an explicit user action.
-->

<template>
    <div ref="rootEl" class="container-terminal">
        <div v-if="status === 'connecting'" class="container-terminal__overlay">
            {{ t('apps.terminal.connecting') }}
        </div>
        <div v-else-if="status === 'error'" class="container-terminal__overlay container-terminal__overlay--error">
            {{ errorMessage || t('apps.terminal.error_generic') }}
        </div>
        <div v-else-if="status === 'closed'" class="container-terminal__overlay container-terminal__overlay--muted">
            {{ t('apps.terminal.closed') }}
        </div>
        <div ref="termEl" class="container-terminal__xterm"></div>
    </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { useAuthStore } from '@/stores/auth'

/**
 * Props
 *   appId         — UUID of the app the container belongs to.
 *   containerName — Docker container name (NOT id). Must carry the
 *                   prexel.app_id=<appId> label or the server returns 403.
 *   cmd           — Optional shell override; falls back to /bin/sh on the
 *                   server side when empty. Operators may pass `bash`,
 *                   `ash`, etc.
 */
const props = defineProps<{
    appId: string
    containerName: string
    cmd?: string
}>()

const emit = defineEmits<{
    /** Fired once the WebSocket either closes cleanly or fails. */
    closed: [reason: 'normal' | 'error']
}>()

const { t } = useI18n()
const authStore = useAuthStore()

const rootEl = ref<HTMLElement | null>(null)
const termEl = ref<HTMLElement | null>(null)
const status = ref<'connecting' | 'open' | 'closed' | 'error'>('connecting')
const errorMessage = ref('')

// Kept outside reactive state — these are heavy non-serialisable objects
// and Vue should never wrap them in a Proxy.
let terminal: Terminal | null = null
let fitAddon: FitAddon | null = null
let ws: WebSocket | null = null
let resizeObserver: ResizeObserver | null = null

/**
 * Build the WS URL. We always use the current page origin: in
 * production the SPA is served by the Prexel server itself; in dev
 * (vite hot reload) the dev server proxies /api/* through to the
 * backend, including WebSocket upgrades.
 *
 * NOTE: we still append `?token=` as a fallback for backends that
 * don't yet read the `bearer.<jwt>` subprotocol. The subprotocol
 * path is preferred (set by openSocket) — once every supported
 * backend version accepts it, the query fallback can go away. See
 * TODO above.
 */
function buildEndpoint(): string {
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const host = window.location.host
    const path = `/api/v1/apps/${encodeURIComponent(props.appId)}/containers/${encodeURIComponent(props.containerName)}/exec`
    const params = new URLSearchParams()
    if (authStore.accessToken) {
        // TODO(remove-once-backend-supports-subprotocol-everywhere):
        // backend coordination — internal/api/handler/container_exec.go
        // must extract the JWT from `Sec-WebSocket-Protocol: bearer.<jwt>`
        // and echo the protocol back in the upgrade response. Drop this
        // query-string fallback after that change has shipped to every
        // supported deploy.
        params.set('token', authStore.accessToken)
    }
    if (props.cmd && props.cmd.trim() !== '') {
        params.set('cmd', props.cmd.trim())
    }
    const qs = params.toString()
    return `${proto}//${host}${path}${qs ? `?${qs}` : ''}`
}

/**
 * Construct the WebSocket. Prefer the `bearer.<jwt>` subprotocol
 * (no token-in-URL leakage into proxy logs / browser history);
 * fall back to a bare ctor if there's no token yet so we still
 * surface the right error from the upgrade handshake instead of a
 * cryptic exception here.
 */
function openSocket(url: string): WebSocket {
    const token = authStore.accessToken
    if (token) {
        // Subprotocol values are restricted to token characters per
        // RFC 6455 §4.1; raw JWTs satisfy that (base64url + '.').
        return new WebSocket(url, [`bearer.${token}`])
    }
    return new WebSocket(url)
}

/**
 * Send a resize control frame. The server expects:
 *   { type: "resize", cols: number, rows: number }
 * as a text frame. We ignore zero values because xterm's initial
 * dimensions can briefly be {0,0} before the DOM lays out.
 */
function sendResize(cols: number, rows: number) {
    if (!ws || ws.readyState !== WebSocket.OPEN) return
    if (cols <= 0 || rows <= 0) return
    try {
        ws.send(JSON.stringify({ type: 'resize', cols, rows }))
    } catch {
        // Best-effort — a stale send during teardown shouldn't be fatal.
    }
}

/**
 * Recompute xterm's grid against the parent container size, then push
 * the new dimensions to the server so the remote PTY's TIOCSWINSZ
 * matches. Called on initial mount, when the dialog resizes, and on
 * window resize.
 */
function fitAndSend() {
    if (!fitAddon || !terminal) return
    try {
        fitAddon.fit()
    } catch {
        // fit() throws when the element isn't visible / has zero size.
        // The next ResizeObserver tick will retry.
        return
    }
    sendResize(terminal.cols, terminal.rows)
}

onMounted(() => {
    if (!termEl.value) return

    // ── xterm setup ────────────────────────────────────────────────
    terminal = new Terminal({
        cursorBlink: true,
        fontFamily:
            "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', monospace",
        fontSize: 13,
        // The backend exec runs with TTY=true → expect the standard
        // mux'd stdout/stderr stream over a single channel.
        convertEol: false,
        scrollback: 5000,
        // Default colours are fine; using the operator's local terminal
        // palette would require channel detection we don't have here.
    })
    fitAddon = new FitAddon()
    terminal.loadAddon(fitAddon)
    terminal.open(termEl.value)
    fitAddon.fit()

    // ── WebSocket setup ───────────────────────────────────────────
    let websocket: WebSocket
    try {
        websocket = openSocket(buildEndpoint())
    } catch (e) {
        status.value = 'error'
        errorMessage.value = (e as Error).message
        return
    }
    // Binary frames as ArrayBuffer — easier to feed straight into xterm
    // than the default Blob (which requires async read).
    websocket.binaryType = 'arraybuffer'
    ws = websocket

    websocket.addEventListener('open', () => {
        status.value = 'open'
        // Push initial size right after open so the remote prompt
        // renders at the correct width on the first paint.
        fitAndSend()
        terminal?.focus()
    })

    websocket.addEventListener('message', (ev) => {
        if (!terminal) return
        if (typeof ev.data === 'string') {
            // The server doesn't send text frames today; ignore.
            return
        }
        // ArrayBuffer → Uint8Array → xterm. xterm accepts both string
        // and Uint8Array; the latter avoids a UTF-8 decode round-trip.
        terminal.write(new Uint8Array(ev.data as ArrayBuffer))
    })

    websocket.addEventListener('error', () => {
        // The 'close' handler runs right after; we only flip status
        // here so the UI can react before the close code arrives.
        if (status.value === 'connecting') {
            status.value = 'error'
            errorMessage.value = t('apps.terminal.error_connect')
        }
    })

    websocket.addEventListener('close', (ev) => {
        const wasOpen = status.value === 'open'
        if (ev.code === 1000 || ev.code === 1005) {
            status.value = 'closed'
            emit('closed', 'normal')
        } else {
            status.value = 'error'
            errorMessage.value = ev.reason || t('apps.terminal.error_disconnect')
            emit('closed', 'error')
        }
        // If we never made it to OPEN, the most common cause is 401
        // (the auth middleware rejected the upgrade). Surface a
        // friendlier hint.
        if (!wasOpen && status.value === 'error') {
            errorMessage.value =
                ev.reason || t('apps.terminal.error_unauthorized')
        }
    })

    // ── Local terminal → WebSocket ────────────────────────────────
    terminal.onData((data) => {
        if (!ws || ws.readyState !== WebSocket.OPEN) return
        // xterm hands us a string; convert to Uint8Array so the
        // WS frame is binary (matches the wire protocol).
        const enc = new TextEncoder()
        ws.send(enc.encode(data))
    })

    // ── Resize plumbing ───────────────────────────────────────────
    // 1. Container size changes (dialog resize, full-screen toggle):
    //    use ResizeObserver — it's coalesced by the browser so we
    //    avoid the rAF dance.
    if (rootEl.value && 'ResizeObserver' in window) {
        resizeObserver = new ResizeObserver(() => {
            fitAndSend()
        })
        resizeObserver.observe(rootEl.value)
    }
    // 2. Window-level resize (zoom, devtools open) is implicit in (1)
    //    because the dialog rescales with the viewport, but keep a
    //    direct listener as a belt-and-braces in case PrimeVue's
    //    Dialog ever stops scaling.
    window.addEventListener('resize', fitAndSend)
})

onBeforeUnmount(() => {
    window.removeEventListener('resize', fitAndSend)
    if (resizeObserver) {
        resizeObserver.disconnect()
        resizeObserver = null
    }
    if (ws) {
        try {
            ws.close(1000, 'client closing')
        } catch {
            // ignore — we're tearing down anyway
        }
        ws = null
    }
    if (terminal) {
        terminal.dispose()
        terminal = null
    }
    fitAddon = null
})
</script>

<style scoped>
.container-terminal {
    position: relative;
    width: 100%;
    height: 100%;
    background: #000;
    display: flex;
    flex-direction: column;
    overflow: hidden;
}

.container-terminal__xterm {
    flex: 1 1 auto;
    min-height: 0;
    padding: 8px;
}

.container-terminal__overlay {
    position: absolute;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    color: rgba(255, 255, 255, 0.85);
    background: rgba(0, 0, 0, 0.55);
    z-index: 1;
    padding: 16px;
    text-align: center;
    pointer-events: none;
}

.container-terminal__overlay--error {
    color: #ff8b8b;
    background: rgba(60, 0, 0, 0.6);
}

.container-terminal__overlay--muted {
    color: rgba(255, 255, 255, 0.55);
}

/* xterm renders its own canvas/dom inside; make sure it fills. */
:deep(.xterm) {
    height: 100%;
}
:deep(.xterm-viewport) {
    background-color: #000 !important;
}
</style>
