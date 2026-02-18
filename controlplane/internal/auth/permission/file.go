package permission

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/common/go/rcucache"
)

type GroupName = string
type UserName = string

// FilePermissionStore loads permissions from a YAML file.
type FilePermissionStore struct {
	path string

	groupPermissions *rcucache.Cache[GroupName, []Permission]
	userPermissions  *rcucache.Cache[UserName, []Permission]
}

// NewFilePermissionStore creates a new FilePermissionStore.
func NewFilePermissionStore(path string) (*FilePermissionStore, error) {
	m := &FilePermissionStore{
		path:             path,
		groupPermissions: rcucache.NewEmptyCache[GroupName, []Permission](),
		userPermissions:  rcucache.NewEmptyCache[UserName, []Permission](),
	}
	if err := m.load(); err != nil {
		return nil, fmt.Errorf("failed to load permissions: %w", err)
	}
	return m, nil
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

// load reads and parses the permissions file.
func (m *FilePermissionStore) load() error {
	buf, err := os.ReadFile(m.path)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

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

	if err := yaml.Unmarshal(buf, &file); err != nil {
		return fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Parse group permissions.
	groupPermissions := map[string][]Permission{}
	for _, gp := range file.Permissions.Groups {
		perms, err := compilePermissions(gp.Permissions)
		if err != nil {
			return fmt.Errorf("group %q: %w", gp.Name, err)
		}
		groupPermissions[gp.Name] = perms
	}

	// Parse user permissions.
	userPermissions := map[string][]Permission{}
	for _, up := range file.Permissions.Users {
		perms, err := compilePermissions(up.Permissions)
		if err != nil {
			return fmt.Errorf("user %q: %w", up.Username, err)
		}
		userPermissions[up.Username] = perms
	}

	// Tiny out-of-sync is possible, but since users and groups permissions are
	// independent, it's not a problem.
	m.groupPermissions.Swap(groupPermissions)
	m.userPermissions.Swap(userPermissions)

	return nil
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
