package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"wechatapp-web/internal/auth"
	"wechatapp-web/internal/user"
)

// UserHandler exposes the user-management endpoints (admin-managed).
type UserHandler struct {
	users user.Store
}

// NewUserHandler creates a user-management handler.
func NewUserHandler(users user.Store) *UserHandler {
	return &UserHandler{users: users}
}

// CreateUserRequest is the body of POST /users (admin).
type CreateUserRequest struct {
	Username string `json:"username" binding:"required" example:"lisi"`      // 用户名
	Email    string `json:"email" example:"lisi@example.com"`                // 邮箱（推荐填写，用于两步验证重置）
	Password string `json:"password" binding:"required" example:"secret123"` // 密码（至少6位）
	Nickname string `json:"nickname" example:"李四"`                           // 昵称
	Role     string `json:"role" example:"user" enums:"user,admin"`          // 角色：user | admin（默认 user）
}

// UpdateUserRequest is the body of PUT /users/:id.
type UpdateUserRequest struct {
	Email    *string `json:"email" example:"zhangsan@example.com"`   // 邮箱（可选，本人或管理员可改）
	Nickname *string `json:"nickname" example:"张三"`                  // 昵称（可选）
	Role     *string `json:"role" enums:"user,admin" example:"user"` // 角色（仅管理员可改）
}

// UserListResponse is the body of GET /users.
type UserListResponse struct {
	Total int             `json:"total"`
	Items []user.SafeUser `json:"items"`
}

// List handles:
//
//	GET /api/v1/users
//
//	@Summary      List all users (admin)
//	@Description  Returns all accounts. Admin only.
//	@Tags         users
//	@Security     BearerAuth
//	@Produce      json
//	@Success      200 {object} UserListResponse "User list"
//	@Failure      401 {object} ErrorResponse "Not authenticated"
//	@Failure      403 {object} ErrorResponse "Not an admin"
//	@Router       /users [get]
func (h *UserHandler) List(c *gin.Context) {
	list, err := h.users.List()
	if err != nil {
		writeError(c, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]user.SafeUser, 0, len(list))
	for _, u := range list {
		items = append(items, u.ToSafe())
	}
	c.JSON(http.StatusOK, UserListResponse{Total: len(items), Items: items})
}

// Get handles:
//
//	GET /api/v1/users/:id
//
//	@Summary      Get user detail
//	@Description  Returns one account. Admin, or the user themselves.
//	@Tags         users
//	@Security     BearerAuth
//	@Produce      json
//	@Param        id path string true "User ID"
//	@Success      200 {object} user.SafeUser "User detail"
//	@Failure      401 {object} ErrorResponse "Not authenticated"
//	@Failure      403 {object} ErrorResponse "Not allowed to view this user"
//	@Failure      404 {object} ErrorResponse "User not found"
//	@Router       /users/{id} [get]
func (h *UserHandler) Get(c *gin.Context) {
	id := c.Param("id")
	claims, _ := auth.ClaimsFromContext(c)
	if !canManage(claims, id) {
		writeError(c, http.StatusForbidden, "无权查看该用户")
		return
	}
	u, err := h.users.GetByID(id)
	if err != nil {
		writeError(c, http.StatusNotFound, "用户不存在")
		return
	}
	c.JSON(http.StatusOK, u.ToSafe())
}

// Create handles:
//
//	POST /api/v1/users
//
//	@Summary      Create a user (admin)
//	@Description  Creates an account with an explicit role. Admin only.
//	@Tags         users
//	@Security     BearerAuth
//	@Accept       json
//	@Produce      json
//	@Param        request body CreateUserRequest true "New account"
//	@Success      201 {object} user.SafeUser "Created user"
//	@Failure      400 {object} ErrorResponse "Invalid input"
//	@Failure      401 {object} ErrorResponse "Not authenticated"
//	@Failure      403 {object} ErrorResponse "Not an admin"
//	@Failure      409 {object} ErrorResponse "Username already exists"
//	@Router       /users [post]
func (h *UserHandler) Create(c *gin.Context) {
	var req CreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求体无效："+err.Error())
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if err := auth.ValidateUsername(req.Username); err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	role := strings.TrimSpace(req.Role)
	if role == "" {
		role = user.RoleUser
	}
	if role != user.RoleUser && role != user.RoleAdmin {
		writeError(c, http.StatusBadRequest, "角色只能是 user 或 admin")
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email != "" {
		if err := auth.ValidateEmail(email); err != nil {
			writeError(c, http.StatusBadRequest, err.Error())
			return
		}
	}

	u := &user.User{
		ID:           user.NewID(),
		Username:     req.Username,
		Email:        email,
		PasswordHash: hash,
		Nickname:     strings.TrimSpace(req.Nickname),
		Role:         role,
	}
	if err := h.users.Create(u); err != nil {
		if errors.Is(err, user.ErrDuplicateUsername) {
			writeError(c, http.StatusConflict, "用户名已存在")
			return
		}
		if errors.Is(err, user.ErrDuplicateEmail) {
			writeError(c, http.StatusConflict, "邮箱已被使用")
			return
		}
		writeError(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusCreated, u.ToSafe())
}

// Update handles:
//
//	PUT /api/v1/users/:id
//
//	@Summary      Update a user
//	@Description  Updates nickname (self or admin) and role (admin only).
//	@Tags         users
//	@Security     BearerAuth
//	@Accept       json
//	@Produce      json
//	@Param        id path string true "User ID"
//	@Param        request body UpdateUserRequest true "Fields to update"
//	@Success      200 {object} user.SafeUser "Updated user"
//	@Failure      400 {object} ErrorResponse "Invalid input"
//	@Failure      401 {object} ErrorResponse "Not authenticated"
//	@Failure      403 {object} ErrorResponse "Not allowed to edit this user"
//	@Failure      404 {object} ErrorResponse "User not found"
//	@Router       /users/{id} [put]
func (h *UserHandler) Update(c *gin.Context) {
	id := c.Param("id")
	claims, _ := auth.ClaimsFromContext(c)
	if !canManage(claims, id) {
		writeError(c, http.StatusForbidden, "无权修改该用户")
		return
	}

	var req UpdateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求体无效："+err.Error())
		return
	}

	u, err := h.users.GetByID(id)
	if err != nil {
		writeError(c, http.StatusNotFound, "用户不存在")
		return
	}

	// Role changes require admin.
	if req.Role != nil {
		if claims == nil || claims.Role != user.RoleAdmin {
			writeError(c, http.StatusForbidden, "仅管理员可以修改角色")
			return
		}
		r := strings.TrimSpace(*req.Role)
		if r != user.RoleUser && r != user.RoleAdmin {
			writeError(c, http.StatusBadRequest, "角色只能是 user 或 admin")
			return
		}
		u.Role = r
	}
	if req.Nickname != nil {
		u.Nickname = strings.TrimSpace(*req.Nickname)
	}
	if req.Email != nil {
		email := strings.ToLower(strings.TrimSpace(*req.Email))
		if email != "" {
			if err := auth.ValidateEmail(email); err != nil {
				writeError(c, http.StatusBadRequest, err.Error())
				return
			}
		}
		u.Email = email
	}

	if err := h.users.Update(u); err != nil {
		if errors.Is(err, user.ErrDuplicateEmail) {
			writeError(c, http.StatusConflict, "邮箱已被使用")
			return
		}
		writeError(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, u.ToSafe())
}

// Delete handles:
//
//	DELETE /api/v1/users/:id
//
//	@Summary      Delete a user (admin)
//	@Description  Removes an account. Admin only.
//	@Tags         users
//	@Security     BearerAuth
//	@Produce      json
//	@Param        id path string true "User ID"
//	@Success      200 {object} map[string]any "message: 用户已删除"
//	@Failure      401 {object} ErrorResponse "Not authenticated"
//	@Failure      403 {object} ErrorResponse "Not an admin"
//	@Failure      404 {object} ErrorResponse "User not found"
//	@Router       /users/{id} [delete]
func (h *UserHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	if claims, _ := auth.ClaimsFromContext(c); claims == nil || claims.Role != user.RoleAdmin {
		writeError(c, http.StatusForbidden, "仅管理员可以删除用户")
		return
	}
	if err := h.users.Delete(id); err != nil {
		writeError(c, http.StatusNotFound, "用户不存在")
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "用户已删除"})
}

// canManage reports whether the claims may view/edit the given user: admins
// manage anyone, everyone manages themselves.
func canManage(claims *auth.Claims, targetID string) bool {
	return claims != nil && (claims.Role == user.RoleAdmin || claims.UserID == targetID)
}
