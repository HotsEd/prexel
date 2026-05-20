package rbac

// Permission slugs are a static source of truth. Roles store selected slugs
// as JSON so the catalog can grow without schema churn.
const (
	PermAppsView          = "apps.view"
	PermAppsCreate        = "apps.create"
	PermAppsUpdate        = "apps.update"
	PermAppsDelete        = "apps.delete"
	PermAppsDeploy        = "apps.deploy"
	PermAppsRollback      = "apps.rollback"
	PermAppsLogs          = "apps.logs"
	PermAppsSecretsView   = "apps.secrets.view"
	PermAppsSecretsManage = "apps.secrets.manage"

	PermDomainsView   = "domains.view"
	PermDomainsManage = "domains.manage"

	PermServersView   = "servers.view"
	PermServersManage = "servers.manage"

	PermGitView   = "git.view"
	PermGitManage = "git.manage"

	PermTeamsView          = "teams.view"
	PermTeamsManage        = "teams.manage"
	PermTeamsMembersManage = "teams.members.manage"

	PermMembersView   = "members.view"
	PermMembersInvite = "members.invite"
	PermMembersManage = "members.manage"

	PermRolesView   = "roles.view"
	PermRolesManage = "roles.manage"

	PermSettingsInstanceManage = "settings.instance.manage"
)

const (
	RoleScopeGlobal = "global"
	RoleScopeTeam   = "team"
	RoleScopeBoth   = "both"
)

type PermissionDefinition struct {
	Slug        string `json:"slug"`
	Group       string `json:"group"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

func PermissionDefinitions() []PermissionDefinition {
	return []PermissionDefinition{
		{PermAppsView, "apps", "Ver apps", "Listar e abrir apps dentro do escopo permitido."},
		{PermAppsCreate, "apps", "Criar apps", "Criar novos apps globais ou em times permitidos."},
		{PermAppsUpdate, "apps", "Editar apps", "Alterar configurações de build, runtime e health-check."},
		{PermAppsDelete, "apps", "Excluir apps", "Remover apps e recursos associados."},
		{PermAppsDeploy, "apps", "Deploy", "Iniciar deploys e restart de apps."},
		{PermAppsRollback, "apps", "Rollback", "Promover versões anteriores."},
		{PermAppsLogs, "apps", "Logs", "Ler logs de deploy e runtime."},
		{PermAppsSecretsView, "apps", "Ver secrets", "Listar metadados de secrets sem ver valores."},
		{PermAppsSecretsManage, "apps", "Gerenciar secrets", "Criar, atualizar e remover secrets."},
		{PermDomainsView, "domains", "Ver domínios", "Listar domínios e status DNS/TLS."},
		{PermDomainsManage, "domains", "Gerenciar domínios", "Criar, verificar e remover domínios."},
		{PermServersView, "servers", "Ver servidores", "Listar servidores Docker."},
		{PermServersManage, "servers", "Gerenciar servidores", "Criar, testar, editar e remover servidores."},
		{PermGitView, "git", "Ver integrações Git", "Listar integrações e repositórios Git."},
		{PermGitManage, "git", "Gerenciar Git", "Criar e remover GitHub Apps, tokens e chaves SSH."},
		{PermTeamsView, "teams", "Ver times", "Listar times e composição."},
		{PermTeamsManage, "teams", "Gerenciar times", "Criar, editar e remover times."},
		{PermTeamsMembersManage, "teams", "Gerenciar membros de times", "Adicionar e remover pessoas dos times."},
		{PermMembersView, "members", "Ver membros", "Listar membros da instância."},
		{PermMembersInvite, "members", "Convidar membros", "Criar novos acessos para a instância."},
		{PermMembersManage, "members", "Gerenciar membros", "Alterar papel, status e times de membros."},
		{PermRolesView, "roles", "Ver permissões", "Listar papéis e catálogo de permissões."},
		{PermRolesManage, "roles", "Gerenciar permissões", "Criar e editar papéis."},
		{PermSettingsInstanceManage, "settings", "Gerenciar instância", "Alterar configurações globais da instalação."},
	}
}

func AllPermissions() []string {
	defs := PermissionDefinitions()
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		out = append(out, d.Slug)
	}
	return out
}

func permissionSet() map[string]struct{} {
	out := map[string]struct{}{}
	for _, slug := range AllPermissions() {
		out[slug] = struct{}{}
	}
	return out
}

type defaultRole struct {
	Slug        string
	Name        string
	Description string
	Scope       string
	IsAdmin     bool
	Permissions []string
}

func defaultRoles() []defaultRole {
	devPerms := []string{
		PermAppsView, PermAppsCreate, PermAppsUpdate, PermAppsDeploy, PermAppsRollback, PermAppsLogs,
		PermAppsSecretsView,
		PermDomainsView,
		PermGitView,
	}
	return []defaultRole{
		{
			Slug:        "admin",
			Name:        "Admin",
			Description: "Acesso total à instância.",
			Scope:       RoleScopeGlobal,
			IsAdmin:     true,
			Permissions: []string{},
		},
		{
			Slug:        "platform",
			Name:        "Platform",
			Description: "Administra integrações, servidores, domínios e políticas globais sem acesso automático a times.",
			Scope:       RoleScopeGlobal,
			Permissions: []string{
				PermAppsView, PermDomainsView, PermDomainsManage, PermServersView, PermServersManage,
				PermGitView, PermGitManage, PermTeamsView, PermTeamsManage, PermTeamsMembersManage,
				PermMembersView, PermMembersInvite, PermMembersManage, PermRolesView, PermRolesManage,
				PermSettingsInstanceManage,
			},
		},
		{
			Slug:        "tech-lead",
			Name:        "Tech Lead",
			Description: "Gerencia apps, deploys, secrets e membros dentro dos times em que lidera.",
			Scope:       RoleScopeTeam,
			Permissions: []string{
				PermAppsView, PermAppsCreate, PermAppsUpdate, PermAppsDelete, PermAppsDeploy, PermAppsRollback, PermAppsLogs,
				PermAppsSecretsView, PermAppsSecretsManage, PermDomainsView, PermDomainsManage,
				PermTeamsView, PermTeamsManage, PermTeamsMembersManage, PermGitView,
			},
		},
		{
			Slug:        "developer",
			Name:        "Developer",
			Description: "Gerencia apps e deploys dentro dos times em que participa.",
			Scope:       RoleScopeTeam,
			Permissions: devPerms,
		},
		{
			Slug:        "qa",
			Name:        "QA",
			Description: "Acompanha deploys, logs e estado dos apps do time sem alterar infraestrutura.",
			Scope:       RoleScopeTeam,
			Permissions: []string{
				PermAppsView, PermAppsDeploy, PermAppsRollback, PermAppsLogs, PermDomainsView, PermTeamsView,
			},
		},
		{
			Slug:        "viewer",
			Name:        "Viewer",
			Description: "Acesso somente leitura aos recursos permitidos.",
			Scope:       RoleScopeBoth,
			Permissions: []string{
				PermAppsView, PermAppsLogs, PermDomainsView, PermServersView, PermGitView, PermTeamsView, PermMembersView, PermRolesView,
			},
		},
	}
}
