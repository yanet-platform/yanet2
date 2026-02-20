package permission

import (
	"fmt"

	"github.com/gobwas/glob"
)

// Permission represents an access control rule with compiled glob matcher.
type Permission struct {
	// pattern is the compiled glob matcher for the permission pattern.
	pattern glob.Glob
}

// NewPermission creates a Permission with compiled glob pattern.
func NewPermission(pattern string) (Permission, error) {
	compiled, err := glob.Compile(pattern)
	if err != nil {
		return Permission{}, fmt.Errorf("invalid permission pattern %q: %w", pattern, err)
	}

	return Permission{
		pattern: compiled,
	}, nil
}

// Match checks if the permission pattern matches the given gRPC method.
func (m Permission) Match(fullMethod string) bool {
	return m.pattern.Match(fullMethod)
}
