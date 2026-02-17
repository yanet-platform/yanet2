package permission

import (
	"fmt"
	"os"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
)

// FilePermissionStore loads permissions from a YAML file.
type FilePermissionStore struct {
	path string

	mu               sync.RWMutex
	groupPermissions map[string][]*core.Permission
	userPermissions  map[string][]*core.Permission
}

// NewFilePermissionStore creates a new FilePermissionStore.
func NewFilePermissionStore(path string) (*FilePermissionStore, error) {
	m := &FilePermissionStore{
		path:             path,
		groupPermissions: map[string][]*core.Permission{},
		userPermissions:  map[string][]*core.Permission{},
	}
	if err := m.load(); err != nil {
		return nil, fmt.Errorf("failed to load permissions: %w", err)
	}
	return m, nil
}

// GetGroupPermissions returns permissions for the given groups.
func (m *FilePermissionStore) GetGroupPermissions(groups []string) []*core.Permission {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*core.Permission
	for _, group := range groups {
		if perms, ok := m.groupPermissions[group]; ok {
			result = append(result, perms...)
		}
	}
	return result
}

// GetUserPermissions returns direct user permissions.
func (m *FilePermissionStore) GetUserPermissions(username string) []*core.Permission {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if perms, ok := m.userPermissions[username]; ok {
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
	newGroupPerms := map[string][]*core.Permission{}
	for _, gp := range file.Permissions.Groups {
		perms, err := compilePermissions(gp.Permissions)
		if err != nil {
			return fmt.Errorf("group %q: %w", gp.Name, err)
		}
		newGroupPerms[gp.Name] = perms
	}

	// Parse user permissions.
	newUserPerms := map[string][]*core.Permission{}
	for _, up := range file.Permissions.Users {
		perms, err := compilePermissions(up.Permissions)
		if err != nil {
			return fmt.Errorf("user %q: %w", up.Username, err)
		}
		newUserPerms[up.Username] = perms
	}

	m.mu.Lock()
	m.groupPermissions = newGroupPerms
	m.userPermissions = newUserPerms
	m.mu.Unlock()

	return nil
}

// compilePermissions compiles a list of permission patterns.
func compilePermissions(patterns []string) ([]*core.Permission, error) {
	result := make([]*core.Permission, 0, len(patterns))
	for _, pattern := range patterns {
		perm, err := core.NewPermission(pattern)
		if err != nil {
			return nil, err
		}
		result = append(result, perm)
	}
	return result, nil
}
