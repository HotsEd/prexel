<template>
    <div class="dns-table-wrap">
        <table class="dns-table">
            <thead>
                <tr>
                    <th>{{ t('domains.wizard.step3.col_type') }}</th>
                    <th>{{ t('domains.wizard.step3.col_name') }}</th>
                    <th>{{ t('domains.wizard.step3.col_value') }}</th>
                </tr>
            </thead>
            <tbody>
                <tr v-for="(row, i) in rows" :key="i">
                    <td><span class="record-tag">{{ row.type }}</span></td>
                    <td class="mono">{{ row.name }}</td>
                    <td class="mono">{{ row.value }}</td>
                </tr>
            </tbody>
        </table>
        <p class="dns-table__hint">{{ t('domains.wizard.step3.records_hint') }}</p>
    </div>
</template>

<script setup lang="ts">
/*
    Renders the "create these records at your DNS provider" table used in
    Step 3 of the add-domain wizard. Extracted into its own component so
    every variant (apex, single subdomain, wildcard) reuses the same
    styled table without copy-pasting markup.
*/
import { useI18n } from 'vue-i18n'

defineProps<{
    rows: Array<{ type: string; name: string; value: string }>
}>()

const { t } = useI18n()
</script>

<style scoped>
.dns-table-wrap {
    border: 1px solid var(--p-content-border);
    border-radius: 10px;
    overflow: hidden;
    background: var(--p-bg);
}
.dns-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 13px;
}
.dns-table th {
    text-align: left;
    padding: 10px 14px;
    background: color-mix(in srgb, var(--p-primary-500), transparent 96%);
    color: var(--p-text-muted);
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    border-bottom: 1px solid var(--p-content-border);
}
.dns-table td {
    padding: 12px 14px;
    color: var(--p-text);
    border-bottom: 1px solid var(--p-divider);
}
.dns-table tbody tr:last-child td { border-bottom: 0; }

.record-tag {
    display: inline-flex;
    padding: 2px 8px;
    border-radius: 6px;
    background: var(--p-primary-500);
    color: var(--p-primary-contrast);
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.04em;
}

.dns-table__hint {
    margin: 0;
    padding: 10px 14px;
    font-size: 11px;
    color: var(--p-text-muted);
    border-top: 1px dashed var(--p-divider);
    background: var(--p-content-bg);
}
</style>
