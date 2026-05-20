<template>
    <DataTable :value="safeMembers" :empty-message="t('settings.teams.noMembers')" class="settings-table">
        <Column :header="t('settings.teams.columns.person')">
            <template #body="{ data }">
                <div>
                    <div>{{ data.name || data.email }}</div>
                    <small>{{ data.email }}</small>
                </div>
            </template>
        </Column>

        <Column :header="t('settings.teams.columns.role')" :style="{ width: '260px' }">
            <template #body="{ data }">
                <Select
                    :model-value="data.team_role?.id"
                    :options="safeRoles"
                    option-value="id"
                    option-label="name"
                    @update:modelValue="changeRole(data, $event)"
                />
            </template>
        </Column>

        <Column :header="t('settings.teams.columns.status')" :style="{ width: '140px' }">
            <template #body="{ data }">
                <Tag :value="statusLabel(data.status)" :severity="statusSeverity(data.status)" />
            </template>
        </Column>

        <Column :style="{ width: '72px' }">
            <template #body="{ data }">
                <Button text rounded severity="danger" icon="pi pi-times" @click="emit('remove', data)" />
            </template>
        </Column>
    </DataTable>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Column from 'primevue/column'
import DataTable from 'primevue/datatable'
import Select from 'primevue/select'
import Tag from 'primevue/tag'
import type { Member, Role } from '@/types/api'

const props = withDefaults(defineProps<{
    members?: Member[] | null
    roles?: Role[] | null
}>(), {
    members: () => [],
    roles: () => [],
})

const emit = defineEmits<{
    'change-role': [payload: { member: Member; roleID: string }]
    remove: [member: Member]
}>()

const { t } = useI18n()
const safeMembers = computed(() => Array.isArray(props.members) ? props.members : [])
const safeRoles = computed(() => Array.isArray(props.roles) ? props.roles : [])

function changeRole(member: Member, value: string | number | null) {
    const roleID = value ? String(value) : ''
    if (!roleID || member.team_role?.id === roleID) return
    emit('change-role', { member, roleID })
}

function statusLabel(status: Member['status']) {
    if (status === 'blocked') return t('settings.teams.status.blocked')
    if (status === 'inactive') return t('settings.teams.status.inactive')
    return t('settings.teams.status.active')
}

function statusSeverity(status: Member['status']) {
    if (status === 'blocked') return 'danger'
    if (status === 'inactive') return 'warn'
    return 'success'
}
</script>
