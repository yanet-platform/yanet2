package gateway

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	"github.com/yanet-platform/yanet2/controlplane/ynpb"
)

// AuthService provides authentication introspection.
type AuthService struct {
	ynpb.UnimplementedAuthServiceServer

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

	// IntrospectToken is not bound to a specific method, so we pass
	// an empty RequestInfo.
	requestInfo := &core.RequestInfo{}
	principal, err := m.manager.Authenticate(ctx, token, requestInfo)
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
