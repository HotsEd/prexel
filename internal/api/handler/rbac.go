package handler

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/prexel/prexel/internal/api/apimiddleware"
	"github.com/prexel/prexel/internal/rbac"
)

type RBACHandler struct {
	svc     *rbac.Service
	dataDir string
}

func NewRBACHandler(svc *rbac.Service, dataDir string) *RBACHandler {
	return &RBACHandler{svc: svc, dataDir: dataDir}
}

func (h *RBACHandler) Permissions(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, rbac.PermissionDefinitions())
}

func (h *RBACHandler) ListRoles(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, rbac.PermRolesView) {
		return
	}
	out, err := h.svc.ListRoles(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type roleReq struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Scope       string   `json:"scope"`
	Permissions []string `json:"permissions"`
	IsAdmin     bool     `json:"is_admin"`
}

func (h *RBACHandler) CreateRole(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, rbac.PermRolesManage) {
		return
	}
	var req roleReq
	if !decodeJSON(w, r, &req) {
		return
	}
	out, err := h.svc.CreateRole(r.Context(), rbac.CreateRoleInput{
		Name:        req.Name,
		Description: req.Description,
		Scope:       req.Scope,
		Permissions: req.Permissions,
		IsAdmin:     req.IsAdmin,
	})
	if err != nil {
		writeRBACError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *RBACHandler) UpdateRole(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, rbac.PermRolesManage) {
		return
	}
	var req struct {
		Name        *string  `json:"name"`
		Description *string  `json:"description"`
		Scope       *string  `json:"scope"`
		Permissions []string `json:"permissions"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	out, err := h.svc.UpdateRole(r.Context(), chi.URLParam(r, "id"), rbac.UpdateRoleInput{
		Name:           req.Name,
		Description:    req.Description,
		Scope:          req.Scope,
		Permissions:    req.Permissions,
		PermissionsSet: req.Permissions != nil,
	})
	if err != nil {
		writeRBACError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *RBACHandler) DeleteRole(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, rbac.PermRolesManage) {
		return
	}
	if err := h.svc.DeleteRole(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeRBACError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *RBACHandler) ListMembers(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, rbac.PermMembersView) {
		return
	}
	out, err := h.svc.ListMembers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type memberReq struct {
	Email    string                 `json:"email"`
	Name     string                 `json:"name"`
	Password string                 `json:"password"`
	RoleID   string                 `json:"role_id"`
	TeamIDs  []string               `json:"team_ids"`
	Teams    []rbac.TeamMemberInput `json:"teams"`
}

func (h *RBACHandler) CreateMember(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, rbac.PermMembersInvite) {
		return
	}
	var req memberReq
	if !decodeJSON(w, r, &req) {
		return
	}
	out, err := h.svc.CreateMember(r.Context(), rbac.CreateMemberInput{
		Email:    req.Email,
		Name:     req.Name,
		Password: req.Password,
		RoleID:   req.RoleID,
		TeamIDs:  req.TeamIDs,
		Teams:    req.Teams,
	})
	if err != nil {
		writeRBACError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *RBACHandler) UpdateMember(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, rbac.PermMembersManage) {
		return
	}
	var req struct {
		Name    *string                `json:"name"`
		RoleID  *string                `json:"role_id"`
		Status  *string                `json:"status"`
		TeamIDs []string               `json:"team_ids"`
		Teams   []rbac.TeamMemberInput `json:"teams"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	out, err := h.svc.UpdateMember(r.Context(), chi.URLParam(r, "id"), rbac.UpdateMemberInput{
		Name:     req.Name,
		RoleID:   req.RoleID,
		Status:   req.Status,
		TeamIDs:  req.TeamIDs,
		Teams:    req.Teams,
		TeamsSet: req.TeamIDs != nil || req.Teams != nil,
	})
	if err != nil {
		writeRBACError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *RBACHandler) DeleteMember(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, rbac.PermMembersManage) {
		return
	}
	uid, _ := apimiddleware.UserIDFromContext(r.Context())
	if err := h.svc.DeleteMember(r.Context(), chi.URLParam(r, "id"), uid); err != nil {
		writeRBACError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *RBACHandler) ListTeams(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, rbac.PermTeamsView) {
		return
	}
	out, err := h.svc.ListTeams(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type teamReq struct {
	Name        string                 `json:"name"`
	Slug        string                 `json:"slug"`
	Description string                 `json:"description"`
	Color       string                 `json:"color"`
	MemberIDs   []string               `json:"member_ids"`
	Members     []rbac.TeamMemberInput `json:"members"`
}

func (h *RBACHandler) CreateTeam(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, rbac.PermTeamsManage) {
		return
	}
	var req teamReq
	if !decodeJSON(w, r, &req) {
		return
	}
	out, err := h.svc.CreateTeam(r.Context(), rbac.CreateTeamInput{
		Name:        req.Name,
		Slug:        req.Slug,
		Description: req.Description,
		Color:       req.Color,
		MemberIDs:   req.MemberIDs,
		Members:     req.Members,
	})
	if err != nil {
		writeRBACError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *RBACHandler) GetTeam(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "id")
	if !h.authorizeTeam(w, r, teamID, rbac.PermTeamsView) {
		return
	}
	out, err := h.svc.GetTeam(r.Context(), teamID)
	if err != nil {
		writeRBACError(w, err)
		return
	}
	members, err := h.svc.MembersForTeam(r.Context(), out.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"team": out, "members": members})
}

func (h *RBACHandler) UpdateTeam(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "id")
	if !h.authorizeTeam(w, r, teamID, rbac.PermTeamsManage) {
		return
	}
	var req struct {
		Name        *string `json:"name"`
		Slug        *string `json:"slug"`
		Description *string `json:"description"`
		Color       *string `json:"color"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	out, err := h.svc.UpdateTeam(r.Context(), teamID, rbac.UpdateTeamInput{
		Name:        req.Name,
		Slug:        req.Slug,
		Description: req.Description,
		Color:       req.Color,
	})
	if err != nil {
		writeRBACError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *RBACHandler) DeleteTeam(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r, rbac.PermTeamsManage) {
		return
	}
	if err := h.svc.DeleteTeam(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeRBACError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *RBACHandler) SetTeamMembers(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "id")
	if !h.authorizeTeam(w, r, teamID, rbac.PermTeamsMembersManage) {
		return
	}
	var req struct {
		MemberIDs []string               `json:"member_ids"`
		Members   []rbac.TeamMemberInput `json:"members"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	members := req.Members
	if len(members) == 0 && len(req.MemberIDs) > 0 {
		members = make([]rbac.TeamMemberInput, 0, len(req.MemberIDs))
		for _, id := range req.MemberIDs {
			members = append(members, rbac.TeamMemberInput{UserID: id})
		}
	}
	out, err := h.svc.SetTeamMembers(r.Context(), teamID, members)
	if err != nil {
		writeRBACError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *RBACHandler) UploadAvatar(w http.ResponseWriter, r *http.Request) {
	uid, ok := apimiddleware.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := r.ParseMultipartForm(2 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_upload")
		return
	}
	file, header, err := r.FormFile("avatar")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing_avatar")
		return
	}
	defer func() { _ = file.Close() }()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".webp":
	default:
		writeError(w, http.StatusBadRequest, "unsupported_avatar_type")
		return
	}
	dir := filepath.Join(h.dataDir, "uploads", "avatars")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "upload_error")
		return
	}
	name := uid + "-" + uuid.NewString() + ext
	dstPath := filepath.Join(dir, name)
	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "upload_error")
		return
	}
	if _, err := io.Copy(dst, http.MaxBytesReader(w, file, 2<<20)); err != nil {
		_ = dst.Close()
		writeError(w, http.StatusBadRequest, "avatar_too_large")
		return
	}
	if err := dst.Close(); err != nil {
		writeError(w, http.StatusInternalServerError, "upload_error")
		return
	}
	if err := h.svc.SetUserAvatar(r.Context(), uid, name); err != nil {
		writeRBACError(w, err)
		return
	}
	user, err := h.svc.UserContext(r.Context(), uid)
	if err != nil {
		writeRBACError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (h *RBACHandler) DeleteAvatar(w http.ResponseWriter, r *http.Request) {
	uid, ok := apimiddleware.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	path, _ := h.svc.AvatarPath(r.Context(), uid)
	if path != "" {
		_ = os.Remove(filepath.Join(h.dataDir, "uploads", "avatars", filepath.Base(path)))
	}
	if err := h.svc.SetUserAvatar(r.Context(), uid, ""); err != nil {
		writeRBACError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *RBACHandler) ServeAvatar(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(chi.URLParam(r, "name"))
	if name == "." || name == "/" || name == "" {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	path := filepath.Join(h.dataDir, "uploads", "avatars", name)
	ext := filepath.Ext(path)
	if typ := mime.TypeByExtension(ext); typ != "" {
		w.Header().Set("Content-Type", typ)
	}
	http.ServeFile(w, r, path)
}

func (h *RBACHandler) authorize(w http.ResponseWriter, r *http.Request, perm string) bool {
	uid, ok := apimiddleware.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	if err := h.svc.Require(r.Context(), uid, perm); err != nil {
		writeRBACError(w, err)
		return false
	}
	return true
}

func (h *RBACHandler) authorizeTeam(w http.ResponseWriter, r *http.Request, teamID string, perm string) bool {
	uid, ok := apimiddleware.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	allowed, err := h.svc.CanAccessTeam(r.Context(), uid, &teamID, perm)
	if err != nil {
		writeRBACError(w, err)
		return false
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "forbidden")
		return false
	}
	return true
}

// decodeJSON lives in http_helpers.go (delegates to internal/api/httpx).

func writeRBACError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, rbac.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, rbac.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, rbac.ErrInvalidInput):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "message": err.Error()})
	case errors.Is(err, rbac.ErrSystemRole):
		writeError(w, http.StatusBadRequest, "system_role")
	case errors.Is(err, rbac.ErrRoleInUse):
		writeError(w, http.StatusConflict, "role_in_use")
	case errors.Is(err, rbac.ErrTeamHasApps):
		writeError(w, http.StatusConflict, "team_has_apps")
	case errors.Is(err, rbac.ErrSelfDelete):
		writeError(w, http.StatusBadRequest, "cannot_delete_self")
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error", "message": err.Error()})
	}
}
