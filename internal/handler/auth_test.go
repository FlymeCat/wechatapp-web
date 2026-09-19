package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp/totp"

	"wechatapp-web/internal/auth"
	"wechatapp-web/internal/user"
)

// captureMailer records sent emails so tests can read the verification code
// that the production SMTP sender would deliver to the user's inbox.
type captureMailer struct {
	mu    sync.Mutex
	mails []capturedMail
}

type capturedMail struct {
	to, subject, body string
}

func (m *captureMailer) Send(_ string, to, subject, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mails = append(m.mails, capturedMail{to: to, subject: subject, body: body})
	return nil
}

func (m *captureMailer) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mails = nil
}

func (m *captureMailer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.mails)
}

func (m *captureMailer) last() capturedMail {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.mails) == 0 {
		return capturedMail{}
	}
	return m.mails[len(m.mails)-1]
}

var sixDigitCode = regexp.MustCompile(`\b(\d{6})\b`)

// lastCode extracts the 6-digit code from the last email body.
func (m *captureMailer) lastCode() string {
	mail := m.last()
	matches := sixDigitCode.FindStringSubmatch(mail.body)
	if len(matches) < 2 {
		return ""
	}
	return matches[1]
}

// newAuthTestRouter wires a store (no persistence), a JWT manager, a capture
// mailer and the auth+user routes the same way router.New does.
func newAuthTestRouter(t *testing.T, secret string) (*gin.Engine, user.Store, *captureMailer) {
	t.Helper()
	users := user.NewMemoryStore("")

	// Seed admin like router.seedAdmin does.
	hash, _ := auth.HashPassword("admin123")
	_ = users.Create(&user.User{
		ID: user.NewID(), Username: "admin", PasswordHash: hash,
		Nickname: "管理员", Role: user.RoleAdmin,
	})

	jwtMgr := auth.NewManager(secret, time.Hour, "wechatapp-web")
	mailer := &captureMailer{}
	authH := NewAuthHandler(users, jwtMgr, "wechatapp-web", mailer, auth.NewEmailOTPManager())
	userH := NewUserHandler(users)

	r := gin.New()
	v1 := r.Group("/api/v1")
	{
		v1.POST("/auth/register", authH.Register)
		v1.POST("/auth/login", authH.Login)
		authed := v1.Group("", jwtMgr.RequireAuth())
		{
			authed.POST("/auth/logout", authH.Logout)
			authed.GET("/auth/me", authH.Me)
			authed.DELETE("/auth/me", authH.DeleteMe)
			authed.PUT("/auth/password", authH.ChangePassword)
			authed.POST("/auth/mfa/setup", authH.MFASetup)
			authed.POST("/auth/mfa/enable", authH.MFAEnable)
			authed.DELETE("/auth/mfa", authH.MFADisable)
		}
		v1.POST("/auth/mfa/verify", jwtMgr.RequireMFAChallenge(), authH.MFAVerify)
		v1.POST("/auth/mfa/reset/request", authH.MFAResetRequestCode)
		v1.POST("/auth/mfa/reset/confirm", authH.MFAResetConfirm)
		admin := v1.Group("", jwtMgr.RequireAuth(), auth.RequireRole(user.RoleAdmin))
		{
			admin.GET("/users", userH.List)
			admin.POST("/users", userH.Create)
			admin.DELETE("/users/:id", userH.Delete)
			admin.DELETE("/users/:id/mfa", userH.MFAResetByAdmin)
		}
		detail := v1.Group("", jwtMgr.RequireAuth())
		{
			detail.GET("/users/:id", userH.Get)
			detail.PUT("/users/:id", userH.Update)
		}
	}
	return r, users, mailer
}

// registerUser creates a normal account via the register endpoint.
func registerUser(t *testing.T, r *gin.Engine, username, email, password string) {
	t.Helper()
	rec := postAuth(t, r, "/api/v1/auth/register", RegisterRequest{
		Username: username, Email: email, Password: password,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register %s status = %d, body = %s", username, rec.Code, rec.Body.String())
	}
}

func postAuth(t *testing.T, r *gin.Engine, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func getAuth(t *testing.T, r *gin.Engine, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func login(t *testing.T, r *gin.Engine, username, password string) string {
	t.Helper()
	rec := postAuth(t, r, "/api/v1/auth/login", LoginRequest{Username: username, Password: password})
	if rec.Code != http.StatusOK {
		t.Fatalf("login %s status = %d, body = %s", username, rec.Code, rec.Body.String())
	}
	var res LoginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	return res.Token
}

func TestRegisterLoginMe(t *testing.T) {
	r, _, _ := newAuthTestRouter(t, "test-secret")

	// Register.
	rec := postAuth(t, r, "/api/v1/auth/register", RegisterRequest{
		Username: "zhangsan", Email: "zhangsan@example.com", Password: "secret123", Nickname: "张三",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created user.SafeUser
	json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Role != user.RoleUser || created.Nickname != "张三" || created.Email != "zhangsan@example.com" {
		t.Errorf("created = %+v", created)
	}

	// Missing email -> 400.
	if rec := postAuth(t, r, "/api/v1/auth/register", RegisterRequest{Username: "noemail", Password: "secret123"}); rec.Code != http.StatusBadRequest {
		t.Errorf("missing email status = %d, want 400", rec.Code)
	}
	// Invalid email -> 400.
	if rec := postAuth(t, r, "/api/v1/auth/register", RegisterRequest{Username: "bademail", Email: "not-an-email", Password: "secret123"}); rec.Code != http.StatusBadRequest {
		t.Errorf("bad email status = %d, want 400", rec.Code)
	}

	// Duplicate username -> 409.
	if rec := postAuth(t, r, "/api/v1/auth/register", RegisterRequest{Username: "zhangsan", Email: "other@example.com", Password: "secret123"}); rec.Code != http.StatusConflict {
		t.Errorf("duplicate register status = %d, want 409", rec.Code)
	}
	// Duplicate email -> 409.
	if rec := postAuth(t, r, "/api/v1/auth/register", RegisterRequest{Username: "zhangsan2", Email: "ZHANGSAN@example.com", Password: "secret123"}); rec.Code != http.StatusConflict {
		t.Errorf("duplicate email status = %d, want 409", rec.Code)
	}

	// Weak password -> 400.
	if rec := postAuth(t, r, "/api/v1/auth/register", RegisterRequest{Username: "wangwu", Email: "wangwu@example.com", Password: "123"}); rec.Code != http.StatusBadRequest {
		t.Errorf("weak password status = %d, want 400", rec.Code)
	}

	// Login.
	token := login(t, r, "zhangsan", "secret123")

	// Wrong password -> 401.
	if rec := postAuth(t, r, "/api/v1/auth/login", LoginRequest{Username: "zhangsan", Password: "wrong"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong password status = %d, want 401", rec.Code)
	}

	// /auth/me with token.
	rec = getAuth(t, r, "/api/v1/auth/me", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("me status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var me user.SafeUser
	json.Unmarshal(rec.Body.Bytes(), &me)
	if me.Username != "zhangsan" || me.ID != created.ID {
		t.Errorf("me = %+v, want id %s", me, created.ID)
	}

	// /auth/me without token -> 401.
	if rec := getAuth(t, r, "/api/v1/auth/me", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("me no token status = %d, want 401", rec.Code)
	}

	// Bad token -> 401.
	if rec := getAuth(t, r, "/api/v1/auth/me", "not-a-token"); rec.Code != http.StatusUnauthorized {
		t.Errorf("me bad token status = %d, want 401", rec.Code)
	}
}

func TestLogoutRevokesToken(t *testing.T) {
	r, _, _ := newAuthTestRouter(t, "test-secret")
	token := login(t, r, "admin", "admin123")

	rec := withToken(t, r, http.MethodPost, "/api/v1/auth/logout", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("logout status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// The same token must now be rejected.
	if rec := getAuth(t, r, "/api/v1/auth/me", token); rec.Code != http.StatusUnauthorized {
		t.Errorf("me after logout status = %d, want 401", rec.Code)
	}
}

func TestChangePassword(t *testing.T) {
	r, _, _ := newAuthTestRouter(t, "test-secret")
	token := login(t, r, "admin", "admin123")

	// Wrong old password -> 400.
	rec := withToken(t, r, http.MethodPut, "/api/v1/auth/password", token, ChangePasswordRequest{OldPassword: "nope", NewPassword: "newpass1"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("wrong old password status = %d, want 400", rec.Code)
	}

	// Correct change -> 200.
	rec = withToken(t, r, http.MethodPut, "/api/v1/auth/password", token, ChangePasswordRequest{OldPassword: "admin123", NewPassword: "newpass1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("change password status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Old password no longer works.
	if rec := postAuth(t, r, "/api/v1/auth/login", LoginRequest{Username: "admin", Password: "admin123"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("old password login status = %d, want 401", rec.Code)
	}
	// New password works.
	if rec := postAuth(t, r, "/api/v1/auth/login", LoginRequest{Username: "admin", Password: "newpass1"}); rec.Code != http.StatusOK {
		t.Errorf("new password login status = %d, want 200", rec.Code)
	}
}

func TestUserManagementAdminFlow(t *testing.T) {
	r, _, _ := newAuthTestRouter(t, "test-secret")
	adminToken := login(t, r, "admin", "admin123")

	// Admin creates a user.
	rec := withToken(t, r, http.MethodPost, "/api/v1/users", adminToken, CreateUserRequest{
		Username: "lisi", Password: "secret123", Nickname: "李四", Role: user.RoleUser,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created user.SafeUser
	json.Unmarshal(rec.Body.Bytes(), &created)

	// Regular user logs in.
	userToken := login(t, r, "lisi", "secret123")

	// List users (admin only).
	rec = getAuth(t, r, "/api/v1/users", adminToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	var list UserListResponse
	json.Unmarshal(rec.Body.Bytes(), &list)
	if list.Total != 2 {
		t.Errorf("total = %d, want 2 (admin + lisi)", list.Total)
	}

	// Regular user cannot list -> 403.
	if rec := getAuth(t, r, "/api/v1/users", userToken); rec.Code != http.StatusForbidden {
		t.Errorf("user list status = %d, want 403", rec.Code)
	}

	// User can view self.
	if rec := getAuth(t, r, "/api/v1/users/"+created.ID, userToken); rec.Code != http.StatusOK {
		t.Errorf("self view status = %d, want 200", rec.Code)
	}

	// User cannot view admin -> 403.
	adminID := getSelfID(t, r, adminToken)
	if rec := getAuth(t, r, "/api/v1/users/"+adminID, userToken); rec.Code != http.StatusForbidden {
		t.Errorf("view admin as user status = %d, want 403", rec.Code)
	}

	// User cannot delete anyone -> 403.
	rec = withToken(t, r, http.MethodDelete, "/api/v1/users/"+created.ID, userToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("delete as user status = %d, want 403", rec.Code)
	}

	// Admin deletes the user -> 200.
	rec = withToken(t, r, http.MethodDelete, "/api/v1/users/"+created.ID, adminToken, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("delete as admin status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Deleted user cannot log in.
	if rec := postAuth(t, r, "/api/v1/auth/login", LoginRequest{Username: "lisi", Password: "secret123"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("login deleted user status = %d, want 401", rec.Code)
	}
}

func TestUpdateUserSelfAndAdmin(t *testing.T) {
	r, users, _ := newAuthTestRouter(t, "test-secret")
	adminToken := login(t, r, "admin", "admin123")

	rec := withToken(t, r, http.MethodPost, "/api/v1/users", adminToken, CreateUserRequest{
		Username: "lisi", Password: "secret123", Role: user.RoleUser,
	})
	var created user.SafeUser
	json.Unmarshal(rec.Body.Bytes(), &created)
	userToken := login(t, r, "lisi", "secret123")

	// Self can change nickname.
	nick := "李四改"
	rec = withToken(t, r, http.MethodPut, "/api/v1/users/"+created.ID, userToken, UpdateUserRequest{Nickname: &nick})
	if rec.Code != http.StatusOK {
		t.Fatalf("self update status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var updated user.SafeUser
	json.Unmarshal(rec.Body.Bytes(), &updated)
	if updated.Nickname != nick {
		t.Errorf("nickname = %q, want %q", updated.Nickname, nick)
	}

	// User cannot promote self to admin -> 403.
	role := user.RoleAdmin
	rec = withToken(t, r, http.MethodPut, "/api/v1/users/"+created.ID, userToken, UpdateUserRequest{Role: &role})
	if rec.Code != http.StatusForbidden {
		t.Errorf("self role change status = %d, want 403", rec.Code)
	}

	// Admin can change role.
	rec = withToken(t, r, http.MethodPut, "/api/v1/users/"+created.ID, adminToken, UpdateUserRequest{Role: &role})
	if rec.Code != http.StatusOK {
		t.Fatalf("admin role change status = %d, body = %s", rec.Code, rec.Body.String())
	}
	json.Unmarshal(rec.Body.Bytes(), &updated)
	if updated.Role != user.RoleAdmin {
		t.Errorf("role = %q, want admin", updated.Role)
	}

	// Self can set their email (needed for MFA email reset).
	email := "lisi@example.com"
	rec = withToken(t, r, http.MethodPut, "/api/v1/users/"+created.ID, userToken, UpdateUserRequest{Email: &email})
	if rec.Code != http.StatusOK {
		t.Fatalf("self email update status = %d, body = %s", rec.Code, rec.Body.String())
	}
	json.Unmarshal(rec.Body.Bytes(), &updated)
	if updated.Email != email {
		t.Errorf("email = %q, want %q", updated.Email, email)
	}
	// Invalid email -> 400.
	bad := "not-an-email"
	if rec := withToken(t, r, http.MethodPut, "/api/v1/users/"+created.ID, userToken, UpdateUserRequest{Email: &bad}); rec.Code != http.StatusBadRequest {
		t.Errorf("bad email status = %d, want 400", rec.Code)
	}
	// The account is now findable by email (used by the MFA reset flow).
	if u, err := users.GetByEmail("lisi@example.com"); err != nil || u.Username != "lisi" {
		t.Errorf("GetByEmail = %v, %v; want lisi", u, err)
	}
}

func TestDeleteMeSelfService(t *testing.T) {
	r, _, _ := newAuthTestRouter(t, "test-secret")
	// Create a second admin so deleting the first is allowed.
	adminToken := login(t, r, "admin", "admin123")
	rec := withToken(t, r, http.MethodPost, "/api/v1/users", adminToken, CreateUserRequest{
		Username: "admin2", Password: "secret123", Role: user.RoleAdmin,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create admin2 status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Register a normal user, then delete their own account.
	regRec := postAuth(t, r, "/api/v1/auth/register", RegisterRequest{Username: "delete_me", Email: "delete@example.com", Password: "secret123"})
	var created user.SafeUser
	json.Unmarshal(regRec.Body.Bytes(), &created)
	userToken := login(t, r, "delete_me", "secret123")

	// Wrong password -> 400.
	rec = withToken(t, r, http.MethodDelete, "/api/v1/auth/me", userToken, DeleteMeRequest{Password: "wrong"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("wrong password status = %d, want 400", rec.Code)
	}

	// Correct password -> 200 and token revoked.
	rec = withToken(t, r, http.MethodDelete, "/api/v1/auth/me", userToken, DeleteMeRequest{Password: "secret123"})
	if rec.Code != http.StatusOK {
		t.Fatalf("delete me status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec := getAuth(t, r, "/api/v1/auth/me", userToken); rec.Code != http.StatusUnauthorized {
		t.Errorf("me after delete status = %d, want 401 (token revoked)", rec.Code)
	}

	// Account is really gone: login fails.
	if rec := postAuth(t, r, "/api/v1/auth/login", LoginRequest{Username: "delete_me", Password: "secret123"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("login deleted account status = %d, want 401", rec.Code)
	}
	// And admin no longer sees it in the list.
	rec = getAuth(t, r, "/api/v1/users", adminToken)
	var list UserListResponse
	json.Unmarshal(rec.Body.Bytes(), &list)
	if list.Total != 2 { // admin + admin2
		t.Errorf("total = %d, want 2", list.Total)
	}
}

func TestDeleteMeLastAdminProtected(t *testing.T) {
	r, _, _ := newAuthTestRouter(t, "test-secret")
	adminToken := login(t, r, "admin", "admin123")

	// Only admin exists -> deleting it must fail.
	rec := withToken(t, r, http.MethodDelete, "/api/v1/auth/me", adminToken, DeleteMeRequest{Password: "admin123"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("last admin delete status = %d, want 400", rec.Code)
	}
	// Account still works.
	if rec := getAuth(t, r, "/api/v1/auth/me", adminToken); rec.Code != http.StatusOK {
		t.Errorf("admin me status = %d, want 200", rec.Code)
	}
}

func TestDeleteMeRequiresToken(t *testing.T) {
	r, _, _ := newAuthTestRouter(t, "test-secret")
	rec := withToken(t, r, http.MethodDelete, "/api/v1/auth/me", "", DeleteMeRequest{Password: "x"})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no token status = %d, want 401", rec.Code)
	}
}

// enableMFAFor drives setup+enable for the given user token and returns the
// TOTP secret.
func enableMFAFor(t *testing.T, r *gin.Engine, token string) string {
	t.Helper()
	rec := withToken(t, r, http.MethodPost, "/api/v1/auth/mfa/setup", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("mfa setup status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var setup MFASetupResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &setup); err != nil {
		t.Fatal(err)
	}
	if setup.Secret == "" || !strings.HasPrefix(setup.OtpauthURL, "otpauth://totp/") {
		t.Fatalf("bad setup response: %+v", setup)
	}
	if !strings.HasPrefix(setup.QRCode, "data:image/png;base64,") {
		t.Errorf("qr_code not a PNG data URI: %.40s", setup.QRCode)
	}

	code := currentTOTP(t, setup.Secret)
	rec = withToken(t, r, http.MethodPost, "/api/v1/auth/mfa/enable", token,
		MFAEnableRequest{Secret: setup.Secret, Code: code})
	if rec.Code != http.StatusOK {
		t.Fatalf("mfa enable status = %d, body = %s", rec.Code, rec.Body.String())
	}
	return setup.Secret
}

func currentTOTP(t *testing.T, secret string) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func TestMFAEnrollmentAndLogin(t *testing.T) {
	r, _, _ := newAuthTestRouter(t, "test-secret")

	// Register + login a normal user.
	registerUser(t, r, "mfauser", "mfauser@example.com", "secret123")
	userToken := login(t, r, "mfauser", "secret123")

	// Setup rejects a bad code and enables nothing.
	rec := withToken(t, r, http.MethodPost, "/api/v1/auth/mfa/setup", userToken, nil)
	var setup MFASetupResponse
	json.Unmarshal(rec.Body.Bytes(), &setup)
	rec = withToken(t, r, http.MethodPost, "/api/v1/auth/mfa/enable", userToken,
		MFAEnableRequest{Secret: setup.Secret, Code: "000000"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad code enable status = %d, want 400", rec.Code)
	}

	secret := enableMFAFor(t, r, userToken)

	// /auth/me now reports mfa_enabled.
	rec = getAuth(t, r, "/api/v1/auth/me", userToken)
	var me user.SafeUser
	json.Unmarshal(rec.Body.Bytes(), &me)
	if !me.MFAEnabled {
		t.Error("me.mfa_enabled = false, want true")
	}

	// Login now returns an MFA challenge instead of an access token.
	rec = postAuth(t, r, "/api/v1/auth/login", LoginRequest{Username: "mfauser", Password: "secret123"})
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d", rec.Code)
	}
	var loginRes LoginResponse
	json.Unmarshal(rec.Body.Bytes(), &loginRes)
	if !loginRes.MFARequired || loginRes.MFAToken == "" || loginRes.Token != "" {
		t.Fatalf("expected mfa challenge, got %+v", loginRes)
	}

	// The challenge token must not work on normal routes.
	if rec := getAuth(t, r, "/api/v1/auth/me", loginRes.MFAToken); rec.Code != http.StatusUnauthorized {
		t.Errorf("challenge token on /auth/me status = %d, want 401", rec.Code)
	}

	// Wrong code -> 400.
	rec = withToken(t, r, http.MethodPost, "/api/v1/auth/mfa/verify", loginRes.MFAToken, MFAVerifyRequest{Code: "000000"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("wrong code verify status = %d, want 400", rec.Code)
	}

	// Correct TOTP -> full access token.
	rec = withToken(t, r, http.MethodPost, "/api/v1/auth/mfa/verify", loginRes.MFAToken,
		MFAVerifyRequest{Code: currentTOTP(t, secret)})
	if rec.Code != http.StatusOK {
		t.Fatalf("verify status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var verified LoginResponse
	json.Unmarshal(rec.Body.Bytes(), &verified)
	if verified.Token == "" || verified.MFARequired {
		t.Fatalf("expected access token, got %+v", verified)
	}
	if rec := getAuth(t, r, "/api/v1/auth/me", verified.Token); rec.Code != http.StatusOK {
		t.Errorf("access token rejected: %d", rec.Code)
	}

	// The challenge token is single-use.
	rec = withToken(t, r, http.MethodPost, "/api/v1/auth/mfa/verify", loginRes.MFAToken,
		MFAVerifyRequest{Code: currentTOTP(t, secret)})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("reused challenge status = %d, want 401", rec.Code)
	}
}

func TestMFADisableFlow(t *testing.T) {
	r, _, _ := newAuthTestRouter(t, "test-secret")
	registerUser(t, r, "mfauser", "mfauser@example.com", "secret123")
	userToken := login(t, r, "mfauser", "secret123")
	secret := enableMFAFor(t, r, userToken)

	// Wrong password -> 400.
	rec := withToken(t, r, http.MethodDelete, "/api/v1/auth/mfa", userToken,
		MFADisableRequest{Password: "nope", Code: currentTOTP(t, secret)})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("wrong password status = %d, want 400", rec.Code)
	}
	// Wrong code -> 400.
	rec = withToken(t, r, http.MethodDelete, "/api/v1/auth/mfa", userToken,
		MFADisableRequest{Password: "secret123", Code: "000000"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("wrong code status = %d, want 400", rec.Code)
	}
	// Correct -> 200 and MFA off.
	rec = withToken(t, r, http.MethodDelete, "/api/v1/auth/mfa", userToken,
		MFADisableRequest{Password: "secret123", Code: currentTOTP(t, secret)})
	if rec.Code != http.StatusOK {
		t.Fatalf("disable status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Login returns a real token again (no second factor).
	rec = postAuth(t, r, "/api/v1/auth/login", LoginRequest{Username: "mfauser", Password: "secret123"})
	var res LoginResponse
	json.Unmarshal(rec.Body.Bytes(), &res)
	if res.MFARequired || res.Token == "" {
		t.Errorf("expected normal login after disable, got %+v", res)
	}
}

func TestMFAResetByEmail(t *testing.T) {
	r, _, mailer := newAuthTestRouter(t, "test-secret")
	registerUser(t, r, "resetme", "resetme@example.com", "secret123")
	userToken := login(t, r, "resetme", "secret123")
	enableMFAFor(t, r, userToken)

	// Locked out: login now requires the second factor.
	rec := postAuth(t, r, "/api/v1/auth/login", LoginRequest{Username: "resetme", Password: "secret123"})
	var res LoginResponse
	json.Unmarshal(rec.Body.Bytes(), &res)
	if !res.MFARequired {
		t.Fatal("expected mfa_required")
	}

	// Unknown email -> generic 200, nothing sent.
	mailer.reset()
	rec = postAuth(t, r, "/api/v1/auth/mfa/reset/request", MFAResetRequest{Email: "ghost@example.com", Password: "secret123"})
	if rec.Code != http.StatusOK {
		t.Fatalf("unknown email status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if mailer.count() != 0 {
		t.Error("email sent for unknown account")
	}

	// Wrong password -> generic 200, nothing sent.
	mailer.reset()
	rec = postAuth(t, r, "/api/v1/auth/mfa/reset/request", MFAResetRequest{Email: "resetme@example.com", Password: "wrongpass"})
	if rec.Code != http.StatusOK {
		t.Fatalf("wrong password status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if mailer.count() != 0 {
		t.Error("email sent with wrong password")
	}

	// Correct credentials -> email with a 6-digit code (email lookup is
	// case-insensitive).
	mailer.reset()
	rec = postAuth(t, r, "/api/v1/auth/mfa/reset/request", MFAResetRequest{Email: "RESETME@Example.com", Password: "secret123"})
	if rec.Code != http.StatusOK {
		t.Fatalf("request status = %d, body = %s", rec.Code, rec.Body.String())
	}
	code := mailer.lastCode()
	if len(code) != 6 {
		t.Fatalf("email code = %q, want 6 digits", code)
	}
	if mailer.last().to != "resetme@example.com" {
		t.Errorf("email sent to %q, want resetme@example.com", mailer.last().to)
	}

	// Wrong code -> 400, MFA untouched.
	rec = postAuth(t, r, "/api/v1/auth/mfa/reset/confirm", MFAResetConfirmRequest{Email: "resetme@example.com", Code: "000000"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("wrong code confirm status = %d, want 400", rec.Code)
	}
	rec = postAuth(t, r, "/api/v1/auth/login", LoginRequest{Username: "resetme", Password: "secret123"})
	json.Unmarshal(rec.Body.Bytes(), &res)
	if !res.MFARequired {
		t.Error("MFA should still be on after a wrong code")
	}

	// Correct code -> 200, MFA off.
	rec = postAuth(t, r, "/api/v1/auth/mfa/reset/confirm", MFAResetConfirmRequest{Email: "resetme@example.com", Code: code})
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Single-factor login works again.
	rec = postAuth(t, r, "/api/v1/auth/login", LoginRequest{Username: "resetme", Password: "secret123"})
	json.Unmarshal(rec.Body.Bytes(), &res)
	if res.MFARequired || res.Token == "" {
		t.Errorf("expected normal login after email reset, got %+v", res)
	}

	// The code is single-use.
	rec = postAuth(t, r, "/api/v1/auth/mfa/reset/confirm", MFAResetConfirmRequest{Email: "resetme@example.com", Code: code})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("reused code confirm status = %d, want 400", rec.Code)
	}
}

func TestMFAResetRequiresEnabledMFA(t *testing.T) {
	r, _, mailer := newAuthTestRouter(t, "test-secret")
	// Account exists with a valid password but MFA was never enabled.
	registerUser(t, r, "plain", "plain@example.com", "secret123")

	mailer.reset()
	rec := postAuth(t, r, "/api/v1/auth/mfa/reset/request", MFAResetRequest{Email: "plain@example.com", Password: "secret123"})
	if rec.Code != http.StatusOK {
		t.Fatalf("request status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if mailer.count() != 0 {
		t.Error("email sent although MFA is not enabled")
	}

	// Confirm has nothing to reset -> 400.
	rec = postAuth(t, r, "/api/v1/auth/mfa/reset/confirm", MFAResetConfirmRequest{Email: "plain@example.com", Code: "123456"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("confirm without MFA status = %d, want 400", rec.Code)
	}
}

func TestMFAAdminReset(t *testing.T) {
	r, _, _ := newAuthTestRouter(t, "test-secret")
	adminToken := login(t, r, "admin", "admin123")
	registerUser(t, r, "mfauser", "mfauser@example.com", "secret123")
	userToken := login(t, r, "mfauser", "secret123")
	enableMFAFor(t, r, userToken)

	// The user now needs a second factor.
	rec := postAuth(t, r, "/api/v1/auth/login", LoginRequest{Username: "mfauser", Password: "secret123"})
	var res LoginResponse
	json.Unmarshal(rec.Body.Bytes(), &res)
	if !res.MFARequired {
		t.Fatal("expected mfa_required")
	}

	// Non-admin cannot reset anyone's MFA.
	rec = withToken(t, r, http.MethodDelete, "/api/v1/users/"+getSelfID(t, r, adminToken)+"/mfa", userToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("non-admin reset status = %d, want 403", rec.Code)
	}

	// Admin resets the user's MFA -> login is single-factor again.
	userID := getSelfID(t, r, userToken)
	rec = withToken(t, r, http.MethodDelete, "/api/v1/users/"+userID+"/mfa", adminToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin reset status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec = postAuth(t, r, "/api/v1/auth/login", LoginRequest{Username: "mfauser", Password: "secret123"})
	json.Unmarshal(rec.Body.Bytes(), &res)
	if res.MFARequired || res.Token == "" {
		t.Errorf("expected normal login after admin reset, got %+v", res)
	}
}

func TestMFASetupWhenAlreadyEnabled(t *testing.T) {
	r, _, _ := newAuthTestRouter(t, "test-secret")
	registerUser(t, r, "mfauser", "mfauser@example.com", "secret123")
	userToken := login(t, r, "mfauser", "secret123")
	enableMFAFor(t, r, userToken)

	rec := withToken(t, r, http.MethodPost, "/api/v1/auth/mfa/setup", userToken, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("re-setup status = %d, want 400", rec.Code)
	}
}

func TestMFAVerifyRequiresChallengeToken(t *testing.T) {
	r, _, _ := newAuthTestRouter(t, "test-secret")
	adminToken := login(t, r, "admin", "admin123")

	// No token.
	rec := withToken(t, r, http.MethodPost, "/api/v1/auth/mfa/verify", "", MFAVerifyRequest{Code: "123456"})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no token status = %d, want 401", rec.Code)
	}
	// An access token is not a challenge token.
	rec = withToken(t, r, http.MethodPost, "/api/v1/auth/mfa/verify", adminToken, MFAVerifyRequest{Code: "123456"})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("access token status = %d, want 401", rec.Code)
	}
}

// --- helpers --------------------------------------------------------------
func withToken(t *testing.T, r *gin.Engine, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func getSelfID(t *testing.T, r *gin.Engine, token string) string {
	t.Helper()
	rec := getAuth(t, r, "/api/v1/auth/me", token)
	var me user.SafeUser
	json.Unmarshal(rec.Body.Bytes(), &me)
	return me.ID
}
