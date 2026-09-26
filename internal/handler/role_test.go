package handler

import (
	"encoding/json"
	"net/http"
	"testing"

	"wechatapp-web/internal/role"
)

func TestRoleCRUDFlow(t *testing.T) {
	r, _, _ := newAuthTestRouter(t, "test-secret")
	adminToken := login(t, r, "admin", "admin123")

	// Non-admin cannot access roles.
	registerUser(t, r, "nobody", "nobody@example.com", "secret123")
	plainToken := login(t, r, "nobody", "secret123")
	if rec := withToken(t, r, http.MethodGet, "/api/v1/roles", plainToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin list status = %d, want 403", rec.Code)
	}

	// Create a custom role.
	rec := withToken(t, r, http.MethodPost, "/api/v1/roles", adminToken, CreateRoleRequest{
		Key: "manager", Name: "经理", Description: "可以查看报表",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create role status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created role.Role
	json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Key != "manager" || created.Builtin {
		t.Fatalf("created = %+v", created)
	}

	// Duplicate key -> 409.
	if rec := withToken(t, r, http.MethodPost, "/api/v1/roles", adminToken, CreateRoleRequest{Key: "manager", Name: "x"}); rec.Code != http.StatusConflict {
		t.Errorf("duplicate role status = %d, want 409", rec.Code)
	}
	// Reserved built-in key -> 400.
	if rec := withToken(t, r, http.MethodPost, "/api/v1/roles", adminToken, CreateRoleRequest{Key: "admin", Name: "x"}); rec.Code != http.StatusBadRequest {
		t.Errorf("reserved key status = %d, want 400", rec.Code)
	}
	// Invalid key -> 400.
	if rec := withToken(t, r, http.MethodPost, "/api/v1/roles", adminToken, CreateRoleRequest{Key: "a b", Name: "x"}); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid key status = %d, want 400", rec.Code)
	}

	// List contains built-ins + the new role.
	rec = withToken(t, r, http.MethodGet, "/api/v1/roles", adminToken, nil)
	var list RoleListResponse
	json.Unmarshal(rec.Body.Bytes(), &list)
	if list.Total != 3 {
		t.Errorf("list total = %d, want 3", list.Total)
	}
	// Built-ins come first.
	if !list.Items[0].Builtin || !list.Items[1].Builtin {
		t.Errorf("built-ins should sort first: %+v", list.Items)
	}

	// Get by ID.
	rec = withToken(t, r, http.MethodGet, "/api/v1/roles/"+idStr(created.ID), adminToken, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("get role status = %d", rec.Code)
	}

	// Update name/description; key cannot change.
	name := "高级经理"
	rec = withToken(t, r, http.MethodPut, "/api/v1/roles/"+idStr(created.ID), adminToken, UpdateRoleRequest{Name: &name})
	if rec.Code != http.StatusOK {
		t.Fatalf("update role status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var updated role.Role
	json.Unmarshal(rec.Body.Bytes(), &updated)
	if updated.Name != name || updated.Key != "manager" {
		t.Errorf("updated = %+v", updated)
	}

	// Assign the custom role to a user (admin can change role).
	uID := getSelfID(t, r, plainToken)
	roleKey := "manager"
	rec = withToken(t, r, http.MethodPut, "/api/v1/users/"+uID, adminToken, UpdateUserRequest{Role: &roleKey})
	if rec.Code != http.StatusOK {
		t.Fatalf("assign role status = %d, body = %s", rec.Code, rec.Body.String())
	}
	// User with a custom role is still not an admin (403 on admin endpoints).
	if rec := withToken(t, r, http.MethodGet, "/api/v1/roles", plainToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("custom-role user admin access = %d, want 403", rec.Code)
	}

	// Deleting a role in use -> 409.
	rec = withToken(t, r, http.MethodDelete, "/api/v1/roles/"+idStr(created.ID), adminToken, nil)
	if rec.Code != http.StatusConflict {
		t.Errorf("delete in-use role status = %d, want 409", rec.Code)
	}

	// Move the user back to user role, then delete succeeds.
	userKey := "user"
	rec = withToken(t, r, http.MethodPut, "/api/v1/users/"+uID, adminToken, UpdateUserRequest{Role: &userKey})
	if rec.Code != http.StatusOK {
		t.Fatalf("reassign role status = %d", rec.Code)
	}
	rec = withToken(t, r, http.MethodDelete, "/api/v1/roles/"+idStr(created.ID), adminToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete role status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Built-in roles cannot be deleted.
	adminRole := list.Items[0] // "admin" built-in sorts first
	rec = withToken(t, r, http.MethodDelete, "/api/v1/roles/"+idStr(adminRole.ID), adminToken, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("delete builtin status = %d, want 400", rec.Code)
	}

	// The reassigned user can log in normally.
	if token := login(t, r, "nobody", "secret123"); token == "" {
		t.Error("login after role reassignment failed")
	}
}

func TestUserCreateWithCustomRole(t *testing.T) {
	r, _, _ := newAuthTestRouter(t, "test-secret")
	adminToken := login(t, r, "admin", "admin123")

	// Create a role first.
	rec := withToken(t, r, http.MethodPost, "/api/v1/roles", adminToken, CreateRoleRequest{Key: "auditor", Name: "审计"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create role: %d %s", rec.Code, rec.Body.String())
	}

	// Admin creates a user with that role.
	rec = withToken(t, r, http.MethodPost, "/api/v1/users", adminToken, CreateUserRequest{
		Username: "auditor1", Email: "auditor1@example.com", Password: "secret123", Role: "auditor",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user with custom role: %d %s", rec.Code, rec.Body.String())
	}

	// Admin creates a user with a nonexistent role -> 400.
	rec = withToken(t, r, http.MethodPost, "/api/v1/users", adminToken, CreateUserRequest{
		Username: "badrole", Email: "badrole@example.com", Password: "secret123", Role: "ghost",
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("create user with ghost role: %d, want 400", rec.Code)
	}
}
