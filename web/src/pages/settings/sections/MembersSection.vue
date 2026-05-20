<template>
    <SCard :title="t('settings.members.title')" :sub="t('settings.members.sub')">
        <div class="section-toolbar">
            <div class="muted">{{ memberCountLabel }}</div>
            <Button :label="t('settings.members.new')" icon="pi pi-plus" @click="openCreate" />
        </div>

        <DataTable :value="members" :loading="loading" class="settings-table">
            <Column :header="t('settings.members.columns.person')">
                <template #body="{ data }">
                    <div class="person-cell">
                        <UiAvatar :name="data.name || data.email" :src="data.avatar_url" size="sm" />
                        <div>
                            <div class="person-cell__name">{{ data.name || data.email }}</div>
                            <div class="person-cell__email">{{ data.email }}</div>
                        </div>
                    </div>
                </template>
            </Column>
            <Column :header="t('settings.members.columns.role')">
                <template #body="{ data }">{{ data.role?.name ?? '—' }}</template>
            </Column>
            <Column :header="t('settings.members.columns.teams')">
                <template #body="{ data }">
                    <div class="chips">
                        <span v-if="!data.teams?.length" class="muted">{{ t('settings.members.teamsGlobalTag') }}</span>
                        <span v-for="team in data.teams" :key="team.id" class="chip" :style="{ '--team-color': team.color }">
                            {{ team.name }}
                        </span>
                    </div>
                </template>
            </Column>
            <Column :header="t('settings.members.columns.status')">
                <template #body="{ data }">
                    <span :class="['status-pill', `status-pill--${data.status}`]">{{ statusLabel(data.status) }}</span>
                </template>
            </Column>
            <Column header="" :style="{ width: '92px' }">
                <template #body="{ data }">
                    <Button text size="small" icon="pi pi-pencil" @click="openEdit(data)" />
                    <Button text size="small" icon="pi pi-trash" severity="danger" @click="askDelete(data)" />
                </template>
            </Column>
        </DataTable>
    </SCard>

    <Dialog v-model:visible="dialogOpen" modal :header="editing ? t('settings.members.edit') : t('settings.members.new')" :style="{ width: '560px' }">
        <div class="dialog-body">
            <SField :label="t('settings.teams.fields.name')">
                <InputText v-model="draft.name" />
            </SField>
            <SField :label="t('settings.profile.email')">
                <InputText v-model="draft.email" type="email" :disabled="!!editing" />
            </SField>
            <SField v-if="!editing" :label="t('settings.members.initialPassword')">
                <Password v-model="draft.password" toggle-mask fluid />
            </SField>
            <SField :label="t('settings.teams.columns.role')">
                <UiSelect v-model="draft.role_id" :options="roleOptions" />
            </SField>
            <SField v-if="editing" :label="t('settings.members.status.label')">
                <UiSelect v-model="draft.status" :options="statusOptions">
                    <template #value="{ value }">
                        <span class="status-option">
                            <span :class="['status-option__dot', `status-option__dot--${value}`]" />
                            {{ statusLabel(value as Member['status']) }}
                        </span>
                    </template>
                    <template #option="{ option }">
                        <span class="status-option">
                            <span :class="['status-option__dot', `status-option__dot--${option.value}`]" />
                            {{ option.label }}
                        </span>
                    </template>
                </UiSelect>
            </SField>
            <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>
        </div>
        <template #footer>
            <Button text :label="t('common.cancel')" @click="dialogOpen = false" />
            <Button :label="t('common.save')" :loading="saving" @click="save" />
        </template>
    </Dialog>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Column from 'primevue/column'
import DataTable from 'primevue/datatable'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import Password from 'primevue/password'
import { useConfirm } from 'primevue/useconfirm'
import SCard from '@/components/settings/SCard.vue'
import SField from '@/components/settings/SField.vue'
import UiAvatar from '@/components/ui/UiAvatar.vue'
import UiSelect from '@/components/ui/UiSelect.vue'
import { apiErrorMessage } from '@/composables/useApi'
import { notify } from '@/lib/notify'
import { rbacService } from '@/services/rbac'
import type { Member, Role } from '@/types/api'

const { t } = useI18n()
const confirm = useConfirm()
const loading = ref(false)
const saving = ref(false)
const error = ref<string | null>(null)
const members = ref<Member[]>([])
const roles = ref<Role[]>([])
const dialogOpen = ref(false)
const editing = ref<Member | null>(null)
const draft = ref({
    name: '',
    email: '',
    password: '',
    role_id: '',
    status: 'active' as Member['status'],
})
const statusOptions = computed(() => [
    { label: t('settings.members.status.active'), value: 'active' },
    { label: t('settings.members.status.inactive'), value: 'inactive' },
    { label: t('settings.members.status.blocked'), value: 'blocked' },
])
const roleOptions = computed(() => [
    { label: t('settings.members.roleNoneGlobal'), value: '' },
    ...roles.value
        .filter((role) => {
            const scope = role.scope ?? 'both'
            return scope === 'global' || scope === 'both'
        })
        .map((role) => ({ label: role.name, value: role.id })),
])
const memberCountLabel = computed(() => {
    const n = members.value.length
    if (n === 0) return t('settings.members.countZero')
    return t('settings.members.count', n, { named: { n } })
})

onMounted(load)

async function load() {
    loading.value = true
    try {
        const [m, r] = await Promise.all([rbacService.members(), rbacService.roles()])
        members.value = m
        roles.value = r
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        loading.value = false
    }
}

function openCreate() {
    editing.value = null
    error.value = null
    draft.value = { name: '', email: '', password: '', role_id: roles.value[0]?.id ?? '', status: 'active' }
    dialogOpen.value = true
}

function openEdit(member: Member) {
    editing.value = member
    error.value = null
    draft.value = {
        name: member.name ?? '',
        email: member.email,
        password: '',
        role_id: member.role?.id ?? '',
        status: member.status,
    }
    dialogOpen.value = true
}

async function save() {
    saving.value = true
    error.value = null
    try {
        if (editing.value) {
            await rbacService.updateMember(editing.value.id, {
                name: draft.value.name,
                role_id: draft.value.role_id,
                status: draft.value.status,
            })
            notify.success(t('settings.members.updatedToast'))
        } else {
            await rbacService.createMember({
                email: draft.value.email,
                name: draft.value.name,
                password: draft.value.password,
                role_id: draft.value.role_id,
                team_ids: [],
            })
            notify.success(t('settings.members.createdToast'))
        }
        dialogOpen.value = false
        await load()
    } catch (e) {
        error.value = apiErrorMessage(e)
    } finally {
        saving.value = false
    }
}

function askDelete(member: Member) {
    confirm.require({
        header: t('settings.members.remove.title'),
        message: t('settings.members.remove.body', { email: member.email }),
        icon: 'pi pi-exclamation-triangle',
        rejectLabel: t('common.cancel'),
        acceptLabel: t('settings.members.remove.accept'),
        acceptClass: 'p-button-danger',
        accept: () => { void deleteMember(member.id) },
    })
}

async function deleteMember(id: string) {
    try {
        await rbacService.deleteMember(id)
        notify.success(t('settings.members.removedToast'))
        await load()
    } catch (e) {
        notify.error(apiErrorMessage(e))
    }
}

function statusLabel(status: Member['status']) {
    if (status === 'blocked') return t('settings.members.status.blocked')
    if (status === 'inactive') return t('settings.members.status.inactive')
    return t('settings.members.status.active')
}
</script>

<style scoped>
.section-toolbar { display: flex; justify-content: space-between; align-items: center; margin-bottom: 14px; }
.settings-table { border: 1px solid var(--p-divider); border-radius: 8px; overflow: hidden; }
.person-cell { display: flex; align-items: center; gap: 10px; }
.person-cell__name { color: var(--p-text); font-weight: 650; }
.person-cell__email, .muted { color: var(--p-text-muted); font-size: 12px; }
.chips { display: flex; flex-wrap: wrap; gap: 6px; }
.chip {
    border-radius: 999px;
    padding: 2px 8px;
    background: color-mix(in srgb, var(--team-color), transparent 86%);
    color: var(--p-text);
    font-size: 12px;
    border: 1px solid color-mix(in srgb, var(--team-color), transparent 60%);
}
.status-pill { border-radius: 999px; padding: 2px 8px; font-size: 12px; font-weight: 650; }
.status-pill--active { color: #16a34a; background: rgba(22, 163, 74, .12); }
.status-pill--inactive { color: var(--p-text-muted); background: var(--p-hover); }
.status-pill--blocked { color: #dc2626; background: rgba(220, 38, 38, .12); }
.status-option { display: inline-flex; align-items: center; gap: 8px; }
.status-option__dot { width: 8px; height: 8px; border-radius: 999px; }
.status-option__dot--active { background: #16a34a; }
.status-option__dot--inactive { background: var(--p-text-muted); }
.status-option__dot--blocked { background: #dc2626; }
.dialog-body { display: flex; flex-direction: column; gap: 12px; padding-top: 8px; }
:deep(.p-inputtext), :deep(.p-select), :deep(.p-password) { width: 100%; }
</style>
