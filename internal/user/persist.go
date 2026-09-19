package user

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// persistedUser is the on-disk shape of a user. PasswordHash and MFA data are
// deliberately excluded from User's JSON tags (API safety) but must survive
// persistence.
type persistedUser struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email,omitempty"`
	PasswordHash string    `json:"password_hash"`
	Nickname     string    `json:"nickname"`
	Role         string    `json:"role"`
	MFASecret    string    `json:"mfa_secret,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func toPersisted(u *User) persistedUser {
	return persistedUser{
		ID:           u.ID,
		Username:     u.Username,
		Email:        u.Email,
		PasswordHash: u.PasswordHash,
		Nickname:     u.Nickname,
		Role:         u.Role,
		MFASecret:    u.MFASecret,
		CreatedAt:    u.CreatedAt,
		UpdatedAt:    u.UpdatedAt,
	}
}

func fromPersisted(p persistedUser) *User {
	return &User{
		ID:           p.ID,
		Username:     p.Username,
		Email:        p.Email,
		PasswordHash: p.PasswordHash,
		Nickname:     p.Nickname,
		Role:         p.Role,
		MFASecret:    p.MFASecret,
		CreatedAt:    p.CreatedAt,
		UpdatedAt:    p.UpdatedAt,
	}
}

// writeJSONFile atomically writes v as pretty JSON to path, creating parent
// directories as needed.
func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// readJSONFile decodes a JSON array of users from path.
func readJSONFile(path string) ([]*User, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var persisted []persistedUser
	if err := json.Unmarshal(data, &persisted); err != nil {
		return nil, err
	}
	out := make([]*User, 0, len(persisted))
	for _, p := range persisted {
		out = append(out, fromPersisted(p))
	}
	return out, nil
}
