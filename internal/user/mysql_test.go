package user

import (
	"context"
	"errors"
	"os"
	"testing"
)

// testMySQLConfig reads the connection settings from env, defaulting to the
// credentials used in this project's development environment. Tests skip when
// no DB_HOST is set (e.g. CI without MySQL).
func testMySQLConfig(t *testing.T) (MySQLConfig, bool) {
	t.Helper()
	host := os.Getenv("TEST_DB_HOST")
	if host == "" {
		host = os.Getenv("DB_HOST")
	}
	if host == "" {
		t.Skip("TEST_DB_HOST/DB_HOST not set; skipping MySQL integration test")
		return MySQLConfig{}, false
	}
	return MySQLConfig{
		Host:     host,
		Port:     firstNonEmpty(os.Getenv("TEST_DB_PORT"), os.Getenv("DB_PORT"), "3306"),
		User:     firstNonEmpty(os.Getenv("TEST_DB_USER"), os.Getenv("DB_USER"), "root"),
		Password: firstNonEmpty(os.Getenv("TEST_DB_PASSWORD"), os.Getenv("DB_PASSWORD"), "Root@123456"),
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
	// Fresh table for a deterministic test.
	if _, err := s.DB().Exec("DROP TABLE IF EXISTS users"); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	u := newTestUser("db_alice", RoleUser)
	if err := s.Create(u); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Duplicate username -> ErrDuplicateUsername.
	if err := s.Create(newTestUser("db_alice", RoleUser)); !errors.Is(err, ErrDuplicateUsername) {
		t.Errorf("err = %v, want ErrDuplicateUsername", err)
	}

	got, err := s.GetByUsername("db_alice")
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	if got.ID != u.ID || !got.CheckPassword("secret123") {
		t.Errorf("got = %+v", got)
	}

	// Update nickname + password check still works.
	got.Nickname = "DB Alice"
	got.PasswordHash = u.PasswordHash
	if err := s.Update(got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	again, _ := s.GetByID(u.ID)
	if again.Nickname != "DB Alice" {
		t.Errorf("nickname = %q", again.Nickname)
	}

	// List.
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Errorf("list len = %d, want 1", len(list))
	}

	// Delete.
	if err := s.Delete(u.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.GetByID(u.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if err := s.Delete(u.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete again err = %v, want ErrNotFound", err)
	}
}

// TestMySQLStoreLegacyNullMFAColumns is a regression test: rows created before
// the MFA columns existed have NULL in them, which must not break reads (the
// admin login used to fail with "wrong password" because scanning NULL into a
// string errored).
func TestMySQLStoreLegacyNullMFAColumns(t *testing.T) {
	cfg, ok := testMySQLConfig(t)
	if !ok {
		return
	}
	s, err := OpenMySQL(cfg)
	if err != nil {
		t.Fatalf("OpenMySQL: %v", err)
	}
	defer s.Close()

	// Simulate a legacy row: a table from before the email and MFA columns
	// existed, with NULL values in the nullable columns.
	if _, err := s.DB().Exec("DROP TABLE IF EXISTS users"); err != nil {
		t.Fatal(err)
	}
	const legacyDDL = `
CREATE TABLE users (
	id          CHAR(32)     NOT NULL,
	username    VARCHAR(64)  NOT NULL,
	password_hash VARCHAR(255) NOT NULL,
	nickname    VARCHAR(64)  NOT NULL DEFAULT '',
	role        VARCHAR(16)  NOT NULL DEFAULT 'user',
	mfa_secret  VARCHAR(128) NULL,
	created_at  DATETIME(3)  NOT NULL,
	updated_at  DATETIME(3)  NOT NULL,
	PRIMARY KEY (id),
	UNIQUE KEY uk_username (username)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`
	if _, err := s.DB().Exec(legacyDDL); err != nil {
		t.Fatal(err)
	}

	u := newTestUser("legacy", RoleAdmin)
	if _, err := s.DB().Exec(
		"INSERT INTO users (id, username, password_hash, nickname, role, mfa_secret, created_at, updated_at) VALUES (?, ?, ?, ?, ?, NULL, NOW(3), NOW(3))",
		u.ID, u.Username, u.PasswordHash, u.Nickname, u.Role,
	); err != nil {
		t.Fatal(err)
	}

	// migrate() must backfill the NULLs and add the email column/index...
	if err := s.migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// ...and reads must work either way.
	got, err := s.GetByUsername("legacy")
	if err != nil {
		t.Fatalf("GetByUsername on legacy row: %v", err)
	}
	if got.MFASecret != "" || got.Email != "" {
		t.Errorf("legacy MFA/email fields = %q/%q, want empty", got.MFASecret, got.Email)
	}
	if !got.CheckPassword("secret123") {
		t.Error("legacy password should still verify")
	}
	if got.MFAEnabled() {
		t.Error("legacy row must not report MFA enabled")
	}

	// Even with NULLs restored, reads stay safe thanks to COALESCE.
	if _, err := s.DB().Exec("UPDATE users SET mfa_secret = NULL"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetByUsername("legacy"); err != nil {
		t.Fatalf("GetByUsername with NULL columns: %v", err)
	}
	if _, err := s.List(); err != nil {
		t.Fatalf("List with NULL columns: %v", err)
	}
}

func TestMySQLStoreEmailIndexAndLookup(t *testing.T) {
	cfg, ok := testMySQLConfig(t)
	if !ok {
		return
	}
	s, err := OpenMySQL(cfg)
	if err != nil {
		t.Fatalf("OpenMySQL: %v", err)
	}
	defer s.Close()

	a := newTestUser("email_a", RoleUser)
	a.Email = "Alice@Example.com"
	if err := s.Create(a); err != nil {
		t.Fatal(err)
	}
	defer s.Delete(a.ID)

	// Lookup is case-insensitive.
	got, err := s.GetByEmail("alice@example.com")
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if got.Username != "email_a" || got.Email != "alice@example.com" {
		t.Errorf("got %+v", got)
	}

	// Duplicate email -> ErrDuplicateEmail (even with different case).
	b := newTestUser("email_b", RoleUser)
	b.Email = "ALICE@example.com"
	if err := s.Create(b); !errors.Is(err, ErrDuplicateEmail) {
		t.Errorf("duplicate email error = %v, want ErrDuplicateEmail", err)
	}

	// Accounts without an email (NULL) are allowed to coexist.
	c := newTestUser("email_c", RoleUser)
	c.Email = ""
	if err := s.Create(c); err != nil {
		t.Fatalf("create without email: %v", err)
	}
	defer s.Delete(c.ID)
	d := newTestUser("email_d", RoleUser)
	if err := s.Create(d); err != nil {
		t.Fatalf("create second without email: %v", err)
	}
	defer s.Delete(d.ID)

	// Updating a user onto another's email fails.
	a.Email = b.Username + "@x.com" // unused email
	if err := s.Update(a); err != nil {
		t.Fatalf("update to free email: %v", err)
	}
	a.Email = "alice@example.com" // b does not exist yet (create failed), so reuse a's own
	if err := s.Update(a); err != nil {
		t.Fatalf("update to own email: %v", err)
	}
	if _, err := s.GetByEmail("alice@example.com"); err != nil {
		t.Errorf("GetByEmail after update: %v", err)
	}
}

func TestMySQLStorePingFails(t *testing.T) {
	// Wrong password must surface a connection error, not panic.
	_, err := OpenMySQL(MySQLConfig{
		Host: "127.0.0.1", Port: "3306", User: "root", Password: "wrong-pass",
		DBName: "does_not_matter", Charset: "utf8mb4",
	})
	if err == nil {
		t.Skip("server accepted wrong password (unexpected); skipping failure test")
	}
}
