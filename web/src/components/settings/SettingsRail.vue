<template>
    <!--
        Despite the name, this rail is reused beyond /settings. Apps
        detail page also mounts it as the left navigation. The `#brand`
        slot lets consumers replace the default kicker/title/sub block
        (which is fine for Settings but too rigid for cases that need
        a status badge / breadcrumb / etc. in the header).
    -->
    <nav class="settings-rail">
        <header class="settings-rail__brand">
            <slot name="brand">
                <div class="settings-rail__kicker">{{ kicker }}</div>
                <h2 class="settings-rail__name">{{ title }}</h2>
                <div class="settings-rail__sub">{{ subtitle }}</div>
            </slot>
        </header>

        <div v-for="group in groups" :key="group.label" class="settings-rail__group">
            <div class="settings-rail__group-label">{{ group.label }}</div>
            <button
                v-for="item in group.items"
                :key="item.id"
                type="button"
                :class="['rail-item', item.id === activeId && 'active']"
                @click="$emit('select', item)"
            >
                <span class="rail-icon">
                    <component :is="item.icon" :size="14" :stroke-width="2" />
                </span>
                <span class="rail-item__main">
                    <span class="rail-item__label">{{ item.label }}</span>
                    <span class="rail-item__desc">{{ item.desc }}</span>
                </span>
                <IconChevronRight class="rail-item__chevron" :class="item.id === activeId && 'is-active'" :size="14" />
            </button>
        </div>
    </nav>
</template>

<script setup lang="ts">
import type { Component } from 'vue'
import IconChevronRight from '@/components/icons/IconChevronRight.vue'

export interface RailItem {
    id: string
    label: string
    desc: string
    icon: Component
    route?: string
}

export interface RailGroup {
    label: string
    items: RailItem[]
}

defineProps<{
    groups: RailGroup[]
    activeId: string
    kicker: string
    title: string
    subtitle: string
}>()

defineEmits<{
    select: [item: RailItem]
}>()
</script>

<style scoped>
.settings-rail {
    width: 300px;
    flex: 0 0 300px;
    padding: 28px 16px 24px;
    border-right: 1px solid var(--p-divider);
    background: var(--p-bg);
    /*
        Sticky-to-top so the rail (and its right border) stay visible while
        the operator scrolls a long settings page. The scroll container is
        the document itself (AppLayout has no inner overflow), so `top: 0`
        is the viewport top.

        `align-self: flex-start` prevents the default flex `stretch` from
        making the rail as tall as the (much taller) settings-main sibling
        — a stretched item has nothing to "stick to" because it never
        scrolls out of its own bounds.

        `height: 100dvh` (NOT max-height) is the key to the visible left
        divider. With intrinsic height the rail's `border-right` would
        only extend as far as the rail's content (~600px), leaving the
        lower half of the viewport with no visible boundary between rail
        and content. Forcing the full viewport height keeps the border
        running the entire vertical edge regardless of how much nav fits.

        Internal overflow-y still allows the rail to scroll its own list
        on very short viewports without ever moving the page scrollbar.
    */
    position: sticky;
    top: 0;
    align-self: flex-start;
    height: 100dvh;
    overflow-y: auto;
    overscroll-behavior: contain;
    scrollbar-width: thin;
}
.settings-rail__brand {
    padding: 0 12px 18px;
    border-bottom: 1px solid var(--p-divider);
    margin-bottom: 18px;
}
.settings-rail__kicker {
    color: var(--p-text-muted);
    font-size: 11px;
    font-weight: 700;
    letter-spacing: .06em;
    text-transform: uppercase;
}
.settings-rail__name {
    margin: 4px 0 0;
    color: var(--p-text);
    font-size: 20px;
    font-weight: 750;
    letter-spacing: 0;
}
.settings-rail__sub {
    margin-top: 2px;
    color: var(--p-text-muted);
    font-size: 12px;
}
.settings-rail__group {
    margin-bottom: 18px;
}
.settings-rail__group-label {
    padding: 0 12px 6px;
    color: var(--p-text-muted);
    font-size: 10px;
    font-weight: 700;
    letter-spacing: .08em;
    text-transform: uppercase;
}
.rail-item {
    display: flex;
    align-items: center;
    gap: 12px;
    width: 100%;
    margin-bottom: 2px;
    border: 0;
    border-radius: 8px;
    background: transparent;
    color: var(--p-text);
    cursor: pointer;
    font: inherit;
    padding: 10px 12px;
    text-align: left;
    transition: background 120ms, color 120ms;
}
.rail-item:hover {
    background: var(--p-hover);
}
.rail-item.active {
    background: color-mix(in srgb, var(--p-primary-color), transparent 88%);
    color: var(--p-primary-color);
}
.rail-icon {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    width: 30px;
    height: 30px;
    border: 1px solid var(--p-divider);
    border-radius: 8px;
    background: var(--p-content-bg);
    color: var(--p-text-subtle);
    transition: background 120ms, color 120ms, border-color 120ms;
}
.rail-item.active .rail-icon {
    border-color: var(--p-primary-color);
    background: var(--p-primary-color);
    color: var(--p-primary-contrast);
}
.rail-item__main {
    flex: 1;
    min-width: 0;
}
.rail-item__label,
.rail-item__desc {
    display: block;
}
.rail-item__label {
    font-size: 13px;
    font-weight: 700;
    line-height: 1.2;
}
.rail-item__desc {
    margin-top: 2px;
    color: var(--p-text-muted);
    font-size: 11px;
    line-height: 1.35;
}
.rail-item__chevron {
    color: var(--p-primary-color);
    opacity: 0;
}
.rail-item__chevron.is-active {
    opacity: 1;
}
@media (max-width: 900px) {
    .settings-rail {
        width: 100%;
        flex: none;
        padding: 0 0 16px;
        border-right: 0;
        background: transparent;
        /* On mobile the rail stacks above the content; sticky would pin a
           full-width block to the top of the viewport and eat content space.
           Reset back to static + drop the viewport-height lock so it sizes
           to its own content. */
        position: static;
        height: auto;
        max-height: none;
        overflow-y: visible;
    }
    .settings-rail__brand {
        padding: 0 16px 16px;
        border-bottom: 1px solid var(--p-divider);
    }
    .settings-rail__group-label {
        padding: 20px 16px 6px;
    }
    .rail-item {
        margin: 0;
        border-top: 1px solid var(--p-divider);
        border-radius: 0;
        padding: 14px 16px;
    }
    .rail-item:last-child {
        border-bottom: 1px solid var(--p-divider);
    }
    .rail-icon {
        width: 32px;
        height: 32px;
    }
    .rail-item__chevron {
        opacity: 1;
        color: var(--p-text-muted);
    }
}
</style>
