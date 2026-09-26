package role

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// persistedRole is the on-disk shape of a role.
type persistedRole struct {
	ID          int64     `json:"id"`
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Builtin     bool      `json:"builtin,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func toPersisted(r *Role) persistedRole {
	return persistedRole{
		ID:          r.ID,
		Key:         r.Key,
		Name:        r.Name,
		Description: r.Description,
		Builtin:     r.Builtin,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
}

func fromPersisted(p persistedRole) *Role {
	return &Role{
		ID:          p.ID,
		Key:         p.Key,
		Name:        p.Name,
		Description: p.Description,
		Builtin:     p.Builtin,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
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

// readJSONFile decodes a JSON array of roles from path.
func readJSONFile(path string) ([]*Role, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var persisted []persistedRole
	if err := json.Unmarshal(data, &persisted); err != nil {
		return nil, err
	}
	out := make([]*Role, 0, len(persisted))
	for _, p := range persisted {
		out = append(out, fromPersisted(p))
	}
	return out, nil
}
