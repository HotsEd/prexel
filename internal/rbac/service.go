package rbac

import (
	"database/sql"
	"errors"
	"regexp"
)

var (
	ErrNotFound     = errors.New("rbac: not found")
	ErrForbidden    = errors.New("rbac: forbidden")
	ErrInvalidInput = errors.New("rbac: invalid input")
	ErrSystemRole   = errors.New("rbac: system role")
	ErrRoleInUse    = errors.New("rbac: role in use")
	ErrTeamHasApps  = errors.New("rbac: team has apps")
	ErrSelfDelete   = errors.New("rbac: cannot delete current user")
	slugRegex       = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)
	colorRegex      = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	statusAllowed   = map[string]struct{}{"active": {}, "inactive": {}, "blocked": {}}
)

type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service { return &Service{db: db} }

type Role struct {
	ID          string   `json:"id"`
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Scope       string   `json:"scope"`
	IsAdmin     bool     `json:"is_admin"`
	IsSystem    bool     `json:"is_system"`
	Permissions []string `json:"permissions"`
	CreatedAt   int64    `json:"created_at"`
	UpdatedAt   int64    `json:"updated_at"`
}

type Team struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	Color       string `json:"color"`
	AvatarPath  string `json:"-"`
	AvatarURL   string `json:"avatar_url,omitempty"`
	MemberCount int    `json:"member_count"`
	AppCount    int    `json:"app_count"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

type Member struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url,omitempty"`
	Status    string `json:"status"`
	Role      *Role  `json:"role,omitempty"`
	TeamRole  *Role  `json:"team_role,omitempty"`
	Teams     []Team `json:"teams"`
	CreatedAt int64  `json:"created_at"`
}

type UserContext struct {
	ID          string   `json:"id"`
	Email       string   `json:"email"`
	Name        string   `json:"name,omitempty"`
	AvatarURL   string   `json:"avatar_url,omitempty"`
	Status      string   `json:"status"`
	Role        *Role    `json:"role_detail,omitempty"`
	RoleSlug    string   `json:"role,omitempty"`
	Permissions []string `json:"permissions"`
	IsAdmin     bool     `json:"is_admin"`
}

type CreateRoleInput struct {
	Name        string
	Description string
	Scope       string
	Permissions []string
	IsAdmin     bool
}

type UpdateRoleInput struct {
	Name           *string
	Description    *string
	Scope          *string
	Permissions    []string
	PermissionsSet bool
}

type CreateMemberInput struct {
	Email    string
	Name     string
	Password string
	RoleID   string
	TeamIDs  []string
	Teams    []TeamMemberInput
}

type UpdateMemberInput struct {
	Name     *string
	RoleID   *string
	Status   *string
	TeamIDs  []string
	Teams    []TeamMemberInput
	TeamsSet bool
}

type CreateTeamInput struct {
	Name        string
	Slug        string
	Description string
	Color       string
	MemberIDs   []string
	Members     []TeamMemberInput
}

type UpdateTeamInput struct {
	Name        *string
	Slug        *string
	Description *string
	Color       *string
}

type TeamMemberInput struct {
	TeamID string `json:"team_id"`
	UserID string `json:"user_id"`
	RoleID string `json:"role_id"`
}
