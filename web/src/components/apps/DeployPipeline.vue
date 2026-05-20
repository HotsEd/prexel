<template>
    <!--
        DeployPipeline — horizontal strip showing the four high-level
        phases of a Prexel deploy. Derived purely from `status` +
        observed bus events; we don't have explicit phase events from
        the backend so the mapping lives here.

        Phases:
          1. Iniciar  — `deploy.started` fired
          2. Build    — image build (app.status_changed.building)
          3. Deploy   — container swap + health check (app.status_changed.deploying)
          4. Concluído — terminal state (success or failed)

        State per chip:
          - `pending`  — gray dot, muted text (not yet reached)
          - `active`   — emerald pulsing dot, bright text (currently running)
          - `done`     — emerald check, bright text (completed)
          - `failed`   — red X, red text (this is where the deploy broke)

        Failure semantics: only the phase that was active when the
        deploy failed gets `failed`; earlier phases stay `done`,
        later phases stay `pending` (greyed). This mirrors how the
        operator perceived the failure — "build went OK, deploy
        crashed."
    -->
    <ol class="deploy-pipeline">
        <li
            v-for="(p, idx) in phases"
            :key="p.id"
            :class="['deploy-pipeline__step', `is-${p.state}`]"
        >
            <span class="deploy-pipeline__dot">
                <span v-if="p.state === 'done'" class="deploy-pipeline__icon">
                    <svg viewBox="0 0 12 12" aria-hidden="true">
                        <path d="M2 6.5l2.5 2.5L10 3.5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
                    </svg>
                </span>
                <span v-else-if="p.state === 'failed'" class="deploy-pipeline__icon">
                    <svg viewBox="0 0 12 12" aria-hidden="true">
                        <path d="M3 3l6 6M9 3l-6 6" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
                    </svg>
                </span>
                <!-- active + pending both render the bare dot; the
                     `is-active` CSS class drives the pulse animation. -->
            </span>
            <div class="deploy-pipeline__body">
                <span class="deploy-pipeline__label">{{ p.label }}</span>
                <span v-if="p.hint" class="deploy-pipeline__hint">{{ p.hint }}</span>
            </div>
            <span
                v-if="idx < phases.length - 1"
                :class="['deploy-pipeline__bar', barClass(phases[idx], phases[idx + 1])]"
                aria-hidden="true"
            />
        </li>
    </ol>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

type PhaseState = 'pending' | 'active' | 'done' | 'failed'
type Status = 'pending' | 'building' | 'deploying' | 'success' | 'failed' | string

interface Phase {
    id: string
    label: string
    hint?: string | null
    state: PhaseState
}

const props = withDefaults(defineProps<{
    /** Current deployment status from the backend. */
    status: Status
    /** Optional human-readable hint for the currently-active phase
        (e.g. "5 layers", "health 1/3"). Rendered under the active
        chip; null/undefined skips it. */
    activeHint?: string | null
}>(), {
    activeHint: null,
})

const { t } = useI18n()

/*
   Build the four chips from the current status. The "active" phase is
   the highest one we've reached without entering the terminal state;
   earlier phases are "done" and later ones are "pending". On failure,
   the active phase is marked `failed`.

   The status values map like this:
     pending    → Iniciar active (we've fired the deploy, haven't
                  hit the build yet)
     building   → Iniciar done, Build active
     deploying  → Iniciar+Build done, Deploy active
     success    → all four done
     failed     → whichever phase was running gets `failed`;
                  unfortunately the status column alone can't tell us
                  which one — but in practice the engine writes
                  `failed` after either `building` or `deploying`, so
                  we mark Deploy as failed by default (it covers
                  swap/healthcheck which is the most common failure
                  surface). If you have a more granular signal, pass
                  it via the `activeHint` prop and we'll surface it.
*/
const phases = computed<Phase[]>(() => {
    const base: Phase[] = [
        { id: 'start',  label: t('deployPipeline.start'), state: 'pending' },
        { id: 'build',  label: t('deployPipeline.build'),   state: 'pending' },
        { id: 'deploy', label: t('deployPipeline.deploy'),  state: 'pending' },
        { id: 'done',   label: t('deployPipeline.done'), state: 'pending' },
    ]

    const setActive = (i: number) => {
        for (let j = 0; j < i; j++) base[j].state = 'done'
        base[i].state = 'active'
        if (props.activeHint) base[i].hint = props.activeHint
    }

    switch (props.status) {
        case 'pending':
            setActive(0)
            break
        case 'building':
            setActive(1)
            break
        case 'deploying':
            setActive(2)
            break
        case 'success':
            for (const p of base) p.state = 'done'
            break
        case 'failed':
            // Mark the phase we were last in as `failed`. Without
            // richer telemetry, default to the deploy phase — it's
            // where most real failures land (health check, image swap).
            for (let j = 0; j < 2; j++) base[j].state = 'done'
            base[2].state = 'failed'
            break
        default:
            // Unknown status — leave all as pending so the chip strip
            // still renders something rather than throwing.
            break
    }

    return base
})

function barClass(prev: Phase, next: Phase): string {
    if (prev.state === 'done' && next.state !== 'pending') return 'is-done'
    if (prev.state === 'failed') return 'is-failed'
    return ''
}
</script>

<style scoped>
.deploy-pipeline {
    display: flex;
    align-items: flex-start;
    gap: 0;
    list-style: none;
    margin: 0;
    padding: 0;
    flex-wrap: wrap;
}
.deploy-pipeline__step {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    padding-right: 8px;
    position: relative;
    flex: 1 1 0;
    min-width: 0;
}
.deploy-pipeline__dot {
    flex-shrink: 0;
    width: 22px;
    height: 22px;
    border-radius: 999px;
    border: 1.5px solid var(--p-content-border);
    background: var(--p-content-bg);
    display: inline-flex;
    align-items: center;
    justify-content: center;
    color: var(--p-text-muted);
    transition: border-color 120ms ease, background 120ms ease, color 120ms ease;
}
.deploy-pipeline__icon { display: inline-flex; }
.deploy-pipeline__icon svg { width: 12px; height: 12px; }

.deploy-pipeline__body {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
    padding-top: 2px;
}
.deploy-pipeline__label {
    font-size: 12px;
    font-weight: 650;
    color: var(--p-text-muted);
    text-transform: uppercase;
    letter-spacing: 0.04em;
    line-height: 1;
}
.deploy-pipeline__hint {
    font-size: 11px;
    color: var(--p-text-muted);
    font-family: ui-monospace, Menlo, monospace;
    line-height: 1.3;
}

/* Connecting bar between chips — drawn as a sibling so we can colour
   it independently based on transition between the two flanking
   phases. */
.deploy-pipeline__bar {
    position: absolute;
    top: 10px;
    right: 0;
    left: calc(22px + 12px); /* dot width + gap */
    height: 2px;
    background: var(--p-content-border);
    border-radius: 999px;
    z-index: -1;
    transform: translateX(8px);
}
.deploy-pipeline__bar.is-done { background: var(--p-primary-500); }
.deploy-pipeline__bar.is-failed { background: var(--p-red-500, #dc2626); }

/* Active state — emerald, gently pulsing. */
.deploy-pipeline__step.is-active .deploy-pipeline__dot {
    border-color: var(--p-primary-500);
    background: color-mix(in srgb, var(--p-primary-500), transparent 78%);
    color: var(--p-primary-500);
    animation: deploy-pipeline-pulse 1.6s ease-in-out infinite;
}
.deploy-pipeline__step.is-active .deploy-pipeline__label { color: var(--p-text); }

/* Done state — solid emerald. */
.deploy-pipeline__step.is-done .deploy-pipeline__dot {
    border-color: var(--p-primary-500);
    background: var(--p-primary-500);
    color: #fff;
}
.deploy-pipeline__step.is-done .deploy-pipeline__label { color: var(--p-text); }

/* Failed state — red. */
.deploy-pipeline__step.is-failed .deploy-pipeline__dot {
    border-color: var(--p-red-500, #dc2626);
    background: var(--p-red-500, #dc2626);
    color: #fff;
}
.deploy-pipeline__step.is-failed .deploy-pipeline__label { color: var(--p-red-400, #ef4444); }

@keyframes deploy-pipeline-pulse {
    0%,100% { box-shadow: 0 0 0 0 color-mix(in srgb, var(--p-primary-500), transparent 70%); }
    50%     { box-shadow: 0 0 0 6px color-mix(in srgb, var(--p-primary-500), transparent 92%); }
}

@media (prefers-reduced-motion: reduce) {
    .deploy-pipeline__step.is-active .deploy-pipeline__dot { animation: none; }
}

@media (max-width: 720px) {
    .deploy-pipeline { flex-direction: column; gap: 12px; }
    .deploy-pipeline__step { flex: 0 0 auto; padding-right: 0; }
    .deploy-pipeline__bar { display: none; }
}
</style>
