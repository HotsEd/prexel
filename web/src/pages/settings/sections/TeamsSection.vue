<template>
    <SCard :title="t('settings.teams.title')" :sub="t('settings.teams.sub')">
        <template #action>
            <Button :label="t('settings.teams.new')" icon="pi pi-plus" @click="openCreateTeam" />
        </template>

        <div class="teams-workbench">
            <aside class="teams-list">
                <div class="teams-list__head">
                    <span>{{ t('settings.teams.columns.team') }}</span>
                    <span>{{ t('settings.teams.columns.members') }}</span>
                    <span>{{ t('settings.teams.columns.apps') }}</span>
                </div>

                <button
                    v-for="team in teams"
                    :key="team.id"
                    type="button"
                    class="team-row"
                    :class="selectedID === team.id && 'is-active'"
                    @click="selectTeam(team.id)"
                >
                    <span class="team-dot" :style="{ background: team.color }" />
                    <span class="team-row__main">
                        <span class="team-row__name">{{ team.name }}</span>
                        <span class="team-row__meta">{{ team.slug }}</span>
                    </span>
                    <span class="team-row__count">{{ team.member_count }}</span>
                    <span class="team-row__count">{{ team.app_count }}</span>
                </button>

                <button type="button" class="team-row team-row--create" @click="openCreateTeam">
                    <span class="team-row__plus">+</span>
                    <span class="team-row__main">
                        <span class="team-row__name">{{ t('settings.teams.new') }}</span>
                        <span class="team-row__meta">{{ t('settings.teams.createScope') }}</span>
                    </span>
                </button>

                <div v-if="!loading && !teams.length" class="teams-empty">
                    {{ t('settings.teams.empty') }}
                </div>
            </aside>

            <section class="team-detail">
                <div v-if="selectedTeam" class="team-overview">
                    <header class="team-detail__head">
                        <div class="team-detail__title">
                            <span class="team-detail__dot" :style="{ background: selectedTeam.color }" />
                            <div>
                                <h3>{{ selectedTeam.name }}</h3>
                                <p>{{ selectedTeam.slug }}</p>
                            </div>
                        </div>
                        <div class="team-detail__actions">
                            <Button text icon="pi pi-pencil" :label="t('settings.teams.edit')" @click="openEditTeam(selectedTeam)" />
                            <Button
                                text
                                severity="danger"
                                icon="pi pi-trash"
                                :label="t('settings.teams.remove')"
                                :disabled="selectedTeam.app_count > 0"
                                :title="selectedTeam.app_count > 0 ? t('settings.teams.deleteBlocked', { count: selectedTeam.app_count }) : ''"
                                @click="askDelete(selectedTeam)"
                            />
                        </div>
                    </header>

                    <div class="team-stats">
                        <div>
                            <span class="team-stats__value">{{ teamMembersCount }}</span>
                            <span class="team-stats__label">{{ t('settings.teams.columns.members') }}</span>
                        </div>
                        <div>
                            <span class="team-stats__value">{{ selectedTeam.app_count }}</span>
                            <span class="team-stats__label">{{ t('settings.teams.columns.apps') }}</span>
                        </div>
                        <div>
                            <span class="team-stats__value">{{ selectedTeam.slug }}</span>
                            <span class="team-stats__label">{{ t('settings.teams.scopeIdentifier') }}</span>
                        </div>
                    </div>

                    <Message v-if="selectedTeam.app_count > 0" severity="warn" :closable="false" class="team-blocked-message">
                        {{ t('settings.teams.deleteBlocked', { count: selectedTeam.app_count }) }}
                    </Message>

                    <div class="team-description">
                        <span>{{ t('settings.teams.fields.description') }}</span>
                        <p>{{ selectedTeam.description || t('settings.teams.noDescription') }}</p>
                    </div>

                    <section class="members-panel">
                        <header class="members-panel__head">
                            <div>
                                <h4>{{ t('settings.teams.membersTitle') }}</h4>
                                <p>{{ t('settings.teams.membersSub') }}</p>
                            </div>
                            <Button
                                :label="t('settings.teams.addMemberAction')"
                                icon="pi pi-user-plus"
                                @click="openAddMemberDialog"
                            />
                        </header>

                        <TeamMembersTable
                            :members="teamMembers"
                            :roles="teamRoles"
                            @change-role="changeMemberRole"
                            @remove="removeMember"
                        />
                    </section>
                </div>

                <div v-else class="team-placeholder">
                    <h3>{{ t('settings.teams.placeholderTitle') }}</h3>
                    <p>{{ t('settings.teams.placeholderSub') }}</p>
                    <Button :label="t('settings.teams.new')" icon="pi pi-plus" @click="openCreateTeam" />
                </div>
            </section>
        </div>
    </SCard>

    <Dialog
        v-model:visible="teamDialogOpen"
        modal
        :header="teamDialogMode === 'create' ? t('settings.teams.createTitle') : t('settings.teams.editTitle')"
        :style="{ width: '760px', maxWidth: 'calc(100vw - 32px)' }"
    >
        <div class="team-dialog">
            <div class="team-form-grid">
                <SField :label="t('settings.teams.fields.name')"><InputText v-model="draft.name" placeholder="Platform" /></SField>
                <SField :label="t('settings.teams.fields.slug')"><InputText v-model="draft.slug" placeholder="platform" /></SField>
                <SField :label="t('settings.teams.fields.color')">
                    <div class="color-field">
                        <ColorPicker v-model="pickerColor" format="hex" />
                        <span class="color-field__preview" :style="{ background: draft.color }" />
                        <span class="color-field__value">{{ draft.color }}</span>
                    </div>
                </SField>
                <SField :label="t('settings.teams.fields.description')" class="team-form-grid__wide"><Textarea v-model="draft.description" rows="4" /></SField>
            </div>
            <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>
        </div>
        <template #footer>
            <Button text :label="t('common.cancel')" @click="teamDialogOpen = false" />
            <Button :label="teamDialogMode === 'create' ? t('settings.teams.createAction') : t('settings.teams.saveDetails')" :loading="saving" @click="saveTeamDialog" />
        </template>
    </Dialog>

    <Dialog
        v-model:visible="addMemberDialogOpen"
        modal
        :header="t('settings.teams.addMemberTitle')"
        :style="{ width: '560px', maxWidth: 'calc(100vw - 32px)' }"
    >
        <TeamAddMemberForm
            :key="memberFormKey"
            :members="availableMembers"
            :roles="teamRoles"
            :permissions="permissions"
            :saving="membersSaving"
            show-cancel
            @submit="addMemberToTeam"
            @cancel="addMemberDialogOpen = false"
        />
    </Dialog>

</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import ColorPicker from 'primevue/colorpicker'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import Textarea from 'primevue/textarea'
import { useConfirm } from 'primevue/useconfirm'
import SCard from '@/components/settings/SCard.vue'
import SField from '@/components/settings/SField.vue'
import TeamAddMemberForm from '@/components/settings/team/TeamAddMemberForm.vue'
import TeamMembersTable from '@/components/settings/team/TeamMembersTable.vue'
import { apiErrorMessage } from '@/composables/useApi'
import { notify } from '@/lib/notify'
import { rbacService } from '@/services/rbac'
import type { Member, PermissionDefinition, Role, Team } from '@/types/api'

type TeamDialogMode = 'create' | 'edit'

const confirm = useConfirm()
const { t } = useI18n()
const teams = ref<Team[]>([])
const roles = ref<Role[]>([])
const permissions = ref<PermissionDefinition[]>([])
const allMembers = ref<Member[]>([])
const teamMembers = ref<Member[]>([])
const selectedTeam = ref<Team | null>(null)
const selectedID = ref('')
const memberFormKey = ref(0)
const addMemberDialogOpen = ref(false)
const teamDialogOpen = ref(false)
const teamDialogMode = ref<TeamDialogMode>('create')
const loading = ref(false)
const detailLoading = ref(false)
const saving = ref(false)
const membersSaving = ref(false)
const error = ref<string | null>(null)
const draft = ref({ name: '', slug: '', color: '#10b981', description: '' })

const teamMemberIDs = computed(() => new Set(asArray(teamMembers.value).map((member) => member.id)))
const teamMembersCount = computed(() => asArray(teamMembers.value).length)
const availableMembers = computed(() => asArray(allMembers.value).filter((member) => !teamMemberIDs.value.has(member.id)))
const teamRoles = computed(() => asArray(roles.value).filter((role) => {
    const scope = role.scope ?? 'both'
    return scope === 'team' || scope === 'both'
}))
const pickerColor = computed({
    get: () => draft.value.color.replace(/^#/, ''),
    set: (value: string | null) => {
        const hex = String(value ?? '').replace(/^#/, '').trim()
        if (/^[0-9a-fA-F]{6}$/.test(hex)) {
            draft.value.color = `#${hex.toLowerCase()}`
        }
    },
})

onMounted(load)

async function load() {
    loading.value = true
    try {
        const [tms, members, loadedRoles, loadedPermissions] = await Promise.all([
            rbacService.teams(),
            rbacService.members(),
            rbacService.roles(),
            rbacService.permissions(),
        ])
        teams.value = asArray(tms)
        allMembers.value = asArray(members)
        roles.value = asArray(loadedRoles)
        permissions.value = asArray(loadedPermissions)
        const nextID = selectedID.value && teams.value.some((team) => team.id === selectedID.value)
            ? selectedID.value
            : teams.value[0]?.id ?? ''
        if (nextID) {
            await selectTeam(nextID, false)
        } else {
            selectedTeam.value = null
            teamMembers.value = []
        }
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        loading.value = false
    }
}

async function selectTeam(id: string, switchMode = true) {
    selectedID.value = id
    error.value = null
    if (switchMode) {
        teamDialogOpen.value = false
        addMemberDialogOpen.value = false
    }
    const cached = teams.value.find((team) => team.id === id)
    if (cached) {
        selectedTeam.value = cached
        draft.value = teamToDraft(cached)
        teamMembers.value = []
    }
    detailLoading.value = true
    try {
        const detail = await rbacService.getTeam(id)
        selectedTeam.value = detail.team
        teamMembers.value = asArray(detail.members)
        draft.value = teamToDraft(detail.team)
        const idx = teams.value.findIndex((team) => team.id === detail.team.id)
        if (idx >= 0) teams.value[idx] = detail.team
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        detailLoading.value = false
    }
}

function teamToDraft(team: Team) {
    return {
        name: team.name,
        slug: team.slug,
        color: team.color,
        description: team.description ?? '',
    }
}

function openCreateTeam() {
    teamDialogMode.value = 'create'
    error.value = null
    draft.value = { name: '', slug: '', color: '#10b981', description: '' }
    teamDialogOpen.value = true
}

function openEditTeam(team: Team) {
    teamDialogMode.value = 'edit'
    error.value = null
    draft.value = teamToDraft(team)
    teamDialogOpen.value = true
}

function openAddMemberDialog() {
    if (!selectedTeam.value) {
        notify.error(t('settings.teams.selectTeamFirst'))
        return
    }
    memberFormKey.value += 1
    addMemberDialogOpen.value = true
}

async function saveTeamDialog() {
    saving.value = true
    error.value = null
    try {
        if (teamDialogMode.value === 'create') {
            const created = await rbacService.createTeam({ ...draft.value, member_ids: [] })
            notify.success(t('settings.teams.created'))
            selectedID.value = created.id
            await load()
            await selectTeam(created.id)
        } else if (selectedTeam.value) {
            const updated = await rbacService.updateTeam(selectedTeam.value.id, draft.value)
            selectedTeam.value = updated
            const idx = teams.value.findIndex((team) => team.id === updated.id)
            if (idx >= 0) teams.value[idx] = updated
            notify.success(t('settings.teams.updated'))
        }
        teamDialogOpen.value = false
    } catch (e) {
        error.value = apiErrorMessage(e)
    } finally {
        saving.value = false
    }
}

async function addMemberToTeam(payload: { userID: string; roleID: string }) {
    if (!selectedTeam.value) {
        notify.error(t('settings.teams.selectTeamFirst'))
        return
    }
    const next = membershipPayload()
    next.push({ user_id: payload.userID, role_id: payload.roleID })
    const saved = await setMembers(next, t('settings.teams.memberAdded'))
    if (saved) {
        memberFormKey.value += 1
        addMemberDialogOpen.value = false
    }
}

async function removeMember(member: Member) {
    if (!selectedTeam.value) return
    const next = membershipPayload().filter((entry) => entry.user_id !== member.id)
    await setMembers(next, t('settings.teams.memberRemoved'))
}

async function changeMemberRole(payload: { member: Member; roleID: string }) {
    const { member, roleID } = payload
    if (!selectedTeam.value || !roleID || member.team_role?.id === roleID) return
    const next = membershipPayload().map((entry) => entry.user_id === member.id ? { ...entry, role_id: roleID } : entry)
    await setMembers(next, t('settings.teams.memberRoleUpdated'))
}

function membershipPayload(): Array<{ user_id: string; role_id: string }> {
    const fallbackRoleID = defaultTeamRoleID()
    return asArray(teamMembers.value)
        .map((member) => ({ user_id: member.id, role_id: member.team_role?.id || fallbackRoleID || '' }))
        .filter((entry) => entry.role_id)
}

function defaultTeamRoleID(): string | null {
    const role = teamRoles.value.find((item) => item.slug === 'developer')
        ?? teamRoles.value.find((item) => !item.is_admin)
        ?? teamRoles.value[0]
    return role?.id ?? null
}

async function setMembers(members: Array<{ user_id: string; role_id: string }>, success: string): Promise<boolean> {
    if (!selectedTeam.value) return false
    membersSaving.value = true
    try {
        await rbacService.setTeamMembers(selectedTeam.value.id, members)
        notify.success(success)
        await refreshTeamAndCounts()
        return true
    } catch (e) {
        notify.error(apiErrorMessage(e))
        return false
    } finally {
        membersSaving.value = false
    }
}

async function refreshTeamAndCounts() {
    if (!selectedTeam.value) return
    const selected = selectedTeam.value.id
    const [tms, members] = await Promise.all([rbacService.teams(), rbacService.members()])
    teams.value = asArray(tms)
    allMembers.value = asArray(members)
    await selectTeam(selected, false)
}

function asArray<T>(value: T[] | null | undefined): T[] {
    return Array.isArray(value) ? value : []
}

function askDelete(team: Team) {
    if (team.app_count > 0) {
        notify.error(t('settings.teams.deleteBlocked', { count: team.app_count }))
        return
    }
    confirm.require({
        header: t('settings.teams.deleteTitle'),
        message: t('settings.teams.deleteMessage', { name: team.name }),
        icon: 'pi pi-exclamation-triangle',
        rejectLabel: t('common.cancel'),
        acceptLabel: t('settings.teams.remove'),
        acceptClass: 'p-button-danger',
        accept: () => { void deleteTeam(team.id) },
    })
}

async function deleteTeam(id: string) {
    try {
        await rbacService.deleteTeam(id)
        notify.success(t('settings.teams.removed'))
        selectedID.value = ''
        selectedTeam.value = null
        teamMembers.value = []
        await load()
    } catch (e) {
        notify.error(apiErrorMessage(e))
    }
}

</script>

<style scoped>
.teams-workbench {
    display: grid;
    grid-template-columns: minmax(360px, 430px) minmax(0, 1fr);
    gap: 24px;
}
.teams-list,
.team-detail {
    border: 1px solid var(--p-divider);
    border-radius: 10px;
    background: var(--p-bg);
}
.teams-list {
    padding: 24px;
    align-self: start;
}
.teams-list__head {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 74px 54px;
    gap: 10px;
    padding: 0 12px 16px;
    color: var(--p-text-muted);
    font-size: 11px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: .05em;
}
.team-row {
    display: grid;
    grid-template-columns: 14px minmax(0, 1fr) 74px 54px;
    align-items: center;
    gap: 10px;
    width: 100%;
    border: 0;
    border-radius: 8px;
    padding: 12px;
    background: transparent;
    color: var(--p-text);
    cursor: pointer;
    text-align: left;
    font: inherit;
}
.team-row + .team-row {
    margin-top: 2px;
}
.team-row:hover {
    background: var(--p-hover);
}
.team-row.is-active {
    background: color-mix(in srgb, var(--p-primary-color), transparent 88%);
}
.team-row--create {
    grid-template-columns: 14px minmax(0, 1fr);
    margin-top: 8px;
    border: 1px dashed var(--p-divider);
    color: var(--p-text-muted);
}
.team-row--create:hover {
    border-color: color-mix(in srgb, var(--p-primary-color), transparent 55%);
    color: var(--p-text);
}
.team-dot,
.team-detail__dot {
    width: 14px;
    height: 14px;
    border-radius: 999px;
    box-shadow: 0 0 0 3px rgba(255, 255, 255, .04);
    flex-shrink: 0;
}
.team-row__plus {
    width: 14px;
    height: 14px;
    border-radius: 999px;
    border: 1px solid var(--p-divider);
    display: inline-flex;
    align-items: center;
    justify-content: center;
    font-size: 12px;
    font-weight: 800;
}
.team-row__main {
    min-width: 0;
}
.team-row__name {
    display: block;
    font-size: 13px;
    font-weight: 700;
}
.team-row__meta,
.muted {
    display: block;
    color: var(--p-text-muted);
    font-size: 11px;
}
.team-row__count {
    justify-self: start;
    min-width: 34px;
    border: 1px solid var(--p-divider);
    border-radius: 999px;
    padding: 1px 8px;
    background: var(--p-content-bg);
    color: var(--p-text);
    font-size: 12px;
    font-weight: 700;
    text-align: center;
}
.teams-empty,
.team-placeholder {
    color: var(--p-text-muted);
    font-size: 13px;
    text-align: center;
    padding: 28px 12px;
}
.team-detail {
    min-height: 430px;
    padding: 24px;
}
.team-overview {
    display: grid;
    gap: 20px;
}
.team-detail__head {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 16px;
    margin-bottom: 0;
}
.members-panel__head {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 16px;
    margin-bottom: 16px;
}
.team-detail__title {
    display: flex;
    align-items: center;
    gap: 10px;
}
.team-detail__actions {
    display: flex;
    align-items: center;
    gap: 6px;
}
.team-detail h3,
.members-panel h4 {
    margin: 0;
    color: var(--p-text);
    font-size: 16px;
    font-weight: 750;
}
.team-detail p,
.members-panel p {
    margin: 2px 0 0;
    color: var(--p-text-muted);
    font-size: 12px;
}
.team-stats {
    display: grid;
    grid-template-columns: 120px 120px minmax(0, 1fr);
    gap: 8px;
}
.team-stats > div {
    border: 1px solid var(--p-divider);
    border-radius: 8px;
    padding: 16px;
    background: var(--p-content-bg);
}
.team-stats__value,
.team-stats__label {
    display: block;
}
.team-stats__value {
    color: var(--p-text);
    font-size: 14px;
    font-weight: 750;
}
.team-stats__label {
    margin-top: 2px;
    color: var(--p-text-muted);
    font-size: 11px;
}
.team-blocked-message {
    margin: 0 0 12px;
}
.team-description {
    border: 1px solid var(--p-divider);
    border-radius: 8px;
    background: var(--p-content-bg);
    padding: 16px;
}
.team-description span {
    display: block;
    color: var(--p-text-muted);
    font-size: 11px;
    font-weight: 700;
    letter-spacing: .05em;
    text-transform: uppercase;
}
.team-description p {
    margin-top: 6px;
    color: var(--p-text);
}
.team-form-grid {
    display: grid;
    grid-template-columns: 1fr 1fr 140px;
    gap: 12px;
}
.team-form-grid__wide {
    grid-column: 1 / -1;
}
.team-dialog {
    display: grid;
    gap: 14px;
}
.color-field {
    display: flex;
    align-items: center;
    gap: 9px;
    width: 100%;
    min-height: 40px;
    border: 1px solid var(--p-input-border);
    border-radius: var(--p-form-field-border-radius);
    background: var(--p-input-bg);
    padding: 6px 10px;
}
.color-field:focus-within {
    border-color: var(--p-primary-color);
    box-shadow: var(--p-focus-ring);
}
.color-field :deep(.p-colorpicker-preview) {
    width: 28px;
    height: 28px;
    border: 1px solid var(--p-divider);
    border-radius: 7px;
    box-shadow: none;
}
.color-field__preview {
    width: 18px;
    height: 18px;
    border: 1px solid var(--p-divider);
    border-radius: 999px;
    flex-shrink: 0;
}
.color-field__value {
    color: var(--p-text-muted);
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
    font-size: 12px;
    line-height: 1;
}
.members-panel {
    padding-top: 24px;
    border-top: 1px solid var(--p-divider);
}
:deep(.p-inputtext),
:deep(.p-textarea) {
    width: 100%;
}
:deep(.p-textarea) {
    min-height: 96px;
    border: 1px solid var(--p-input-border);
    border-radius: var(--p-form-field-border-radius);
    background: var(--p-input-bg);
    color: var(--p-text);
    padding: var(--p-form-field-padding-y) var(--p-form-field-padding-x);
    font: inherit;
    font-size: var(--p-form-field-font-size);
    line-height: 1.5;
    resize: vertical;
}
:deep(.p-textarea:hover) {
    border-color: var(--p-inputtext-hover-border-color, var(--p-input-border));
}
:deep(.p-textarea:focus) {
    border-color: var(--p-primary-color);
    box-shadow: var(--p-focus-ring);
    outline: 0;
}
@media (max-width: 980px) {
    .teams-workbench,
    .team-stats,
    .team-form-grid {
        grid-template-columns: 1fr;
    }
    .team-detail__head,
    .members-panel__head {
        display: grid;
    }
}
</style>
