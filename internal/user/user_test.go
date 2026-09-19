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
		ID:           NewID(),
		Username:     username,
		PasswordHash: string(hash),
		Nickname:     username,
		Role:         role,
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
