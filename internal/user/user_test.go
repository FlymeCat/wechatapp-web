package user

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func newTestUser(username, role string) *User {
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret123"), bcrypt.MinCost)
	return &User{
		Username:     username,
		PasswordHash: string(hash),
		Nickname:     username,
		Role:         role,
	}
}

func TestMemoryStoreAutoIncrementID(t *testing.T) {
	s := NewMemoryStore("")
	seen := map[int64]bool{}
	for _, name := range []string{"a", "b", "c"} {
		u := newTestUser(name, RoleUser)
		if err := s.Create(u); err != nil {
			t.Fatal(err)
		}
		if u.ID <= 0 {
			t.Fatalf("Create did not assign a positive ID: %d", u.ID)
		}
		if seen[u.ID] {
			t.Fatalf("duplicate ID assigned: %d", u.ID)
		}
		seen[u.ID] = true
	}
	// IDs must be sequential starting at 1.
	for i := int64(1); i <= 3; i++ {
		if !seen[i] {
			t.Errorf("expected ID %d to be assigned", i)
		}
	}
}

func TestMemoryStoreCRUD(t *testing.T) {
	s := NewMemoryStore("")
	u1 := newTestUser("alice", RoleUser)
	if err := s.Create(u1); err != nil {
		t.Fatal(err)
	}

	// Duplicate username rejected.
	if err := s.Create(newTestUser("alice", RoleUser)); !errors.Is(err, ErrDuplicateUsername) {
		t.Errorf("err = %v, want ErrDuplicateUsername", err)
	}

	got, err := s.GetByUsername("alice")
	if err != nil || got.ID != u1.ID {
		t.Fatalf("GetByUsername: %v %+v", err, got)
	}
	if got.PasswordHash == "" || !got.CheckPassword("secret123") {
		t.Error("password check failed")
	}

	// Update nickname.
	got.Nickname = "Alice"
	if err := s.Update(got); err != nil {
		t.Fatal(err)
	}
	again, _ := s.GetByID(u1.ID)
	if again.Nickname != "Alice" {
		t.Errorf("nickname = %q", again.Nickname)
	}

	// Delete.
	if err := s.Delete(u1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetByID(u1.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestMemoryStoreListOrdered(t *testing.T) {
	s := NewMemoryStore("")
	for _, name := range []string{"c", "a", "b"} {
		if err := s.Create(newTestUser(name, RoleUser)); err != nil {
			t.Fatal(err)
		}
	}
	list, _ := s.List()
	if len(list) != 3 {
		t.Fatalf("len = %d", len(list))
	}
}

func TestMemoryStorePersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "users.json")

	s := NewMemoryStore(path)
	u := newTestUser("persist-me", RoleAdmin)
	if err := s.Create(u); err != nil {
		t.Fatal(err)
	}

	// A fresh store on the same file must see the user.
	s2 := NewMemoryStore(path)
	got, err := s2.GetByUsername("persist-me")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Role != RoleAdmin || !got.CheckPassword("secret123") {
		t.Errorf("reloaded user wrong: %+v", got)
	}

	// Delete persists too.
	if err := s2.Delete(u.ID); err != nil {
		t.Fatal(err)
	}
	s3 := NewMemoryStore(path)
	if _, err := s3.GetByUsername("persist-me"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound after persisted delete", err)
	}

	_ = os.Remove(path)
}

func TestMemoryStorePersistenceResumesIDCounter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "users.json")

	s := NewMemoryStore(path)
	first := newTestUser("first", RoleUser)
	if err := s.Create(first); err != nil {
		t.Fatal(err)
	}

	// A fresh store must not reissue an ID that already exists on disk.
	s2 := NewMemoryStore(path)
	second := newTestUser("second", RoleUser)
	if err := s2.Create(second); err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID {
		t.Fatalf("reloaded store reused ID %d", first.ID)
	}
	if second.ID <= first.ID {
		t.Errorf("new ID %d should exceed existing %d", second.ID, first.ID)
	}
}
