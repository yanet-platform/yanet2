package gateway

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/internal/gateway/auth"
	"github.com/yanet-platform/yanet2/controlplane/ynpb"
)

// AuthService provides authentication introspection.
type AuthService struct {
	ynpb.UnimplementedAuthServer

	manager *auth.Manager
}

// NewAuthService creates a new AuthService.
func NewAuthService(manager *auth.Manager) *AuthService {
	return &AuthService{
		manager: manager,
	}
}

// IntrospectToken validates a token and returns principal information.
func (m *AuthService) IntrospectToken(
	ctx context.Context,
	request *ynpb.IntrospectTokenRequest,
) (*ynpb.IntrospectTokenResponse, error) {
	token := request.GetToken()
	principal, err := m.manager.Authenticate(ctx, token)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "authentication failed: %v", err)
	}

	return &ynpb.IntrospectTokenResponse{
		Principal: &ynpb.Principal{
			User:        principal.User,
			Groups:      principal.Groups,
			AuthMethod:  principal.AuthMethod,
			AuthTime:    principal.AuthTime.Unix(),
			IsAnonymous: principal.IsAnonymous,
		},
	}, nil
}
