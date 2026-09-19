package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wechatapp-web/internal/auth"
	"wechatapp-web/internal/mail"
	"wechatapp-web/internal/user"
)

// AuthHandler exposes the authentication endpoints.
type AuthHandler struct {
	users     user.Store
	jwt       *auth.Manager
	mfaIssuer string // name shown in authenticator apps
	mailer    mail.Sender
	emailOTP  *auth.EmailOTPManager
}

// NewAuthHandler creates an auth handler. mfaIssuer may be empty, in which case
// the auth package default is used.
func NewAuthHandler(users user.Store, jwt *auth.Manager, mfaIssuer string, mailer mail.Sender, emailOTP *auth.EmailOTPManager) *AuthHandler {
	return &AuthHandler{users: users, jwt: jwt, mfaIssuer: mfaIssuer, mailer: mailer, emailOTP: emailOTP}
}

// RegisterRequest is the body of POST /auth/register.
type RegisterRequest struct {
	Username string `json:"username" binding:"required" example:"zhangsan"` // 用户名（3-32位字母/数字/下划线）
	Email    string `json:"email" binding:"required" example:"user@example.com"`
	Password string `json:"password" binding:"required" example:"secret123"` // 密码（至少6位）
	Nickname string `json:"nickname" example:"张三"`                           // 昵称（可选）
}

// LoginRequest is the body of POST /auth/login.
type LoginRequest struct {
	Username string `json:"username" binding:"required" example:"admin"`    // 用户名
	Password string `json:"password" binding:"required" example:"admin123"` // 密码
}

// LoginResponse is returned by POST /auth/login.
//
// When the account has MFA enabled the response instead carries
// mfa_required=true and an mfa_token (no access token); call
// POST /auth/mfa/verify with that token to finish signing in.
type LoginResponse struct {
	Token     string         `json:"token,omitempty"`      // JWT（放在 Authorization: Bearer <token> 头中）
	TokenType string         `json:"token_type,omitempty"` // 恒为 Bearer
	ExpiresAt time.Time      `json:"expires_at"`           // 过期时间（access 或 mfa_token 的）
	User      *user.SafeUser `json:"user,omitempty"`       // 用户信息（需要 MFA 第二步时不返回）
	// MFARequired 为 true 时，需带上 mfa_token 调用 POST /auth/mfa/verify 完成登录。
	MFARequired bool `json:"mfa_required"`
	// MFAToken 是第二步验证用的短期令牌（5 分钟，一次性）。
	MFAToken string `json:"mfa_token,omitempty"`
}

// ChangePasswordRequest is the body of PUT /auth/password.
type ChangePasswordRequest struct {
	OldPassword string `json:"old_password" binding:"required" example:"old123456"` // 原密码
	NewPassword string `json:"new_password" binding:"required" example:"new123456"` // 新密码（至少6位）
}

// DeleteMeRequest is the body of DELETE /auth/me (注销账号).
type DeleteMeRequest struct {
	Password string `json:"password" binding:"required" example:"secret123"` // 当前密码确认
}

// Register handles:
//
//	POST /api/v1/auth/register
//
//	@Summary      Register a new user
//	@Description  Creates a normal (user-role) account. Login afterwards to get a token.
//	@Tags         auth
//	@Accept       json
//	@Produce      json
//	@Param        request body RegisterRequest true "Username, password and optional nickname"
//	@Success      201 {object} user.SafeUser "Created user"
//	@Failure      400 {object} ErrorResponse "Invalid input"
//	@Failure      409 {object} ErrorResponse "Username already exists"
//	@Router       /auth/register [post]
func (h *AuthHandler) Register(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求体无效："+err.Error())
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if err := auth.ValidateUsername(req.Username); err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := auth.ValidateEmail(req.Email); err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	u := &user.User{
		ID:           user.NewID(),
		Username:     req.Username,
		Email:        strings.ToLower(strings.TrimSpace(req.Email)),
		PasswordHash: hash,
		Nickname:     strings.TrimSpace(req.Nickname),
		Role:         user.RoleUser,
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

// Login handles:
//
//	POST /api/v1/auth/login
//
//	@Summary      Login and get a JWT
//	@Description  Authenticates with username/password and returns a JWT to be
//	@Description  sent as "Authorization: Bearer <token>".
//	@Tags         auth
//	@Accept       json
//	@Produce      json
//	@Param        request body LoginRequest true "Username and password"
//	@Success      200 {object} LoginResponse "JWT and user info"
//	@Failure      400 {object} ErrorResponse "Missing fields"
//	@Failure      401 {object} ErrorResponse "Wrong username or password"
//	@Router       /auth/login [post]
func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求体无效："+err.Error())
		return
	}

	u, err := h.users.GetByUsername(strings.TrimSpace(req.Username))
	if err != nil || !auth.CheckPassword(u.PasswordHash, req.Password) {
		// Same message for both cases to avoid leaking which usernames exist.
		writeError(c, http.StatusUnauthorized, "用户名或密码错误")
		return
	}

	// Password OK. If MFA is on, stop here and require the second factor.
	if u.MFAEnabled() {
		challenge, expiresAt, err := h.jwt.IssueMFAChallenge(u.ID, u.Username, u.Role)
		if err != nil {
			writeError(c, http.StatusInternalServerError, "签发 token 失败："+err.Error())
			return
		}
		c.JSON(http.StatusOK, LoginResponse{
			MFARequired: true,
			MFAToken:    challenge,
			ExpiresAt:   expiresAt,
		})
		return
	}

	token, expiresAt, err := h.jwt.Issue(u.ID, u.Username, u.Role)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "签发 token 失败："+err.Error())
		return
	}

	safe := u.ToSafe()
	c.JSON(http.StatusOK, LoginResponse{
		Token:     token,
		TokenType: "Bearer",
		ExpiresAt: expiresAt,
		User:      &safe,
	})
}

// Logout handles:
//
//	POST /api/v1/auth/logout
//
//	@Summary      Logout (revoke current token)
//	@Description  Revokes the presented JWT so it can no longer be used.
//	@Tags         auth
//	@Security     BearerAuth
//	@Success      200 {object} map[string]any "message: 已退出登录"
//	@Failure      401 {object} ErrorResponse "Missing or invalid token"
//	@Router       /auth/logout [post]
func (h *AuthHandler) Logout(c *gin.Context) {
	claims, ok := auth.ClaimsFromContext(c)
	if !ok {
		writeError(c, http.StatusUnauthorized, "未登录")
		return
	}
	h.jwt.Revoke(claims)
	c.JSON(http.StatusOK, gin.H{"message": "已退出登录"})
}

// Me handles:
//
//	GET /api/v1/auth/me
//
//	@Summary      Get current user profile
//	@Description  Returns the profile of the authenticated user.
//	@Tags         auth
//	@Security     BearerAuth
//	@Produce      json
//	@Success      200 {object} user.SafeUser "Current user profile"
//	@Failure      401 {object} ErrorResponse "Missing or invalid token"
//	@Failure      404 {object} ErrorResponse "User no longer exists"
//	@Router       /auth/me [get]
func (h *AuthHandler) Me(c *gin.Context) {
	uid := auth.UserIDFromContext(c)
	u, err := h.users.GetByID(uid)
	if err != nil {
		writeError(c, http.StatusNotFound, "用户不存在")
		return
	}
	c.JSON(http.StatusOK, u.ToSafe())
}

// ChangePassword handles:
//
//	PUT /api/v1/auth/password
//
//	@Summary      Change own password
//	@Description  Changes the password of the authenticated user after verifying the old one.
//	@Tags         auth
//	@Security     BearerAuth
//	@Accept       json
//	@Produce      json
//	@Param        request body ChangePasswordRequest true "Old and new password"
//	@Success      200 {object} map[string]any "message: 密码修改成功"
//	@Failure      400 {object} ErrorResponse "Invalid input or wrong old password"
//	@Failure      401 {object} ErrorResponse "Missing or invalid token"
//	@Failure      404 {object} ErrorResponse "User no longer exists"
//	@Router       /auth/password [put]
func (h *AuthHandler) ChangePassword(c *gin.Context) {
	var req ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求体无效："+err.Error())
		return
	}

	uid := auth.UserIDFromContext(c)
	u, err := h.users.GetByID(uid)
	if err != nil {
		writeError(c, http.StatusNotFound, "用户不存在")
		return
	}
	if !auth.CheckPassword(u.PasswordHash, req.OldPassword) {
		writeError(c, http.StatusBadRequest, "原密码错误")
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	u.PasswordHash = hash
	if err := h.users.Update(u); err != nil {
		writeError(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "密码修改成功"})
}

// DeleteMe handles:
//
//	DELETE /api/v1/auth/me
//
//	@Summary      Delete my own account (注销账号)
//	@Description  Deletes the authenticated user's own account after verifying
//	@Description  the password, and revokes the current token. The last admin
//	@Description  cannot be deleted this way.
//	@Tags         auth
//	@Security     BearerAuth
//	@Accept       json
//	@Produce      json
//	@Param        request body DeleteMeRequest true "Password confirmation"
//	@Success      200 {object} map[string]any "message: 账号已注销"
//	@Failure      400 {object} ErrorResponse "Wrong password, or last admin cannot be deleted"
//	@Failure      401 {object} ErrorResponse "Missing or invalid token"
//	@Failure      404 {object} ErrorResponse "User no longer exists"
//	@Router       /auth/me [delete]
func (h *AuthHandler) DeleteMe(c *gin.Context) {
	var req DeleteMeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求体无效："+err.Error())
		return
	}

	uid := auth.UserIDFromContext(c)
	claims, _ := auth.ClaimsFromContext(c)
	u, err := h.users.GetByID(uid)
	if err != nil {
		writeError(c, http.StatusNotFound, "用户不存在")
		return
	}
	if !auth.CheckPassword(u.PasswordHash, req.Password) {
		writeError(c, http.StatusBadRequest, "密码错误")
		return
	}

	// Never leave the system without an admin.
	if u.Role == user.RoleAdmin && isLastAdmin(h.users, u.ID) {
		writeError(c, http.StatusBadRequest, "最后一个管理员不能注销")
		return
	}

	if err := h.users.Delete(uid); err != nil {
		writeError(c, http.StatusInternalServerError, err.Error())
		return
	}
	// The token must not survive the account.
	h.jwt.Revoke(claims)
	c.JSON(http.StatusOK, gin.H{"message": "账号已注销"})
}

// isLastAdmin reports whether u is the only admin in the store.
func isLastAdmin(users user.Store, excludeID string) bool {
	list, err := users.List()
	if err != nil {
		return true // fail closed
	}
	for _, u := range list {
		if u.Role == user.RoleAdmin && u.ID != excludeID {
			return false
		}
	}
	return true
}
