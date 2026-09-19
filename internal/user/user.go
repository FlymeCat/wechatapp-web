// Package user defines the user model and a thread-safe user store used by
// the authentication and user-management endpoints.
package user

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Roles supported by the system.
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// ErrNotFound is returned when a user does not exist.
var ErrNotFound = errors.New("user not found")

// ErrDuplicateUsername is returned when creating a user whose username already
// exists.
var ErrDuplicateUsername = errors.New("username already exists")

// ErrDuplicateEmail is returned when creating a user whose email is already
// taken by another account.
var ErrDuplicateEmail = errors.New("email already exists")

// User is a stored account. PasswordHash and MFA secrets are never serialized
// to JSON.
type User struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	Email        string `json:"email"`
	PasswordHash string `json:"-"`
	Nickname     string `json:"nickname"`
	Role         string `json:"role"` // admin | user
	// MFASecret is the base32 TOTP secret; empty means MFA is not enabled.
	MFASecret string    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// MFAEnabled reports whether the account has two-factor authentication on.
func (u *User) MFAEnabled() bool { return u.MFASecret != "" }

// SafeUser is the user object exposed to API responses, with no secrets.
type SafeUser struct {
	ID         string    `json:"id"`
	Username   string    `json:"username"`
	Email      string    `json:"email"`
	Nickname   string    `json:"nickname"`
	Role       string    `json:"role"`
	MFAEnabled bool      `json:"mfa_enabled"` // 是否已开启两步验证
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// ToSafe converts a stored user into its safe representation.
func (u *User) ToSafe() SafeUser {
	return SafeUser{
		ID:         u.ID,
		Username:   u.Username,
		Email:      u.Email,
		Nickname:   u.Nickname,
		Role:       u.Role,
		MFAEnabled: u.MFAEnabled(),
		CreatedAt:  u.CreatedAt,
		UpdatedAt:  u.UpdatedAt,
	}
}

// CheckPassword reports whether the given plaintext password matches the
// stored bcrypt hash.
func (u *User) CheckPassword(plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(plain)) == nil
}

// Store is the persistence abstraction for users. The memory implementation
// can be swapped for a database-backed one later.
type Store interface {
	Create(u *User) error
	GetByID(id string) (*User, error)
	GetByUsername(username string) (*User, error)
	GetByEmail(email string) (*User, error)
	List() ([]*User, error)
	Update(u *User) error
	Delete(id string) error
}

// MemoryStore keeps users in memory, optionally persisting to a JSON file on
// every mutation when File is non-empty.
type MemoryStore struct {
	mu      sync.RWMutex
	users   map[string]*User  // by id
	byName  map[string]string // username -> id
	byEmail map[string]string // normalized email -> id
	file    string
}

// NewMemoryStore creates an empty in-memory store. If file is non-empty the
// store loads existing users from it and persists every mutation to it.
func NewMemoryStore(file string) *MemoryStore {
	s := &MemoryStore{
		users:   make(map[string]*User),
		byName:  make(map[string]string),
		byEmail: make(map[string]string),
		file:    file,
	}
	if file != "" {
		_ = s.load()
	}
	return s
}

// normalizeEmail lowercases and trims an email address for keying.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// Create stores a new user. The ID and CreatedAt/UpdatedAt must be set by the
// caller.
func (s *MemoryStore) Create(u *User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byName[u.Username]; ok {
		return ErrDuplicateUsername
	}
	email := normalizeEmail(u.Email)
	if email != "" {
		if _, ok := s.byEmail[email]; ok {
			return ErrDuplicateEmail
		}
	}
	now := time.Now()
	if u.CreatedAt.IsZero() {
		u.CreatedAt = now
	}
	u.UpdatedAt = now
	u.Email = email
	cp := *u
	s.users[u.ID] = &cp
	s.byName[u.Username] = u.ID
	if email != "" {
		s.byEmail[email] = u.ID
	}
	return s.save()
}

// GetByID returns a copy of the user with the given ID.
func (s *MemoryStore) GetByID(id string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *u
	return &cp, nil
}

// GetByUsername returns a copy of the user with the given username.
func (s *MemoryStore) GetByUsername(username string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byName[username]
	if !ok {
		return nil, ErrNotFound
	}
	u, ok := s.users[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *u
	return &cp, nil
}

// GetByEmail returns a copy of the user with the given email (case-insensitive).
func (s *MemoryStore) GetByEmail(email string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byEmail[normalizeEmail(email)]
	if !ok {
		return nil, ErrNotFound
	}
	u, ok := s.users[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *u
	return &cp, nil
}

// List returns all users ordered by creation time.
func (s *MemoryStore) List() ([]*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*User, 0, len(s.users))
	for _, u := range s.users {
		cp := *u
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// Update replaces the stored user. The Username must not change (it is the
// identity key); change it via create/delete instead.
func (s *MemoryStore) Update(u *User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.users[u.ID]
	if !ok {
		return ErrNotFound
	}
	if cur.Username != u.Username {
		return fmt.Errorf("username cannot be changed")
	}
	email := normalizeEmail(u.Email)
	if email != "" {
		if otherID, ok := s.byEmail[email]; ok && otherID != u.ID {
			return ErrDuplicateEmail
		}
	}
	u.Email = email
	u.UpdatedAt = time.Now()
	cp := *u
	s.users[u.ID] = &cp
	// Refresh the email index (the old entry is dropped even if the email
	// was cleared).
	delete(s.byEmail, normalizeEmail(cur.Email))
	if email != "" {
		s.byEmail[email] = u.ID
	}
	return s.save()
}

// Delete removes the user with the given ID.
func (s *MemoryStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[id]
	if !ok {
		return ErrNotFound
	}
	delete(s.users, id)
	delete(s.byName, u.Username)
	delete(s.byEmail, normalizeEmail(u.Email))
	return s.save()
}

// save persists the store to file when configured.
func (s *MemoryStore) save() error {
	if s.file == "" {
		return nil
	}
	users := make([]persistedUser, 0, len(s.users))
	for _, u := range s.users {
		users = append(users, toPersisted(u))
	}
	return writeJSONFile(s.file, users)
}

// load reads users from file when configured. Ignored on failure so a missing
// file simply starts empty.
func (s *MemoryStore) load() error {
	users, err := readJSONFile(s.file)
	if err != nil {
		return err
	}
	for _, u := range users {
		u.Email = normalizeEmail(u.Email)
		s.users[u.ID] = u
		s.byName[u.Username] = u.ID
		if u.Email != "" {
			s.byEmail[u.Email] = u.ID
		}
	}
	return nil
}
