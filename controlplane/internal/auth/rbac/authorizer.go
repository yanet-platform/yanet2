package rbac

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/permission"
)

// PermissionStore provides access to permissions.
type PermissionStore interface {
	GetGroupPermissions(groups []string) []permission.Permission
	GetUserPermissions(username string) []permission.Permission
}

// RBACAuthorizer implements role-based access control.
//
// Uses ANY match strategy: if ANY permission matches, access is granted.
type RBACAuthorizer struct {
	permissionStore PermissionStore
	log             *zap.Logger
}

type authorizerOptions struct {
	Log *zap.Logger
}

// AuthorizerOption configures RBACAuthorizer.
type AuthorizerOption func(*authorizerOptions)

// WithLog sets the logger for the authorizer.
func WithLog(log *zap.Logger) AuthorizerOption {
	return func(o *authorizerOptions) {
		o.Log = log
	}
}

func newAuthorizerOptions() *authorizerOptions {
	return &authorizerOptions{
		Log: zap.NewNop(),
	}
}

// NewRBACAuthorizer creates a new RBACAuthorizer.
func NewRBACAuthorizer(permissionStore PermissionStore, options ...AuthorizerOption) *RBACAuthorizer {
	opts := newAuthorizerOptions()
	for _, o := range options {
		o(opts)
	}

	return &RBACAuthorizer{
		permissionStore: permissionStore,
		log:             opts.Log,
	}
}

// Authorize checks if the principal has permission to execute the method.
//
// If any permission matches, access is granted, otherwise access is denied.
func (m *RBACAuthorizer) Authorize(
	ctx context.Context,
	principal *core.Principal,
	fullMethod string,
) error {
	// Collect all permissions (group + user).
	var allPermissions []permission.Permission

	// Get group permissions.
	if len(principal.Groups) > 0 {
		groupPerms := m.permissionStore.GetGroupPermissions(principal.Groups)
		allPermissions = append(allPermissions, groupPerms...)
	}

	// Get direct user permissions.
	userPerms := m.permissionStore.GetUserPermissions(principal.User)
	allPermissions = append(allPermissions, userPerms...)

	// Check if ANY permission matches (first match wins, early exit).
	for _, perm := range allPermissions {
		if perm.Match(fullMethod) {
			m.log.Debug("access granted",
				zap.String("user", principal.User),
				zap.String("method", fullMethod),
			)
			return nil
		}
	}

	// No permissions matched - deny by default.
	m.log.Warn("access denied",
		zap.String("user", principal.User),
		zap.Strings("groups", principal.Groups),
		zap.String("method", fullMethod),
	)

	return status.Error(codes.PermissionDenied, "access denied")
}
