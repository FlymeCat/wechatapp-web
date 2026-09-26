package role

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newTestRole(key string) *Role {
	return &Role{
		Key:  key,
		Name: key,
	}
}

func TestMemoryStoreCRUD(t *testing.T) {
	s := NewMemoryStore("")

	r := newTestRole("manager")
	if err := s.Create(r); err != nil {
		t.Fatal(err)
	}
	if r.ID <= 0 {
		t.Fatalf("Create did not assign a positive ID: %d", r.ID)
	}
	// Duplicate key.
	if err := s.Create(newTestRole("manager")); !errors.Is(err, ErrDuplicateKey) {
		t.Errorf("duplicate key = %v, want ErrDuplicateKey", err)
	}

	got, err := s.GetByKey("manager")
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != "manager" || got.Name != "manager" {
		t.Errorf("got %+v", got)
	}
	if _, err := s.GetByKey("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing key = %v, want ErrNotFound", err)
	}

	// Update name.
	r.Name = "经理"
	if err := s.Update(r); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetByID(r.ID)
	if got.Name != "经理" {
		t.Errorf("name = %q, want 经理", got.Name)
	}

	// Key is immutable.
	clone := *r
	clone.Key = "boss"
	if err := s.Update(&clone); !errors.Is(err, ErrKeyImmutable) {
		t.Errorf("key change = %v, want ErrKeyImmutable", err)
	}

	// Delete.
	if err := s.Delete(r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetByID(r.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete = %v, want ErrNotFound", err)
	}
}

func TestMemoryStoreBuiltinProtection(t *testing.T) {
	s := NewMemoryStore("")
	builtins := BuiltinRoles()
	for _, b := range builtins {
		if err := s.Create(b); err != nil {
			t.Fatal(err)
		}
	}
	admin, _ := s.GetByKey(AdminKey)
	if err := s.Delete(admin.ID); !errors.Is(err, ErrBuiltin) {
		t.Errorf("delete builtin = %v, want ErrBuiltin", err)
	}
	if _, err := s.GetByKey(AdminKey); err != nil {
		t.Errorf("builtin should still exist: %v", err)
	}
}

func TestMemoryStorePersistence(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "roles.json")

	s := NewMemoryStore(file)
	r := newTestRole("manager")
	if err := s.Create(r); err != nil {
		t.Fatal(err)
	}
	// Force a save + reload from a fresh store.
	s2 := NewMemoryStore(file)
	got, err := s2.GetByKey("manager")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Name != r.Name {
		t.Errorf("reloaded name = %q, want %q", got.Name, r.Name)
	}

	// A missing file starts empty.
	if _, err := NewMemoryStore(filepath.Join(dir, "missing.json")).GetByKey("x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing file = %v, want ErrNotFound", err)
	}

	// Unreadable path must not panic (load ignored on failure).
	_ = os.WriteFile(file, []byte("{not json"), 0o644)
	_ = NewMemoryStore(file)
}

func TestValidateKey(t *testing.T) {
	for _, ok := range []string{"admin", "user", "manager", "op_1", "audit-log"} {
		if err := ValidateKey(ok); err != nil {
			t.Errorf("ValidateKey(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "a", "has space", "中文", "x@y", "toolongtoolongtoolongtoolongtoolongtoolong"} {
		if err := ValidateKey(bad); err == nil {
			t.Errorf("ValidateKey(%q) = nil, want error", bad)
		}
	}
}

// --- MySQL integration (skipped without TEST_DB_* env) ---------------------

func testMySQLConfig(t *testing.T) (MySQLConfig, bool) {
	t.Helper()
	host := os.Getenv("TEST_DB_HOST")
	if host == "" {
		t.Skip("TEST_DB_HOST not set; skipping MySQL integration test")
	}
	port := os.Getenv("TEST_DB_PORT")
	if port == "" {
		port = "3306"
	}
	return MySQLConfig{
		Host:     host,
		Port:     port,
		User:     firstNonEmpty(os.Getenv("TEST_DB_USER"), "root"),
		Password: os.Getenv("TEST_DB_PASSWORD"),
		DBName:   firstNonEmpty(os.Getenv("TEST_DB_NAME"), "wechatapp-web_test"),
		Charset:  "utf8mb4",
	}, true
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func TestMySQLStoreCRUD(t *testing.T) {
	cfg, ok := testMySQLConfig(t)
	if !ok {
		return
	}
	s, err := OpenMySQL(cfg)
	if err != nil {
		t.Fatalf("OpenMySQL: %v", err)
	}
	defer s.Close()
	if _, err := s.DB().Exec("DROP TABLE IF EXISTS roles"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}

	r := newTestRole("manager")
	if err := s.Create(r); err != nil {
		t.Fatal(err)
	}
	defer s.Delete(r.ID)
	if err := s.Create(newTestRole("manager")); !errors.Is(err, ErrDuplicateKey) {
		t.Errorf("duplicate key = %v, want ErrDuplicateKey", err)
	}
	got, err := s.GetByKey("manager")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "manager" {
		t.Errorf("got %+v", got)
	}
	// Key immutability via Update.
	r.Name = "经理"
	if err := s.Update(r); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetByID(r.ID)
	if got.Name != "经理" {
		t.Errorf("name = %q", got.Name)
	}
	// List.
	list, err := s.List()
	if err != nil || len(list) < 1 {
		t.Errorf("list len=%d err=%v", len(list), err)
	}
}

func TestMySQLStoreLegacyIDMigration(t *testing.T) {
	cfg, ok := testMySQLConfig(t)
	if !ok {
		return
	}
	s, err := OpenMySQL(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Simulate a roles table from before the bigint ID change.
	if _, err := s.DB().Exec("DROP TABLE IF EXISTS roles"); err != nil {
		t.Fatal(err)
	}
	const legacyDDL = `
CREATE TABLE roles (
	id          CHAR(32)     NOT NULL,
	role_key    VARCHAR(64)  NOT NULL,
	name        VARCHAR(64)  NOT NULL,
	description VARCHAR(255) NOT NULL DEFAULT '',
	builtin     TINYINT(1)   NOT NULL DEFAULT 0,
	created_at  DATETIME(3)  NOT NULL,
	updated_at  DATETIME(3)  NOT NULL,
	PRIMARY KEY (id),
	UNIQUE KEY uk_role_key (role_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`
	if _, err := s.DB().Exec(legacyDDL); err != nil {
		t.Fatal(err)
	}
	const legacyID = "fedcba9876543210fedcba9876543210"
	if _, err := s.DB().Exec(
		"INSERT INTO roles (id, role_key, name, description, builtin, created_at, updated_at) VALUES (?, 'manager', '经理', '旧数据', 0, NOW(3), NOW(3))",
		legacyID,
	); err != nil {
		t.Fatal(err)
	}

	if err := s.migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	got, err := s.GetByKey("manager")
	if err != nil {
		t.Fatalf("GetByKey after migration: %v", err)
	}
	if got.ID <= 0 {
		t.Errorf("migrated ID = %d, want a positive auto-increment value", got.ID)
	}
	if got.Name != "经理" || got.Description != "旧数据" {
		t.Errorf("migrated role lost data: %+v", got)
	}
}

func TestMySQLStoreBuiltinSeeding(t *testing.T) {
	cfg, ok := testMySQLConfig(t)
	if !ok {
		return
	}
	s, err := OpenMySQL(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.DB().Exec("DROP TABLE IF EXISTS roles"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureBuiltin(ctx); err != nil {
		t.Fatal(err)
	}
	// Idempotent.
	if err := s.EnsureBuiltin(ctx); err != nil {
		t.Fatalf("second EnsureBuiltin: %v", err)
	}
	if _, err := s.GetByKey(AdminKey); err != nil {
		t.Errorf("admin role missing: %v", err)
	}
	if _, err := s.GetByKey(UserKey); err != nil {
		t.Errorf("user role missing: %v", err)
	}
}
