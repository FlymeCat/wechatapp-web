// Package role defines the role model and stores used by the role-management
// endpoints. Roles are dynamic (admin-created) except for two built-in roles
// ("admin" and "user") that can be renamed but never deleted.
package role

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Keys of the built-in roles. They are fixed strings the authorization layer
// relies on (e.g. RequireRole("admin")), so their keys must never change.
const (
	AdminKey = "admin"
	UserKey  = "user"
)

// ErrNotFound is returned when a role does not exist.
var ErrNotFound = errors.New("role not found")

// ErrDuplicateKey is returned when creating a role whose key already exists.
var ErrDuplicateKey = errors.New("role key already exists")

// ErrBuiltin is returned when mutating a built-in role in a forbidden way.
var ErrBuiltin = errors.New("built-in role cannot be deleted or have its key changed")

// ErrKeyImmutable is returned when trying to change a role's key.
var ErrKeyImmutable = errors.New("role key cannot be changed")

// ErrInUse is returned when deleting a role that is still assigned to users.
var ErrInUse = errors.New("role is still assigned to users")

var roleKeyRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{2,32}$`)

// Role is a named group that users can be assigned to. Key is the stable
// identifier stored on the user record and used by RequireRole. ID is an
// auto-incrementing 64-bit integer.
type Role struct {
	ID          int64     `json:"id"`
	Key         string    `json:"key"`         // 唯一标识，如 admin / user / manager
	Name        string    `json:"name"`        // 显示名称
	Description string    `json:"description"` // 描述
	Builtin     bool      `json:"builtin"`     // 内置角色不可删除、key 不可改
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ValidateKey checks a role key's format.
func ValidateKey(key string) error {
	key = strings.TrimSpace(key)
	if !roleKeyRe.MatchString(key) {
		return fmt.Errorf("角色标识需为 2-32 位字母/数字/下划线/连字符")
	}
	return nil
}

// BuiltinRoles returns the two built-in roles with fresh IDs/timestamps.
// Seeding uses them to guarantee the authorization anchors always exist.
func BuiltinRoles() []*Role {
	now := time.Now()
	return []*Role{
		{
			Key:         AdminKey,
			Name:        "管理员",
			Description: "系统内置超级管理员角色",
			Builtin:     true,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		{
			Key:         UserKey,
			Name:        "普通用户",
			Description: "系统内置默认角色",
			Builtin:     true,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
	}
}

// Store is the persistence abstraction for roles.
type Store interface {
	Create(r *Role) error
	GetByID(id int64) (*Role, error)
	GetByKey(key string) (*Role, error)
	List() ([]*Role, error)
	Update(r *Role) error
	Delete(id int64) error
}

// MemoryStore keeps roles in memory, optionally persisting to a JSON file on
// every mutation when File is non-empty.
type MemoryStore struct {
	mu     sync.RWMutex
	nextID int64
	roles  map[int64]*Role  // by id
	byKey  map[string]int64 // key -> id
	file   string
}

// NewMemoryStore creates an empty in-memory store. If file is non-empty the
// store loads existing roles from it and persists every mutation to it.
func NewMemoryStore(file string) *MemoryStore {
	s := &MemoryStore{
		nextID: 1,
		roles:  make(map[int64]*Role),
		byKey:  make(map[string]int64),
		file:   file,
	}
	if file != "" {
		_ = s.load()
	}
	return s
}

// Create stores a new role. The store assigns the auto-incrementing ID and
// timestamps; the caller sets the remaining fields.
func (s *MemoryStore) Create(r *Role) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byKey[r.Key]; ok {
		return ErrDuplicateKey
	}
	now := time.Now()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	r.UpdatedAt = now
	r.ID = s.nextID
	s.nextID++
	cp := *r
	s.roles[r.ID] = &cp
	s.byKey[r.Key] = r.ID
	return s.save()
}

// GetByID returns a copy of the role with the given ID.
func (s *MemoryStore) GetByID(id int64) (*Role, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.roles[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *r
	return &cp, nil
}

// GetByKey returns a copy of the role with the given key.
func (s *MemoryStore) GetByKey(key string) (*Role, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byKey[key]
	if !ok {
		return nil, ErrNotFound
	}
	r, ok := s.roles[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *r
	return &cp, nil
}

// List returns all roles ordered by creation time (built-ins first).
func (s *MemoryStore) List() ([]*Role, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Role, 0, len(s.roles))
	for _, r := range s.roles {
		cp := *r
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Builtin != out[j].Builtin {
			return out[i].Builtin
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// Update replaces the stored role. The key is immutable (it is the identity
// used by user records and authorization); built-in roles keep their key.
func (s *MemoryStore) Update(r *Role) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.roles[r.ID]
	if !ok {
		return ErrNotFound
	}
	if cur.Key != r.Key {
		return ErrKeyImmutable
	}
	r.UpdatedAt = time.Now()
	cp := *r
	s.roles[r.ID] = &cp
	return s.save()
}

// Delete removes a role. Built-in roles and roles still assigned to users
// cannot be deleted (the caller checks the assignment count).
func (s *MemoryStore) Delete(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.roles[id]
	if !ok {
		return ErrNotFound
	}
	if r.Builtin {
		return ErrBuiltin
	}
	delete(s.roles, id)
	delete(s.byKey, r.Key)
	return s.save()
}

// save persists the store to file when configured.
func (s *MemoryStore) save() error {
	if s.file == "" {
		return nil
	}
	roles := make([]persistedRole, 0, len(s.roles))
	for _, r := range s.roles {
		roles = append(roles, toPersisted(r))
	}
	return writeJSONFile(s.file, roles)
}

// load reads roles from file when configured. Ignored on failure so a missing
// file simply starts empty. The next-ID counter is resumed past the highest
// stored ID so new rows never collide with persisted ones.
func (s *MemoryStore) load() error {
	roles, err := readJSONFile(s.file)
	if err != nil {
		return err
	}
	for _, r := range roles {
		s.roles[r.ID] = r
		s.byKey[r.Key] = r.ID
		if r.ID >= s.nextID {
			s.nextID = r.ID + 1
		}
	}
	return nil
}
