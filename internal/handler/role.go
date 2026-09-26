package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"wechatapp-web/internal/role"
	"wechatapp-web/internal/user"
)

// RoleHandler exposes the role-management endpoints (admin-managed).
type RoleHandler struct {
	roles role.Store
	users user.Store
}

// NewRoleHandler creates a role-management handler. users is used to block
// deleting roles that are still assigned to accounts.
func NewRoleHandler(roles role.Store, users user.Store) *RoleHandler {
	return &RoleHandler{roles: roles, users: users}
}

// CreateRoleRequest is the body of POST /roles (admin).
type CreateRoleRequest struct {
	Key         string `json:"key" binding:"required" example:"manager"` // 角色唯一标识（2-32位字母/数字/下划线/连字符）
	Name        string `json:"name" binding:"required" example:"经理"`     // 显示名称
	Description string `json:"description" example:"可以查看报表"`             // 描述（可选）
}

// UpdateRoleRequest is the body of PUT /roles/:id. The key is immutable.
type UpdateRoleRequest struct {
	Name        *string `json:"name" example:"经理"`            // 显示名称（可选）
	Description *string `json:"description" example:"可以查看报表"` // 描述（可选）
}

// RoleListResponse is the body of GET /roles.
type RoleListResponse struct {
	Total int         `json:"total"`
	Items []role.Role `json:"items"`
}

// List handles:
//
//	GET /api/v1/roles
//
//	@Summary      List all roles (admin)
//	@Description  Returns every role, built-in ones first. Admin only.
//	@Tags         roles
//	@Security     BearerAuth
//	@Produce      json
//	@Success      200 {object} RoleListResponse "Role list"
//	@Failure      401 {object} ErrorResponse "Not authenticated"
//	@Failure      403 {object} ErrorResponse "Not an admin"
//	@Router       /roles [get]
func (h *RoleHandler) List(c *gin.Context) {
	list, err := h.roles.List()
	if err != nil {
		writeError(c, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]role.Role, 0, len(list))
	for _, r := range list {
		items = append(items, *r)
	}
	c.JSON(http.StatusOK, RoleListResponse{Total: len(items), Items: items})
}

// Get handles:
//
//	GET /api/v1/roles/:id
//
//	@Summary      Get role detail (admin)
//	@Description  Returns one role by ID. Admin only.
//	@Tags         roles
//	@Security     BearerAuth
//	@Produce      json
//	@Param        id path int true "Role ID"
//	@Success      200 {object} role.Role "Role detail"
//	@Failure      401 {object} ErrorResponse "Not authenticated"
//	@Failure      403 {object} ErrorResponse "Not an admin"
//	@Failure      404 {object} ErrorResponse "Role not found"
//	@Router       /roles/{id} [get]
func (h *RoleHandler) Get(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	r, err := h.roles.GetByID(id)
	if err != nil {
		writeError(c, http.StatusNotFound, "角色不存在")
		return
	}
	c.JSON(http.StatusOK, r)
}

// Create handles:
//
//	POST /api/v1/roles
//
//	@Summary      Create a role (admin)
//	@Description  Adds a new role that can be assigned to users. Built-in keys
//	@Description  "admin" and "user" cannot be reused. Admin only.
//	@Tags         roles
//	@Security     BearerAuth
//	@Accept       json
//	@Produce      json
//	@Param        request body CreateRoleRequest true "New role"
//	@Success      201 {object} role.Role "Created role"
//	@Failure      400 {object} ErrorResponse "Invalid input or reserved key"
//	@Failure      401 {object} ErrorResponse "Not authenticated"
//	@Failure      403 {object} ErrorResponse "Not an admin"
//	@Failure      409 {object} ErrorResponse "Role key already exists"
//	@Router       /roles [post]
func (h *RoleHandler) Create(c *gin.Context) {
	var req CreateRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求体无效："+err.Error())
		return
	}
	key := strings.TrimSpace(req.Key)
	if err := role.ValidateKey(key); err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	if key == role.AdminKey || key == role.UserKey {
		writeError(c, http.StatusBadRequest, "内置角色标识 admin/user 不可重复创建")
		return
	}

	r := &role.Role{
		Key:         key,
		Name:        strings.TrimSpace(req.Name),
		Description: strings.TrimSpace(req.Description),
	}
	if r.Name == "" {
		writeError(c, http.StatusBadRequest, "角色名称不能为空")
		return
	}
	if err := h.roles.Create(r); err != nil {
		if errors.Is(err, role.ErrDuplicateKey) {
			writeError(c, http.StatusConflict, "角色标识已存在")
			return
		}
		writeError(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusCreated, r)
}

// Update handles:
//
//	PUT /api/v1/roles/:id
//
//	@Summary      Update a role (admin)
//	@Description  Changes the display name and/or description. The key is
//	@Description  immutable (it is the identity used by user records and
//	@Description  authorization). Admin only.
//	@Tags         roles
//	@Security     BearerAuth
//	@Accept       json
//	@Produce      json
//	@Param        id path int true "Role ID"
//	@Param        request body UpdateRoleRequest true "Fields to update"
//	@Success      200 {object} role.Role "Updated role"
//	@Failure      400 {object} ErrorResponse "Invalid input"
//	@Failure      401 {object} ErrorResponse "Not authenticated"
//	@Failure      403 {object} ErrorResponse "Not an admin"
//	@Failure      404 {object} ErrorResponse "Role not found"
//	@Router       /roles/{id} [put]
func (h *RoleHandler) Update(c *gin.Context) {
	var req UpdateRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求体无效："+err.Error())
		return
	}
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	r, err := h.roles.GetByID(id)
	if err != nil {
		writeError(c, http.StatusNotFound, "角色不存在")
		return
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			writeError(c, http.StatusBadRequest, "角色名称不能为空")
			return
		}
		r.Name = name
	}
	if req.Description != nil {
		r.Description = strings.TrimSpace(*req.Description)
	}
	if err := h.roles.Update(r); err != nil {
		writeError(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, r)
}

// Delete handles:
//
//	DELETE /api/v1/roles/:id
//
//	@Summary      Delete a role (admin)
//	@Description  Removes a custom role. Built-in roles (admin/user) cannot be
//	@Description  deleted, and a role still assigned to users cannot be deleted
//	@Description  until those users are moved to another role. Admin only.
//	@Tags         roles
//	@Security     BearerAuth
//	@Produce      json
//	@Param        id path int true "Role ID"
//	@Success      200 {object} map[string]any "message: 角色已删除"
//	@Failure      400 {object} ErrorResponse "Built-in role"
//	@Failure      401 {object} ErrorResponse "Not authenticated"
//	@Failure      403 {object} ErrorResponse "Not an admin"
//	@Failure      404 {object} ErrorResponse "Role not found"
//	@Failure      409 {object} ErrorResponse "Role still assigned to users"
//	@Router       /roles/{id} [delete]
func (h *RoleHandler) Delete(c *gin.Context) {
	id, ok := parseIDParam(c)
	if !ok {
		return
	}
	r, err := h.roles.GetByID(id)
	if err != nil {
		writeError(c, http.StatusNotFound, "角色不存在")
		return
	}
	if r.Builtin {
		writeError(c, http.StatusBadRequest, "内置角色不可删除")
		return
	}
	inUse, err := h.users.CountByRole(r.Key)
	if err != nil {
		writeError(c, http.StatusInternalServerError, err.Error())
		return
	}
	if inUse > 0 {
		writeError(c, http.StatusConflict, "仍有 "+strconv.Itoa(inUse)+" 个用户使用该角色，请先调整后再删除")
		return
	}
	if err := h.roles.Delete(r.ID); err != nil {
		if errors.Is(err, role.ErrBuiltin) {
			writeError(c, http.StatusBadRequest, "内置角色不可删除")
			return
		}
		writeError(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "角色已删除"})
}
