<template>
    <SCard :title="t('settings.roles.title')" :sub="t('settings.roles.sub')">
        <template #action>
            <Button :label="t('settings.roles.new')" icon="pi pi-plus" @click="openCreate" />
        </template>

        <div v-if="loading" class="roles-table">
            <div class="roles-table__head">
                <span>{{ t('settings.roles.columns.role') }}</span>
                <span>{{ t('settings.roles.columns.usage') }}</span>
                <span>{{ t('settings.roles.columns.members') }}</span>
                <span>{{ t('settings.roles.columns.permissions') }}</span>
                <span></span>
            </div>
            <div v-for="i in 5" :key="i" class="roles-table__row">
                <Skeleton height="18px" width="180px" />
                <Skeleton height="20px" width="96px" />
                <Skeleton height="18px" width="64px" />
                <Skeleton height="20px" width="260px" />
                <Skeleton height="18px" width="92px" />
            </div>
        </div>

        <div v-else class="roles-table">
            <div class="roles-table__head">
                <span>{{ t('settings.roles.columns.role') }}</span>
                <span>{{ t('settings.roles.columns.usage') }}</span>
                <span>{{ t('settings.roles.columns.members') }}</span>
                <span>{{ t('settings.roles.columns.permissions') }}</span>
                <span></span>
            </div>

            <div v-for="role in roles" :key="role.id" class="roles-table__row">
                <div class="role-cell">
                    <span class="role-cell__icon" :class="role.is_admin && 'role-cell__icon--admin'">
                            <IconShield :size="14" :stroke-width="2.2" />
                    </span>
                    <div class="role-cell__text">
                        <div class="role-cell__name">
                            <span>{{ role.name }}</span>
                            <span v-if="role.is_system" class="role-badge">{{ t('settings.roles.system') }}</span>
                        </div>
                        <div class="role-cell__desc">{{ role.description || t('settings.roles.noDescription') }}</div>
                    </div>
                </div>

                <div>
                    <span class="scope-pill">{{ scopeLabel(role.scope) }}</span>
                </div>

                <div class="roles-table__count">{{ roleMemberCount(role.id) }}</div>

                <div class="role-perms">
                    <span v-if="role.is_admin" class="role-perm role-perm--accent">{{ t('settings.permissions.all') }}</span>
                    <template v-else>
                        <span
                            v-for="group in permissionPills(role).fullGroups"
                            :key="`group-${role.id}-${group}`"
                            class="role-perm role-perm--accent"
                        >
                            {{ t('settings.permissions.allOfGroup', { group: groupLabel(group) }) }}
                        </span>
                        <span
                            v-for="slug in permissionPills(role).looseSlugs"
                            :key="`perm-${role.id}-${slug}`"
                            class="role-perm"
                            :title="permissionDescription(slug)"
                        >
                            {{ permissionLabel(slug) }}
                        </span>
                        <span v-if="!permissionPills(role).fullGroups.length && !permissionPills(role).looseSlugs.length" class="role-perm role-perm--muted">
                            {{ t('settings.permissions.none') }}
                        </span>
                    </template>
                </div>

                <div class="role-actions">
                    <button
                        type="button"
                        class="role-action"
                        @click="openEdit(role)"
                    >
                        {{ t('settings.roles.edit') }}
                    </button>
                    <button
                        v-if="!role.is_admin"
                        type="button"
                        class="role-action role-action--muted"
                        @click="duplicateRole(role)"
                    >
                        {{ t('settings.roles.duplicate') }}
                    </button>
                    <button
                        v-if="!role.is_admin"
                        type="button"
                        class="role-action role-action--danger"
                        @click="askDelete(role)"
                    >
                        {{ t('settings.roles.delete') }}
                    </button>
                </div>
            </div>
        </div>
    </SCard>

    <Dialog
        v-model:visible="dialogOpen"
        modal
        :header="editing ? t('settings.roles.editTitle') : t('settings.roles.createTitle')"
        :style="{ width: '960px', maxWidth: 'calc(100vw - 32px)' }"
    >
        <div class="dialog-body">
            <SField :label="t('settings.roles.fields.name')"><InputText v-model="draft.name" /></SField>
            <SField :label="t('settings.roles.fields.description')"><InputText v-model="draft.description" /></SField>
            <label class="admin-toggle">
                <Checkbox v-model="draft.is_admin" binary :disabled="!!editing" />
                <span>{{ t('settings.roles.adminHint') }}</span>
            </label>
            <div v-if="!draft.is_admin" class="permission-groups">
                <section v-for="group in groupedPermissions" :key="group.name" class="permission-group">
                    <h3>{{ groupLabel(group.name) }}</h3>
                    <label v-for="perm in group.items" :key="perm.slug" class="permission-row">
                        <Checkbox v-model="draft.permissions" :value="perm.slug" />
                        <span>
                            <strong>{{ permissionName(perm) }}</strong>
                            <small>{{ permissionDescriptionFor(perm) }}</small>
                        </span>
                    </label>
                </section>
            </div>
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
import Checkbox from 'primevue/checkbox'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import Skeleton from 'primevue/skeleton'
import { useConfirm } from 'primevue/useconfirm'
import IconShield from '@/components/icons/IconShield.vue'
import SCard from '@/components/settings/SCard.vue'
import SField from '@/components/settings/SField.vue'
import { apiErrorMessage } from '@/composables/useApi'
import { notify } from '@/lib/notify'
import { rbacService } from '@/services/rbac'
import type { Member, PermissionDefinition, Role } from '@/types/api'

const confirm = useConfirm()
const { t, te } = useI18n()
const roles = ref<Role[]>([])
const permissions = ref<PermissionDefinition[]>([])
const members = ref<Member[]>([])
const loading = ref(false)
const saving = ref(false)
const error = ref<string | null>(null)
const dialogOpen = ref(false)
const editing = ref<Role | null>(null)
const draft = ref({ name: '', description: '', scope: 'both' as Role['scope'], permissions: [] as string[], is_admin: false })

const groupedPermissions = computed(() => {
    const map = new Map<string, PermissionDefinition[]>()
    for (const p of permissions.value) {
        if (!map.has(p.group)) map.set(p.group, [])
        map.get(p.group)!.push(p)
    }
    return Array.from(map, ([name, items]) => ({ name, items }))
})

const permissionMap = computed(() => {
    const map = new Map<string, PermissionDefinition>()
    for (const p of permissions.value) map.set(p.slug, p)
    return map
})

const catalogByGroup = computed<Record<string, string[]>>(() => {
    const map: Record<string, string[]> = {}
    for (const p of permissions.value) {
        if (!map[p.group]) map[p.group] = []
        map[p.group].push(p.slug)
    }
    return map
})

onMounted(load)

async function load() {
    loading.value = true
    try {
        const [r, p, m] = await Promise.all([rbacService.roles(), rbacService.permissions(), rbacService.members()])
        roles.value = r
        permissions.value = p
        members.value = m
    } catch (e) {
        notify.error(apiErrorMessage(e))
    } finally {
        loading.value = false
    }
}

function openCreate() {
    editing.value = null
    error.value = null
    draft.value = { name: '', description: '', scope: 'both', permissions: [], is_admin: false }
    dialogOpen.value = true
}

function openEdit(role: Role) {
    editing.value = role
    error.value = null
    draft.value = { name: role.name, description: role.description, scope: role.scope ?? 'both', permissions: [...role.permissions], is_admin: role.is_admin }
    dialogOpen.value = true
}

function duplicateRole(role: Role) {
    editing.value = null
    error.value = null
    draft.value = {
        name: `${role.name} copy`,
        description: role.description,
        scope: role.scope ?? 'both',
        permissions: role.is_admin ? permissions.value.map((p) => p.slug) : [...role.permissions],
        is_admin: false,
    }
    dialogOpen.value = true
}

async function save() {
    saving.value = true
    error.value = null
    try {
        if (editing.value) {
            await rbacService.updateRole(editing.value.id, {
                name: draft.value.name,
                description: draft.value.description,
                scope: draft.value.scope,
                permissions: draft.value.permissions,
            })
            notify.success(t('settings.roles.updated'))
        } else {
            await rbacService.createRole(draft.value)
            notify.success(t('settings.roles.created'))
        }
        dialogOpen.value = false
        await load()
    } catch (e) {
        error.value = apiErrorMessage(e)
    } finally {
        saving.value = false
    }
}

function askDelete(role: Role) {
    confirm.require({
        header: t('settings.roles.deleteTitle'),
        message: t('settings.roles.deleteMessage', { name: role.name }),
        icon: 'pi pi-exclamation-triangle',
        rejectLabel: t('common.cancel'),
        acceptLabel: t('settings.roles.delete'),
        acceptClass: 'p-button-danger',
        accept: () => { void deleteRole(role.id) },
    })
}

function roleMemberCount(roleID: string): number {
    return members.value.filter((member) => member.role?.id === roleID || member.team_role?.id === roleID || member.teams?.some((team) => (team as any).role?.id === roleID)).length
}

function scopeLabel(scope: Role['scope']): string {
    if (scope === 'global') return t('settings.roles.scopes.global')
    if (scope === 'team') return t('settings.roles.scopes.team')
    return t('settings.roles.scopes.both')
}

async function deleteRole(id: string) {
    try {
        await rbacService.deleteRole(id)
        notify.success(t('settings.roles.removed'))
        await load()
    } catch (e) {
        notify.error(apiErrorMessage(e))
    }
}

function permissionLabel(slug: string): string {
    const perm = permissionMap.value.get(slug)
    if (!perm) return slug
    return permissionName(perm)
}

function permissionDescription(slug: string): string {
    const perm = permissionMap.value.get(slug)
    if (!perm) return ''
    return permissionDescriptionFor(perm)
}

function groupLabel(key: string): string {
    const path = `settings.permissions.groups.${i18nKey(key)}`
    return te(path) ? t(path) : key.replace(/[._-]/g, ' ')
}

function permissionName(perm: PermissionDefinition): string {
    const path = `settings.permissions.items.${i18nKey(perm.slug)}.name`
    return te(path) ? t(path) : perm.name
}

function permissionDescriptionFor(perm: PermissionDefinition): string {
    const path = `settings.permissions.items.${i18nKey(perm.slug)}.desc`
    return te(path) ? t(path) : perm.description
}

function i18nKey(value: string): string {
    return value.replace(/[^a-zA-Z0-9]/g, '_')
}

function permissionPills(role: Role): { fullGroups: string[]; looseSlugs: string[] } {
    const owned = new Set(role.permissions)
    const fullGroups: string[] = []
    const absorbed = new Set<string>()
    for (const [group, slugs] of Object.entries(catalogByGroup.value)) {
        if (slugs.length < 2) continue
        if (slugs.every((slug) => owned.has(slug))) {
            fullGroups.push(group)
            for (const slug of slugs) absorbed.add(slug)
        }
    }
    return {
        fullGroups,
        looseSlugs: role.permissions.filter((slug) => !absorbed.has(slug)),
    }
}
</script>

<style scoped>
.roles-table {
    border: 1px solid var(--p-divider);
    border-radius: 8px;
    overflow: hidden;
    background: var(--p-content-bg);
}
.roles-table__head,
.roles-table__row {
    display: grid;
    grid-template-columns: minmax(260px, 1.1fr) 140px 96px minmax(320px, 1.4fr) 170px;
    align-items: center;
    gap: 14px;
}
.roles-table__head {
    padding: 10px 14px;
    border-bottom: 1px solid var(--p-divider);
    background: var(--p-datatable-header-cell-background);
    color: var(--p-text-muted);
    font-size: 11px;
    font-weight: 750;
    letter-spacing: .05em;
    text-transform: uppercase;
}
.roles-table__row {
    min-height: 72px;
    padding: 12px 14px;
    border-bottom: 1px solid var(--p-divider);
}
.roles-table__row:last-child {
    border-bottom: 0;
}
.roles-table__row:hover {
    background: var(--p-hover);
}
.roles-table__count {
    color: var(--p-text);
    font-size: 13px;
    font-weight: 700;
}
.role-cell {
    display: flex;
    align-items: center;
    gap: 10px;
    min-width: 0;
}
.role-cell__icon {
    width: 30px;
    height: 30px;
    border: 1px solid var(--p-divider);
    border-radius: 7px;
    background: var(--p-bg);
    color: var(--p-text-subtle);
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
}
.role-cell__icon--admin {
    background: var(--p-primary-color);
    border-color: var(--p-primary-color);
    color: var(--p-primary-contrast);
}
.role-cell__text {
    min-width: 0;
}
.role-cell__name {
    display: flex;
    align-items: center;
    gap: 8px;
    color: var(--p-text);
    font-size: 13px;
    font-weight: 750;
}
.role-cell__desc {
    margin-top: 2px;
    color: var(--p-text-muted);
    font-size: 12px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}
.role-badge,
.scope-pill {
    display: inline-flex;
    align-items: center;
    border-radius: 999px;
    padding: 2px 8px;
    font-size: 10.5px;
    font-weight: 750;
    white-space: nowrap;
}
.role-badge {
    background: rgba(100, 116, 139, 0.14);
    color: #94a3b8;
    letter-spacing: .05em;
    text-transform: uppercase;
}
.scope-pill {
    border: 1px solid color-mix(in srgb, var(--p-primary-color), transparent 72%);
    background: color-mix(in srgb, var(--p-primary-color), transparent 90%);
    color: var(--p-primary-color);
}
.role-perms {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 4px;
    max-height: 52px;
    overflow: hidden;
}
.role-perm {
    padding: 2px 7px;
    border: 1px solid var(--p-divider);
    border-radius: 5px;
    background: var(--p-bg);
    color: var(--p-text-subtle);
    font-size: 10.5px;
    line-height: 1.35;
}
.role-perm--accent {
    background: color-mix(in srgb, var(--p-primary-color), transparent 88%);
    border-color: color-mix(in srgb, var(--p-primary-color), transparent 72%);
    color: var(--p-primary-color);
    font-weight: 650;
}
.role-perm--muted {
    background: transparent;
    color: var(--p-text-muted);
    font-style: italic;
}
.role-actions {
    display: flex;
    justify-content: flex-end;
    gap: 10px;
}
.role-action {
    border: 0;
    background: transparent;
    color: var(--p-primary-color);
    cursor: pointer;
    font: inherit;
    font-size: 12px;
    font-weight: 650;
    padding: 0;
}
.role-action--muted {
    color: var(--p-text-muted);
}
.role-action--danger {
    color: #dc2626;
}
.dialog-body {
    display: flex;
    flex-direction: column;
    gap: 14px;
    padding-top: 8px;
}
.admin-toggle {
    display: flex;
    align-items: center;
    gap: 10px;
    color: var(--p-text);
    font-size: 13px;
}
.permission-groups {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px;
    max-height: 420px;
    overflow: auto;
}
.permission-group {
    border: 1px solid var(--p-divider);
    border-radius: 8px;
    padding: 12px;
    background: var(--p-bg);
}
.permission-group h3 {
    margin: 0 0 8px;
    color: var(--p-text);
    font-size: 13px;
    text-transform: capitalize;
}
.permission-row {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    padding: 7px 0;
}
.permission-row + .permission-row {
    border-top: 1px dashed var(--p-divider);
}
.permission-row strong {
    display: block;
    color: var(--p-text);
    font-size: 12px;
}
.permission-row small {
    display: block;
    color: var(--p-text-muted);
    font-size: 11px;
    line-height: 1.35;
}
:deep(.p-inputtext) {
    width: 100%;
}
@media (max-width: 900px) {
    .roles-table__head {
        display: none;
    }
    .roles-table__row,
    .permission-groups {
        grid-template-columns: 1fr;
    }
    .role-actions {
        justify-content: flex-start;
    }
}
</style>
