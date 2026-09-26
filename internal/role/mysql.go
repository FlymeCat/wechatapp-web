package role

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/go-sql-driver/mysql"

	"wechatapp-web/internal/mysqlutil"
)

// MySQLStore is a Store backed by a MySQL database. It implements the Store
// interface so handlers are agnostic to the storage backend.
type MySQLStore struct {
	db *sql.DB
}

// MySQLConfig describes a MySQL connection (reuses the user store's schema).
type MySQLConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	DBName   string
	Charset  string
}

// OpenMySQL connects to MySQL and ensures the roles table exists.
func OpenMySQL(cfg MySQLConfig) (*MySQLStore, error) {
	if cfg.Host == "" || cfg.User == "" {
		return nil, fmt.Errorf("mysql host and user are required")
	}
	charset := cfg.Charset
	if charset == "" {
		charset = "utf8mb4"
	}
	addr := cfg.Host
	if cfg.Port != "" {
		addr += ":" + cfg.Port
	}
	dsn := func(dbname string) string {
		return fmt.Sprintf("%s:%s@tcp(%s)/%s?charset=%s&parseTime=true&loc=Local",
			cfg.User, cfg.Password, addr, dbname, charset)
	}

	// Ensure the database exists (connect without a database), like the user
	// store does, so both stores work standalone.
	if cfg.DBName != "" {
		server, err := sql.Open("mysql", dsn(""))
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

	db, err := sql.Open("mysql", dsn(cfg.DBName))
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

// migrate ensures the roles table exists, rebuilding it first if it uses the
// old CHAR(32) ID schema.
func (s *MySQLStore) migrate(ctx context.Context) error {
	oldRows, err := mysqlutil.MigrateIDToBigInt(ctx, s.db, "roles")
	if err != nil {
		return err
	}
	const ddl = `
CREATE TABLE IF NOT EXISTS roles (
	id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
	role_key    VARCHAR(64)  NOT NULL,
	name        VARCHAR(64)  NOT NULL,
	description VARCHAR(255) NOT NULL DEFAULT '',
	builtin     TINYINT(1)   NOT NULL DEFAULT 0,
	created_at  DATETIME(3)  NOT NULL,
	updated_at  DATETIME(3)  NOT NULL,
	PRIMARY KEY (id),
	UNIQUE KEY uk_role_key (role_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`
	if _, err := s.db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("create roles table: %w", err)
	}
	if err := mysqlutil.ReinsertMigrated(ctx, s.db, "roles", oldRows); err != nil {
		return err
	}
	return nil
}

// DB exposes the underlying handle (used by tests).
func (s *MySQLStore) DB() *sql.DB { return s.db }

// Close closes the underlying connection pool.
func (s *MySQLStore) Close() error { return s.db.Close() }

const roleColumns = "id, role_key, name, description, builtin, created_at, updated_at"

func scanRole(row interface{ Scan(...any) error }) (*Role, error) {
	r := &Role{}
	err := row.Scan(&r.ID, &r.Key, &r.Name, &r.Description, &r.Builtin, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return r, nil
}

// Create inserts a new role. The database assigns the auto-increment ID,
// which is written back to r.ID.
func (s *MySQLStore) Create(r *Role) error {
	now := time.Now()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	r.UpdatedAt = now
	res, err := s.db.Exec(
		"INSERT INTO roles (role_key, name, description, builtin, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		r.Key, r.Name, r.Description, r.Builtin, r.CreatedAt, r.UpdatedAt,
	)
	if isDuplicateKey(err) {
		return ErrDuplicateKey
	}
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("read last insert id: %w", err)
	}
	r.ID = id
	return nil
}

// GetByID returns the role with the given ID.
func (s *MySQLStore) GetByID(id int64) (*Role, error) {
	row := s.db.QueryRow("SELECT "+roleColumns+" FROM roles WHERE id = ?", id)
	return scanRole(row)
}

// GetByKey returns the role with the given key.
func (s *MySQLStore) GetByKey(key string) (*Role, error) {
	row := s.db.QueryRow("SELECT "+roleColumns+" FROM roles WHERE role_key = ?", key)
	return scanRole(row)
}

// List returns all roles ordered by built-in first, then creation time.
func (s *MySQLStore) List() ([]*Role, error) {
	rows, err := s.db.Query("SELECT " + roleColumns + " FROM roles ORDER BY builtin DESC, created_at ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Role{}
	for rows.Next() {
		r, err := scanRole(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Update replaces the stored role. The key is immutable.
func (s *MySQLStore) Update(r *Role) error {
	r.UpdatedAt = time.Now()
	res, err := s.db.Exec(
		"UPDATE roles SET name = ?, description = ?, updated_at = ? WHERE id = ? AND role_key = ?",
		r.Name, r.Description, r.UpdatedAt, r.ID, r.Key,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		// Either the role vanished or the key changed; check existence.
		cur, err := s.GetByID(r.ID)
		if err != nil {
			return ErrNotFound
		}
		if cur.Key != r.Key {
			return ErrKeyImmutable
		}
	}
	return nil
}

// Delete removes a role. Built-in roles cannot be deleted.
func (s *MySQLStore) Delete(id int64) error {
	r, err := s.GetByID(id)
	if err != nil {
		return err
	}
	if r.Builtin {
		return ErrBuiltin
	}
	res, err := s.db.Exec("DELETE FROM roles WHERE id = ?", id)
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

func isDuplicateKey(err error) bool {
	if err == nil {
		return false
	}
	var myErr *mysql.MySQLError
	if errors.As(err, &myErr) {
		return myErr.Number == 1062
	}
	return false
}

// UsersWithRole counts how many users hold the given role key, used to block
// deletion of roles that are still assigned. It reads the users table directly
// so the role package does not depend on the user package.
func (s *MySQLStore) UsersWithRole(ctx context.Context, roleKey string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE role = ?", roleKey).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// EnsureBuiltin inserts the built-in roles if they are missing (idempotent).
// The admin/user keys must always exist so authorization anchors hold.
func (s *MySQLStore) EnsureBuiltin(ctx context.Context) error {
	builtins := BuiltinRoles()
	for _, b := range builtins {
		if _, err := s.GetByKey(b.Key); errors.Is(err, ErrNotFound) {
			if err := s.Create(b); err != nil && !errors.Is(err, ErrDuplicateKey) {
				return fmt.Errorf("seed builtin role %q: %w", b.Key, err)
			}
		}
	}
	return nil
}
