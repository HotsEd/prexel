import { useApi } from '@/composables/useApi'
import type { Member, PermissionDefinition, Role, Team, TeamDetail } from '@/types/api'

function asArray<T>(value: T[] | null | undefined): T[] {
    return Array.isArray(value) ? value : []
}

export const rbacService = {
    async permissions(): Promise<PermissionDefinition[]> {
        const res = await useApi().get<PermissionDefinition[]>('/permissions')
        return asArray(res.data)
    },

    async roles(): Promise<Role[]> {
        const res = await useApi().get<Role[]>('/roles')
        return asArray(res.data)
    },

    async createRole(payload: { name: string; description?: string; scope?: Role['scope']; permissions: string[]; is_admin?: boolean }): Promise<Role> {
        const res = await useApi().post<Role>('/roles', payload)
        return res.data
    },

    async updateRole(id: string, payload: { name?: string; description?: string; scope?: Role['scope']; permissions?: string[] }): Promise<Role> {
        const res = await useApi().patch<Role>(`/roles/${id}`, payload)
        return res.data
    },

    async deleteRole(id: string): Promise<void> {
        await useApi().delete(`/roles/${id}`)
    },

    async members(): Promise<Member[]> {
        const res = await useApi().get<Member[]>('/members')
        return asArray(res.data)
    },

    async createMember(payload: { email: string; name?: string; password: string; role_id?: string; team_ids: string[] }): Promise<Member> {
        const res = await useApi().post<Member>('/members', payload)
        return res.data
    },

    async updateMember(id: string, payload: { name?: string; role_id?: string; status?: Member['status']; team_ids?: string[] }): Promise<Member> {
        const res = await useApi().patch<Member>(`/members/${id}`, payload)
        return res.data
    },

    async deleteMember(id: string): Promise<void> {
        await useApi().delete(`/members/${id}`)
    },

    async teams(): Promise<Team[]> {
        const res = await useApi().get<Team[]>('/teams')
        return asArray(res.data)
    },

    async getTeam(id: string): Promise<TeamDetail> {
        const res = await useApi().get<TeamDetail>(`/teams/${id}`)
        return { ...res.data, members: asArray(res.data?.members) }
    },

    async createTeam(payload: { name: string; slug?: string; description?: string; color?: string; member_ids: string[] }): Promise<Team> {
        const res = await useApi().post<Team>('/teams', payload)
        return res.data
    },

    async updateTeam(id: string, payload: { name?: string; slug?: string; description?: string; color?: string }): Promise<Team> {
        const res = await useApi().patch<Team>(`/teams/${id}`, payload)
        return res.data
    },

    async deleteTeam(id: string): Promise<void> {
        await useApi().delete(`/teams/${id}`)
    },

    async setTeamMembers(id: string, members: Array<{ user_id: string; role_id: string }>): Promise<Team> {
        const res = await useApi().put<Team>(`/teams/${id}/members`, { members })
        return res.data
    },
}
