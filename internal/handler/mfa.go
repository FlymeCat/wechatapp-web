package handler

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"wechatapp-web/internal/auth"
	"wechatapp-web/internal/user"
)

// MFASetupResponse is returned by POST /auth/mfa/setup.
type MFASetupResponse struct {
	Secret     string `json:"secret"`      // base32 密钥（手动输入时使用）
	OtpauthURL string `json:"otpauth_url"` // otpauth:// URI，可自行生成二维码
	QRCode     string `json:"qr_code"`     // data:image/png;base64,... 可直接放进 <img src>
}

// MFAEnableRequest is the body of POST /auth/mfa/enable.
type MFAEnableRequest struct {
	Secret string `json:"secret" binding:"required" example:"JBSWY3DPEHPK3PXP"` // setup 返回的 secret
	Code   string `json:"code" binding:"required" example:"123456"`             // 认证器 App 当前 6 位验证码
}

// MFAEnableResponse is returned by POST /auth/mfa/enable.
type MFAEnableResponse struct {
	Message string `json:"message"` // 提示信息
}

// MFAVerifyRequest is the body of POST /auth/mfa/verify (登录第二步).
type MFAVerifyRequest struct {
	Code string `json:"code" binding:"required" example:"123456"` // 认证器 App 当前 6 位验证码
}

// MFADisableRequest is the body of DELETE /auth/mfa.
type MFADisableRequest struct {
	Password string `json:"password" binding:"required" example:"secret123"` // 当前密码
	Code     string `json:"code" binding:"required" example:"123456"`        // 当前验证码
}

// MFAResetRequest is the body of POST /auth/mfa/reset/request.
type MFAResetRequest struct {
	Email    string `json:"email" binding:"required" example:"user@example.com"` // 账号绑定的邮箱
	Password string `json:"password" binding:"required" example:"secret123"`     // 账号密码
}

// MFAResetConfirmRequest is the body of POST /auth/mfa/reset/confirm.
type MFAResetConfirmRequest struct {
	Email string `json:"email" binding:"required" example:"user@example.com"` // 账号绑定的邮箱
	Code  string `json:"code" binding:"required" example:"123456"`            // 邮箱收到的 6 位验证码
}

// MFASetup handles:
//
//	POST /api/v1/auth/mfa/setup
//
//	@Summary      Begin MFA setup (生成 TOTP 密钥)
//	@Description  Generates a TOTP secret and provisioning URI/QR code. Nothing
//	@Description  is persisted until /auth/mfa/enable confirms a valid code.
//	@Tags         mfa
//	@Security     BearerAuth
//	@Produce      json
//	@Success      200 {object} MFASetupResponse "Secret, otpauth URL and QR code"
//	@Failure      400 {object} ErrorResponse "MFA already enabled"
//	@Failure      401 {object} ErrorResponse "Not authenticated"
//	@Router       /auth/mfa/setup [post]
func (h *AuthHandler) MFASetup(c *gin.Context) {
	u, err := h.currentUser(c)
	if err != nil {
		writeError(c, http.StatusNotFound, "用户不存在")
		return
	}
	if u.MFAEnabled() {
		writeError(c, http.StatusBadRequest, "已开启两步验证，请先关闭后再重新绑定")
		return
	}

	key, err := auth.NewTOTPKey(auth.TOTPParams{Issuer: h.mfaIssuer, AccountName: u.Username})
	if err != nil {
		writeError(c, http.StatusInternalServerError, "生成密钥失败："+err.Error())
		return
	}
	png, err := auth.QRCodePNG(key)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "生成二维码失败："+err.Error())
		return
	}

	c.JSON(http.StatusOK, MFASetupResponse{
		Secret:     key.Secret(),
		OtpauthURL: key.URL(),
		QRCode:     "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
	})
}

// MFAEnable handles:
//
//	POST /api/v1/auth/mfa/enable
//
//	@Summary      Confirm and enable MFA
//	@Description  Verifies the submitted code against the setup secret and
//	@Description  stores it, turning on two-factor authentication.
//	@Tags         mfa
//	@Security     BearerAuth
//	@Accept       json
//	@Produce      json
//	@Param        request body MFAEnableRequest true "Secret from setup and a current code"
//	@Success      200 {object} MFAEnableResponse "MFA enabled"
//	@Failure      400 {object} ErrorResponse "Invalid secret or code"
//	@Failure      401 {object} ErrorResponse "Not authenticated"
//	@Router       /auth/mfa/enable [post]
func (h *AuthHandler) MFAEnable(c *gin.Context) {
	var req MFAEnableRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求体无效："+err.Error())
		return
	}
	secret := strings.ToUpper(strings.TrimSpace(req.Secret))
	if !auth.VerifyTOTP(secret, req.Code) {
		writeError(c, http.StatusBadRequest, "验证码错误，请确认认证器 App 时间准确后重试")
		return
	}

	u, err := h.currentUser(c)
	if err != nil {
		writeError(c, http.StatusNotFound, "用户不存在")
		return
	}
	if u.MFAEnabled() {
		writeError(c, http.StatusBadRequest, "已开启两步验证")
		return
	}

	u.MFASecret = secret
	if err := h.users.Update(u); err != nil {
		writeError(c, http.StatusInternalServerError, err.Error())
		return
	}

	c.JSON(http.StatusOK, MFAEnableResponse{Message: "两步验证已开启"})
}

// MFAVerify handles:
//
//	POST /api/v1/auth/mfa/verify
//
//	@Summary      Complete login with the second factor
//	@Description  Exchanges the short-lived mfa_token returned by /auth/login
//	@Description  plus a TOTP code for a full access token.
//	@Tags         mfa
//	@Security     BearerAuth
//	@Accept       json
//	@Produce      json
//	@Param        request body MFAVerifyRequest true "TOTP code"
//	@Success      200 {object} LoginResponse "Full access token"
//	@Failure      400 {object} ErrorResponse "Invalid code"
//	@Failure      401 {object} ErrorResponse "Missing/invalid/expired MFA challenge token"
//	@Router       /auth/mfa/verify [post]
func (h *AuthHandler) MFAVerify(c *gin.Context) {
	var req MFAVerifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求体无效："+err.Error())
		return
	}
	claims, ok := auth.ClaimsFromContext(c)
	if !ok {
		writeError(c, http.StatusUnauthorized, "请先使用账号密码登录")
		return
	}

	u, err := h.users.GetByID(claims.UserID)
	if err != nil {
		writeError(c, http.StatusNotFound, "用户不存在")
		return
	}
	if !u.MFAEnabled() {
		writeError(c, http.StatusBadRequest, "该账号未开启两步验证")
		return
	}

	if !auth.VerifyTOTP(u.MFASecret, req.Code) {
		writeError(c, http.StatusBadRequest, "验证码错误")
		return
	}

	// The challenge token is single-use.
	h.jwt.Revoke(claims)

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

// MFADisable handles:
//
//	DELETE /api/v1/auth/mfa
//
//	@Summary      Disable MFA
//	@Description  Turns off two-factor authentication after verifying the
//	@Description  account password and a current TOTP code.
//	@Tags         mfa
//	@Security     BearerAuth
//	@Accept       json
//	@Produce      json
//	@Param        request body MFADisableRequest true "Password and current code"
//	@Success      200 {object} map[string]any "message: 两步验证已关闭"
//	@Failure      400 {object} ErrorResponse "Wrong password or code"
//	@Failure      401 {object} ErrorResponse "Not authenticated"
//	@Router       /auth/mfa [delete]
func (h *AuthHandler) MFADisable(c *gin.Context) {
	var req MFADisableRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求体无效："+err.Error())
		return
	}
	u, err := h.currentUser(c)
	if err != nil {
		writeError(c, http.StatusNotFound, "用户不存在")
		return
	}
	if !u.MFAEnabled() {
		writeError(c, http.StatusBadRequest, "该账号未开启两步验证")
		return
	}
	if !auth.CheckPassword(u.PasswordHash, req.Password) {
		writeError(c, http.StatusBadRequest, "密码错误")
		return
	}
	if !auth.VerifyTOTP(u.MFASecret, req.Code) {
		writeError(c, http.StatusBadRequest, "验证码错误")
		return
	}

	u.MFASecret = ""
	if err := h.users.Update(u); err != nil {
		writeError(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "两步验证已关闭"})
}

// MFAResetRequestCode handles:
//
//	POST /api/v1/auth/mfa/reset/request
//
//	@Summary      Send an MFA reset code by email
//	@Description  When a user lost their authenticator, this sends a 6-digit
//	@Description  verification code to the account email. Requires the account
//	@Description  password. The response is generic to avoid account enumeration.
//	@Tags         mfa
//	@Accept       json
//	@Produce      json
//	@Param        request body MFAResetRequest true "Email and password"
//	@Success      200 {object} map[string]any "Generic success message"
//	@Failure      400 {object} ErrorResponse "Invalid body"
//	@Router       /auth/mfa/reset/request [post]
func (h *AuthHandler) MFAResetRequestCode(c *gin.Context) {
	var req MFAResetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求体无效："+err.Error())
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))

	u, err := h.users.GetByEmail(email)
	if err == nil && u.MFAEnabled() && auth.CheckPassword(u.PasswordHash, req.Password) {
		code, fresh, gerr := h.emailOTP.Generate(email)
		if gerr != nil {
			writeError(c, http.StatusInternalServerError, "生成验证码失败："+gerr.Error())
			return
		}
		if fresh {
			appName := h.mfaIssuer
			if appName == "" {
				appName = "wechatapp-web"
			}
			body := fmt.Sprintf(
				"您的账号 %s 正在申请重置两步验证。\n\n验证码：%s\n\n验证码 10 分钟内有效，请勿泄露给他人。如非本人操作，请忽略此邮件。",
				u.Username, code,
			)
			if serr := h.mailer.Send("", email, "【"+appName+"】两步验证重置验证码", body); serr != nil {
				h.emailOTP.Invalidate(email)
				writeError(c, http.StatusInternalServerError, "邮件发送失败："+serr.Error())
				return
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "如果该邮箱存在且已开启两步验证，验证码已发送到邮箱"})
}

// MFAResetConfirm handles:
//
//	POST /api/v1/auth/mfa/reset/confirm
//
//	@Summary      Confirm an MFA reset with the emailed code
//	@Description  Verifies the emailed code and disables two-factor
//	@Description  authentication so the user can log in again with just their
//	@Description  password and re-enroll a fresh authenticator.
//	@Tags         mfa
//	@Accept       json
//	@Produce      json
//	@Param        request body MFAResetConfirmRequest true "Email and emailed code"
//	@Success      200 {object} map[string]any "message: 两步验证已重置"
//	@Failure      400 {object} ErrorResponse "Wrong/expired code or MFA not enabled"
//	@Failure      404 {object} ErrorResponse "User not found"
//	@Router       /auth/mfa/reset/confirm [post]
func (h *AuthHandler) MFAResetConfirm(c *gin.Context) {
	var req MFAResetConfirmRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "请求体无效："+err.Error())
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))

	u, err := h.users.GetByEmail(email)
	if err != nil {
		writeError(c, http.StatusNotFound, "用户不存在")
		return
	}
	if !u.MFAEnabled() {
		writeError(c, http.StatusBadRequest, "该账号未开启两步验证")
		return
	}
	if verr := h.emailOTP.Verify(email, req.Code); verr != nil {
		writeError(c, http.StatusBadRequest, "验证码错误或已过期")
		return
	}

	u.MFASecret = ""
	if err := h.users.Update(u); err != nil {
		writeError(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "两步验证已重置，请使用密码登录后重新绑定认证器"})
}

// MFAResetByAdmin handles:
//
//	DELETE /api/v1/users/:id/mfa
//
//	@Summary      Reset a user's MFA (admin)
//	@Description  Disables two-factor authentication for another account, for
//	@Description  example when the user lost their authenticator. Admin only.
//	@Tags         users
//	@Security     BearerAuth
//	@Produce      json
//	@Param        id path string true "User ID"
//	@Success      200 {object} map[string]any "message: 已重置该用户的两步验证"
//	@Failure      401 {object} ErrorResponse "Not authenticated"
//	@Failure      403 {object} ErrorResponse "Not an admin"
//	@Failure      404 {object} ErrorResponse "User not found"
//	@Router       /users/{id}/mfa [delete]
func (h *UserHandler) MFAResetByAdmin(c *gin.Context) {
	id := c.Param("id")
	u, err := h.users.GetByID(id)
	if err != nil {
		writeError(c, http.StatusNotFound, "用户不存在")
		return
	}
	if !u.MFAEnabled() {
		writeError(c, http.StatusBadRequest, "该用户未开启两步验证")
		return
	}
	u.MFASecret = ""
	if err := h.users.Update(u); err != nil {
		writeError(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("已重置用户 %s 的两步验证", u.Username)})
}

// --- helpers -------------------------------------------------------------

// currentUser loads the authenticated user from the request context.
func (h *AuthHandler) currentUser(c *gin.Context) (*user.User, error) {
	uid := auth.UserIDFromContext(c)
	return h.users.GetByID(uid)
}
