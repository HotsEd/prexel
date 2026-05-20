<template>
    <form class="team-add-member-form" @submit.prevent="submit">
        <Fluid>
            <SField :label="t('settings.teams.fields.member')">
                <UiSelect
                    v-model="selectedMemberID"
                    :options="memberOptions"
                    filter
                    clearable
                    :placeholder="t('settings.teams.addMember')"
                >
                    <template #value="{ value, placeholder }">
                        <span v-if="selectedMemberOption(value)" class="member-select-row">
                            <UiAvatar
                                :name="selectedMemberOption(value)?.label"
                                :src="selectedMemberOption(value)?.member.avatar_url"
                                size="sm"
                            />
                            <span>{{ selectedMemberOption(value)?.label }}</span>
                        </span>
                        <span v-else>{{ placeholder }}</span>
                    </template>
                    <template #option="{ option }">
                        <span class="member-select-row">
                            <UiAvatar :name="option.label" :src="option.member.avatar_url" size="sm" />
                            <span class="member-select-row__text">
                                <strong>{{ option.member.name || option.member.email }}</strong>
                                <small v-if="option.member.name">{{ option.member.email }}</small>
                            </span>
                        </span>
                    </template>
                </UiSelect>
            </SField>

            <SField :label="t('settings.teams.fields.teamRole')">
                <UiSelect
                    v-model="selectedRoleID"
                    :options="roleOptions"
                    clearable
                    :placeholder="t('settings.teams.teamRole')"
                />
            </SField>
        </Fluid>

        <section class="role-preview">
            <div class="role-preview__head">
                <div>
                    <h4>{{ t('settings.teams.rolePreviewTitle') }}</h4>
                    <p v-if="selectedRole">{{ selectedRole.description || t('settings.roles.noDescription') }}</p>
                    <p v-else>{{ t('settings.teams.noRoleSelected') }}</p>
                </div>
                <Tag v-if="selectedRole?.is_admin" severity="danger" :value="t('settings.permissions.all')" />
                <Tag v-else-if="selectedRole" severity="secondary" :value="String(selectedRolePermissionItems.length)" />
            </div>

            <Message v-if="selectedRole?.is_admin" severity="warn" :closable="false">
                {{ t('settings.roles.adminHint') }}
            </Message>

            <div v-else-if="selectedRolePermissionItems.length" class="role-preview__badges">
                <Tag
                    v-for="permission in selectedRolePermissionItems"
                    :key="permission.slug"
                    severity="secondary"
                    :value="permissionName(permission)"
                    :title="permissionDescription(permission)"
                />
            </div>

            <Message v-else-if="selectedRole" severity="info" :closable="false">
                {{ t('settings.teams.noRolePermissions') }}
            </Message>
        </section>

        <Message v-if="!safeRoles.length" severity="warn" :closable="false">
            {{ t('settings.teams.noTeamRoles') }}
        </Message>

        <footer class="team-add-member-form__actions">
            <Button v-if="showCancel" type="button" text :label="t('common.cancel')" @click="emit('cancel')" />
            <Button type="submit" :label="t('settings.teams.add')" :loading="saving" />
        </footer>
    </form>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import Fluid from 'primevue/fluid'
import Message from 'primevue/message'
import Tag from 'primevue/tag'
import SField from '@/components/settings/SField.vue'
import UiAvatar from '@/components/ui/UiAvatar.vue'
import UiSelect from '@/components/ui/UiSelect.vue'
import { notify } from '@/lib/notify'
import type { Member, PermissionDefinition, Role } from '@/types/api'

const props = withDefaults(defineProps<{
    members?: Member[] | null
    roles?: Role[] | null
    permissions?: PermissionDefinition[] | null
    saving?: boolean
    showCancel?: boolean
}>(), {
    members: () => [],
    roles: () => [],
    permissions: () => [],
    saving: false,
    showCancel: false,
})

const emit = defineEmits<{
    submit: [payload: { userID: string; roleID: string }]
    cancel: []
}>()

const { t, te } = useI18n()
const selectedMemberID = ref<string | null>(null)
const selectedRoleID = ref<string | null>(null)
const safeMembers = computed(() => Array.isArray(props.members) ? props.members : [])
const safeRoles = computed(() => Array.isArray(props.roles) ? props.roles : [])
const safePermissions = computed(() => Array.isArray(props.permissions) ? props.permissions : [])
const memberOptions = computed(() => safeMembers.value.map((member) => ({
    label: memberLabel(member),
    value: member.id,
    member,
})))
const roleOptions = computed(() => safeRoles.value.map((role) => ({
    label: role.name,
    value: role.id,
})))
const permissionMap = computed(() => {
    const map = new Map<string, PermissionDefinition>()
    for (const permission of safePermissions.value) map.set(permission.slug, permission)
    return map
})
const selectedRole = computed(() => safeRoles.value.find((role) => role.id === selectedRoleID.value) ?? null)
const selectedRolePermissionItems = computed(() => {
    if (!selectedRole.value || selectedRole.value.is_admin) return []
    return selectedRole.value.permissions.map((slug) => permissionMap.value.get(slug) ?? {
        slug,
        group: '',
        name: slug,
        description: '',
    })
})

const defaultRoleID = computed(() => {
    const role = safeRoles.value.find((item) => item.slug === 'developer')
        ?? safeRoles.value.find((item) => !item.is_admin)
        ?? safeRoles.value[0]
    return role?.id ?? null
})

watch(safeRoles, () => {
    if (!selectedRoleID.value || !safeRoles.value.some((role) => role.id === selectedRoleID.value)) {
        selectedRoleID.value = defaultRoleID.value
    }
}, { immediate: true })

watch(safeMembers, () => {
    if (selectedMemberID.value && !safeMembers.value.some((member) => member.id === selectedMemberID.value)) {
        selectedMemberID.value = null
    }
})

function memberLabel(member: Member) {
    return member.name ? `${member.name} (${member.email})` : member.email
}

function selectedMemberOption(id: string | number | null) {
    return memberOptions.value.find((item) => item.value === id)
}

function permissionName(permission: PermissionDefinition): string {
    const path = `settings.permissions.items.${i18nKey(permission.slug)}.name`
    return te(path) ? t(path) : permission.name
}

function permissionDescription(permission: PermissionDefinition): string {
    const path = `settings.permissions.items.${i18nKey(permission.slug)}.desc`
    return te(path) ? t(path) : permission.description
}

function i18nKey(value: string): string {
    return value.replace(/[^a-zA-Z0-9]/g, '_')
}

function submit() {
    if (!safeRoles.value.length) {
        notify.error(t('settings.teams.noTeamRoles'))
        return
    }
    if (!selectedMemberID.value) {
        notify.error(t('settings.teams.selectMemberFirst'))
        return
    }
    const roleID = selectedRoleID.value || defaultRoleID.value
    if (!roleID) {
        notify.error(t('settings.teams.selectRoleFirst'))
        return
    }
    emit('submit', { userID: selectedMemberID.value, roleID })
}
</script>

<style scoped>
.team-add-member-form {
    display: flex;
    flex-direction: column;
    gap: 16px;
}

.team-add-member-form :deep(.p-fluid) {
    display: flex;
    flex-direction: column;
    gap: 14px;
}

.member-select-row {
    display: inline-flex;
    align-items: center;
    gap: 10px;
    min-width: 0;
}

.member-select-row__text {
    display: flex;
    min-width: 0;
    flex-direction: column;
    gap: 2px;
}

.member-select-row__text strong {
    overflow: hidden;
    color: var(--p-text);
    font-size: 13px;
    font-weight: 650;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.member-select-row__text small {
    overflow: hidden;
    color: var(--p-text-muted);
    font-size: 12px;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.role-preview {
    display: flex;
    flex-direction: column;
    gap: 12px;
    padding: 14px;
    border: 1px solid var(--p-content-border);
    border-radius: var(--p-r-md);
    background: var(--p-content-bg);
}

.role-preview__head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
}

.role-preview__head h4,
.role-preview__head p {
    margin: 0;
}

.role-preview__head h4 {
    color: var(--p-text);
    font-size: 14px;
    font-weight: 700;
}

.role-preview__head p {
    margin-top: 4px;
    color: var(--p-text-muted);
    font-size: 12px;
    line-height: 1.4;
}

.role-preview__badges {
    display: flex;
    max-height: 112px;
    flex-wrap: wrap;
    gap: 6px;
    overflow: auto;
}

.team-add-member-form__actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
}
</style>
