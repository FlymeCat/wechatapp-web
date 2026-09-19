package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// MySQLStore is a Store backed by a MySQL database. It implements the Store
// interface so handlers are agnostic to the storage backend.
type MySQLStore struct {
	db *sql.DB
}

// MySQLConfig describes a MySQL connection.
type MySQLConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	DBName   string
	Charset  string
}

// DSN builds the driver data source name for the given database (empty DBName
// connects to the server without selecting a database).
func (c MySQLConfig) DSN(dbname string) string {
	charset := c.Charset
	if charset == "" {
		charset = "utf8mb4"
	}
	addr := c.Host
	if c.Port != "" {
		addr += ":" + c.Port
	}
	return fmt.Sprintf("%s:%s@tcp(%s)/%s?charset=%s&parseTime=true&loc=Local",
		c.User, c.Password, addr, dbname, charset)
}

// OpenMySQL connects to MySQL, creating the database and users table if they
// do not exist, and returns a ready store.
func OpenMySQL(cfg MySQLConfig) (*MySQLStore, error) {
	if cfg.Host == "" || cfg.User == "" {
		return nil, fmt.Errorf("mysql host and user are required")
	}

	// 1. Ensure the database exists (connect without a database).
	if cfg.DBName != "" {
		server, err := sql.Open("mysql", cfg.DSN(""))
		if err != nil {
			return nil, fmt.Errorf("open mysql server: %w", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := server.PingContext(ctx); err != nil {
			cancel()
			_ = server.Close()
			return nil, fmt.Errorf("ping mysql server: %w", err)
		}
		stmt := fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci", cfg.DBName)
		if _, err := server.ExecContext(ctx, stmt); err != nil {
			cancel()
			_ = server.Close()
			return nil, fmt.Errorf("create database %q: %w", cfg.DBName, err)
		}
		cancel()
		_ = server.Close()
	}

	// 2. Connect to the database and migrate.
	db, err := sql.Open("mysql", cfg.DSN(cfg.DBName))
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping mysql: %w", err)
	}

	s := &MySQLStore{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// NewMySQLStore connects to MySQL with a ready-made DSN and migrates.
func NewMySQLStore(dsn string) (*MySQLStore, error) {
	if dsn == "" {
		return nil, fmt.Errorf("empty mysql DSN")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping mysql: %w", err)
	}

	s := &MySQLStore{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// migrate ensures the users table exists with all current columns. For tables
// created by older versions it adds any missing columns (idempotent).
func (s *MySQLStore) migrate(ctx context.Context) error {
	const ddl = `
CREATE TABLE IF NOT EXISTS users (
	id          CHAR(32)     NOT NULL,
	username    VARCHAR(64)  NOT NULL,
	email       VARCHAR(190) NULL,
	password_hash VARCHAR(255) NOT NULL,
	nickname    VARCHAR(64)  NOT NULL DEFAULT '',
	role        VARCHAR(16)  NOT NULL DEFAULT 'user',
	mfa_secret  VARCHAR(128) NOT NULL DEFAULT '',
	created_at  DATETIME(3)  NOT NULL,
	updated_at  DATETIME(3)  NOT NULL,
	PRIMARY KEY (id),
	UNIQUE KEY uk_username (username),
	UNIQUE KEY uk_email (email)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`
	if _, err := s.db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("create users table: %w", err)
	}
	// Upgrade path for tables created before MFA/email support.
	upgrades := []struct{ name, ddl string }{
		{"mfa_secret", "ALTER TABLE users ADD COLUMN mfa_secret VARCHAR(128) NOT NULL DEFAULT ''"},
		{"email", "ALTER TABLE users ADD COLUMN email VARCHAR(190) NULL"},
	}
	for _, col := range upgrades {
		if _, err := s.db.ExecContext(ctx, col.ddl); err != nil && !isDuplicateColumn(err) {
			return fmt.Errorf("migrate column %s: %w", col.name, err)
		}
	}
	// Rows that predate the mfa_secret column keep NULL; normalize to ''.
	const backfill = "UPDATE users SET mfa_secret = '' WHERE mfa_secret IS NULL"
	if _, err := s.db.ExecContext(ctx, backfill); err != nil {
		return fmt.Errorf("backfill mfa_secret: %w", err)
	}
	// Add the email unique index only when the column was just added, so a
	// partially-migrated table (column without index) self-heals too. Existing
	// tables that already have the index skip this silently.
	const emailIndex = "ALTER TABLE users ADD UNIQUE KEY uk_email (email)"
	if _, err := s.db.ExecContext(ctx, emailIndex); err != nil && !isDuplicateIndex(err) {
		return fmt.Errorf("migrate email index: %w", err)
	}
	return nil
}

// isDuplicateColumn reports whether the error is MySQL error 1060
// (duplicate column name), which the ALTER ignores safely.
func isDuplicateColumn(err error) bool {
	return mysqlErrNumber(err) == 1060
}

// isDuplicateIndex reports whether the error is MySQL error 1061 (duplicate
// key name), which the ALTER ignores safely.
func isDuplicateIndex(err error) bool {
	return mysqlErrNumber(err) == 1061
}

func mysqlErrNumber(err error) uint16 {
	if err == nil {
		return 0
	}
	var myErr *mysql.MySQLError
	if errors.As(err, &myErr) {
		return myErr.Number
	}
	return 0
}

// DB exposes the underlying handle (used by tests).
func (s *MySQLStore) DB() *sql.DB { return s.db }

// Close closes the underlying connection pool.
func (s *MySQLStore) Close() error { return s.db.Close() }

// userColumns selects all user columns. COALESCE makes legacy rows (created
// before the email/MFA columns existed, where ALTER left them NULL) scan
// cleanly into non-nullable Go strings.
const userColumns = "id, username, COALESCE(email, '') AS email, password_hash, nickname, role, " +
	"COALESCE(mfa_secret, '') AS mfa_secret, created_at, updated_at"

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	u := &User{}
	err := row.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.Nickname, &u.Role, &u.MFASecret, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return u, nil
}

// Create inserts a new user.
func (s *MySQLStore) Create(u *User) error {
	now := time.Now()
	if u.CreatedAt.IsZero() {
		u.CreatedAt = now
	}
	u.UpdatedAt = now
	u.Email = normalizeEmail(u.Email)
	_, err := s.db.Exec(
		"INSERT INTO users (id, username, email, password_hash, nickname, role, mfa_secret, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		u.ID, u.Username, nullableEmail(u.Email), u.PasswordHash, u.Nickname, u.Role, u.MFASecret, u.CreatedAt, u.UpdatedAt,
	)
	switch {
	case isDuplicate(err) && strings.Contains(mysqlErrMessage(err), "uk_email"):
		return ErrDuplicateEmail
	case isDuplicate(err):
		return ErrDuplicateUsername
	}
	return err
}

// nullableEmail returns NULL for an empty email so the unique index tolerates
// multiple accounts without an email address.
func nullableEmail(email string) any {
	if email == "" {
		return nil
	}
	return email
}

// GetByID returns the user with the given ID.
func (s *MySQLStore) GetByID(id string) (*User, error) {
	row := s.db.QueryRow("SELECT "+userColumns+" FROM users WHERE id = ?", id)
	return scanUser(row)
}

// GetByUsername returns the user with the given username.
func (s *MySQLStore) GetByUsername(username string) (*User, error) {
	row := s.db.QueryRow("SELECT "+userColumns+" FROM users WHERE username = ?", username)
	return scanUser(row)
}

// GetByEmail returns the user with the given email (case-insensitive).
func (s *MySQLStore) GetByEmail(email string) (*User, error) {
	row := s.db.QueryRow("SELECT "+userColumns+" FROM users WHERE LOWER(email) = ?", normalizeEmail(email))
	return scanUser(row)
}

// List returns all users ordered by creation time.
func (s *MySQLStore) List() ([]*User, error) {
	rows, err := s.db.Query("SELECT " + userColumns + " FROM users ORDER BY created_at ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Update replaces the stored user (username is the identity and cannot change).
func (s *MySQLStore) Update(u *User) error {
	u.UpdatedAt = time.Now()
	u.Email = normalizeEmail(u.Email)
	res, err := s.db.Exec(
		"UPDATE users SET email = ?, password_hash = ?, nickname = ?, role = ?, mfa_secret = ?, updated_at = ? WHERE id = ?",
		nullableEmail(u.Email), u.PasswordHash, u.Nickname, u.Role, u.MFASecret, u.UpdatedAt, u.ID,
	)
	if err != nil && isDuplicate(err) {
		return ErrDuplicateEmail
	}
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		// RowsAffected can be 0 when values didn't change; verify existence.
		if _, err := s.GetByID(u.ID); err != nil {
			return ErrNotFound
		}
	}
	return nil
}

// Delete removes the user with the given ID.
func (s *MySQLStore) Delete(id string) error {
	res, err := s.db.Exec("DELETE FROM users WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// isDuplicate maps MySQL duplicate-key errors (1062) to ErrDuplicateUsername.
func isDuplicate(err error) bool {
	return mysqlErrNumber(err) == 1062
}

func mysqlErrMessage(err error) string {
	if err == nil {
		return ""
	}
	var myErr *mysql.MySQLError
	if errors.As(err, &myErr) {
		return myErr.Message
	}
	return ""
}
