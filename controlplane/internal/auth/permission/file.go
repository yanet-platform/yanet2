package permission

import (
	"fmt"
	"os"

	"github.com/yanet-platform/yanet2/common/go/rcucache"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/loader"
)

type GroupName = string
type UserName = string

// FilePermissionStore loads permissions from a YAML file.
type FilePermissionStore struct {
	groupPermissions *rcucache.Cache[GroupName, []Permission]
	userPermissions  *rcucache.Cache[UserName, []Permission]
}

// NewFilePermissionStore creates a new FilePermissionStore.
func NewFilePermissionStore(path string) (*FilePermissionStore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to load permissions: failed to read file: %w", err)
	}
	store, err := decodePermissions(data, false)
	if err != nil {
		return nil, fmt.Errorf("failed to load permissions: %w", err)
	}
	return store, nil
}

// ParsePolicyPermissions compiles managed grants from verified bytes.
func ParsePolicyPermissions(data []byte) (*FilePermissionStore, error) {
	return decodePermissions(data, true)
}

// GetGroupPermissions returns permissions for the given groups.
func (m *FilePermissionStore) GetGroupPermissions(groups []string) []Permission {
	view := m.groupPermissions.View()

	var result []Permission
	for _, group := range groups {
		if perms, ok := view.Lookup(group); ok {
			result = append(result, perms...)
		}
	}
	return result
}

// GetUserPermissions returns direct user permissions.
func (m *FilePermissionStore) GetUserPermissions(username string) []Permission {
	view := m.userPermissions.View()

	if perms, ok := view.Lookup(username); ok {
		return perms
	}
	return nil
}

func decodePermissions(data []byte, strict bool) (*FilePermissionStore, error) {
	var file struct {
		Permissions struct {
			Groups []struct {
				Name        string   `yaml:"name"`
				Permissions []string `yaml:"permissions"`
			} `yaml:"groups"`
			Users []struct {
				Username    string   `yaml:"username"`
				Permissions []string `yaml:"permissions"`
			} `yaml:"users"`
		} `yaml:"permissions"`
	}

	if err := loader.DecodeYAML(data, &file, strict); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}
	if strict && file.Permissions.Groups == nil && file.Permissions.Users == nil {
		return nil, fmt.Errorf("missing group and user permission lists")
	}

	// Parse group permissions.
	groupPermissions := map[string][]Permission{}
	for _, gp := range file.Permissions.Groups {
		if _, exists := groupPermissions[gp.Name]; strict && (exists || gp.Name == "") {
			return nil, fmt.Errorf("empty or duplicate permission group %q", gp.Name)
		}
		perms, err := compilePermissions(gp.Permissions)
		if err != nil {
			return nil, fmt.Errorf("group %q: %w", gp.Name, err)
		}
		groupPermissions[gp.Name] = perms
	}

	// Parse user permissions.
	userPermissions := map[string][]Permission{}
	for _, up := range file.Permissions.Users {
		if _, exists := userPermissions[up.Username]; strict && (exists || up.Username == "") {
			return nil, fmt.Errorf("empty or duplicate permission user %q", up.Username)
		}
		perms, err := compilePermissions(up.Permissions)
		if err != nil {
			return nil, fmt.Errorf("user %q: %w", up.Username, err)
		}
		userPermissions[up.Username] = perms
	}

	return &FilePermissionStore{
		groupPermissions: rcucache.NewCache(groupPermissions),
		userPermissions:  rcucache.NewCache(userPermissions),
	}, nil
}

// compilePermissions compiles a list of permission patterns.
func compilePermissions(patterns []string) ([]Permission, error) {
	out := make([]Permission, 0, len(patterns))
	for _, pattern := range patterns {
		perm, err := NewPermission(pattern)
		if err != nil {
			return nil, err
		}

		out = append(out, perm)
	}

	return out, nil
}
